# DeepSeek / OpenAI 兼容分组故障切换

## 问题与复现

基线为 `ziyue67/sub2api` main `d8db267ee288708636177fbfdd547ecc9ee90747`。
同一个 API Key 绑定 DeepSeek 和 OpenAI 分组，两组账号都明确支持同一模型，且
`group_routing.failover=true`。首次选中分组的账号返回可重试 503 后，备用组没有接替。

首次按模型选择分组与本次问题不同：原实现的两个主分组方向、legacy / lowest_rate /
priority_weighted 六个测试均通过。跨平台故障切换的两个方向均失败，同平台对照通过。

完整 handler 测试也在修复前失败：预期先调用账号 101，再调用账号 102，实际只调用
101 并返回 502。HTTP 上游由内存替身实现，没有收费请求或生产连接。

## 根因

1. `service/api_key_unified_routing.go` 的 `nextUnifiedGroup` 将备用组严格限制为
   与当前组相同的 platform；`deepseek` 和 `openai` 虽使用同一个兼容网关，仍被排除。
2. `handler/openai_chat_completions.go` 在重试循环外计算 requestPlatform，换组后
   调度器仍可能用旧平台筛选账号。
3. 在放通换组后的计费回归中，Chat Completions 成功路径暴露出重复用量提交：
   原有直接 RecordUsage 任务与后新增的 submitChatUsage(result) 都执行。
   模拟仓储记录了两次相同 Usage。该发现不等于已证实生产重复扣款；生产仍有账本去重。

## 改动

- HTTP Chat Completions 和普通 HTTP Responses 显式启用兼容平台文本换组。
  使用平台目录的 `UsesOpenAIGateway` 判断共享网关能力，而非放行所有平台。
- 只有 token 请求可使用这一放宽；其他 handler 未显式启用时保留原来的同平台约束。
- 每轮 Chat 调度重新读取有效组的平台。保留原有模型、用户权限、余额/套餐、冷却、
  禁用分组及账号可用性检查；仍受 API Key 的 failover 开关控制。
- 媒体意图、Responses compact、WebSocket 没有新增跨平台重试权限。
  带 previous_response_id / conversation / thread 引用、item_reference、compaction
  或 encrypted_content 的文本请求同样不启用跨平台重放。
- Chat 成功路径复用 submitChatUsage，去掉重复的直接提交块；错误返回中已有 Usage
  的结算路径保留。
- 无数据库迁移、无新公开端点、无配置默认值变化，不改变模型价格或上游 Usage。

## 测试与验收

```sh
cd backend
go test -tags=unit ./internal/service ./internal/handler \
  -run '^(TestDeepSeekOpenAI|TestOpenAIText|TestCompatibleGroupFailover|TestChatBridgeHandlerRecovery)' -count=1 -v
go test -tags=unit ./...
```

- 两种入口 × 流式/非流式 × 两个跨平台方向：模拟首次 503、备用返回正文及 Usage；
  断言实际账号调用顺序、响应正文、有效组、有效倍率和只写入一条 Usage。
- 真实选组探测（假仓储、真实 ProbeAPIKeyGroup）：双向模型筛选、主组顺序、策略、
  未支持模型拒绝及共享认证快照不变。
- 普通计费服务测试：切换后按备用组倍率计算费用，调用余额扣减一次，日志的分组、
  账号和金额一致。使用内存仓储，未更改任何真实客户余额。
- 关闭 failover、不启用文本兼容标记、禁用备用组、冷却、模型拒绝、账号不可用、
  请求准入拒绝、image 请求以及缺少初始准入记录，均不得通过本次放宽绕过。
- 状态绑定与网关隔离测试：密文和会话引用拒绝跨平台；Anthropic、Gemini、
  composite、未知平台不被当成 OpenAI 兼容备用组。
- 现有 Chat 流中断回归覆盖输出后不重放、输出前恢复和恢复后 Ops 结果。

## 运行边界

需要用户已经开启该 Key 的“跨分组故障切换”，且备用组也支持请求模型。
本修复不会把 GPT 模型自动变为 DeepSeek 模型，不会保证两个上游均故障时仍成功。
有状态或不兼容协议请求维持原有边界。代码修复与本地模拟验证不等同于线上部署，
发布时应单独核验 PR CI、合并后 CI、镜像、部署及有限业务验证。

## 本地验证记录（2026-11-17）

Go 1.27.4 / Windows amd64：上述专项、已有去重回归及专项 race 检查通过。
全量 `go test -p 2 -tags=unit ./...` 已执行但未全绿，包含 POSIX 文件权限、符号链接、
内核子进程和其他未逐项建立基线对照的失败。不能把专项通过表述为全仓回归通过。
本地未执行 PostgreSQL/Redis 集成测试；合并前仍需 Linux CI 和失败项核对。
