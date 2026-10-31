# 账号连接测试耗时

## 1. 范围与基线

目标为 ranxi2001/sub2api 的 production，基线 `77455cf755fa0f119367b911704f11df88e7917e`。
本功能在隔离工作树实现，提前发布 [Draft PR #602](https://github.com/ranxi2001/sub2api/pull/602)。
只增加账号连接测试的可观察时间和 UI，不改变 hi/prompt、凭据选择、请求次数、计费或普通路由。
保留基线已合并的 API-key/OAuth 管理边界修复，无 migration、配置开关或生产数据要求。

## 2. 真实调用链与采集边界

两处真实入口 `components/account/AccountTestModal.vue` 和
`components/admin/account/AccountTestModal.vue` 均 POST `/api/v1/admin/accounts/:id/test`，
经过 `admin.AccountHandler.Test` → `AccountTestService.TestAccountConnection` → 实际平台分支。
handler 在解析参数前保存开始时间，service 建立独立 requesttiming collector；后台直接调用从 service 入口开始。
service 退出时先完成平台函数的 body close 和共用 keepalive 清理，再生成最终计时；不含 handler 后续限流恢复。
非法 account ID 的 HTTP 400 仍走原验证响应；UI 显示未收到服务端指标，不伪造测试时间。

| 实际分支 | 连接/响应头 | 首内容/流输出 |
| --- | --- | --- |
| OpenAI API-key Responses / Chat Completions、OAuth Codex Responses、compact | 测试适配器 → StartAttempt → repository StartTransport/httptrace | 四种真实流解析器之一；compact 的缓冲解析不冒充实时边界 |
| Claude API-key/OAuth/Vertex、Gemini API-key/OAuth/service-account、Grok text、聚合平台的实际 SSE 分支 | 同一测试适配器，保留原 TLS/profile/凭据/重试 | Responses / Chat / Claude / Gemini 的非空白可见文本解析点 |
| OpenAI API-key 图片、Bedrock、TypeSafe、Grok 图片/视频/搜索/语音等 HTTP 分支 | 能观察到时采集；多次请求不折算成单个网络阶段 | 非流不适用；诊断文字、上传提示、媒体 URL 不算文本 token |
| BPS/SIWC/Prism、OAuth 图片工具、Antigravity、插件、Grok Realtime 等委托/缓冲路径 | 仅呈现已有 collector 实际采集到的单次 HTTP 边界，否则缺失 | 未暴露实时文本解析时显示 unsupported_path；不从缓冲结果或通用 content 事件推算 |
| synthetic 标记账号、找不到账号、缺凭据、前置失败 | 都有生命周期事件；未发生的网络阶段缺失 | 未发生/未采集，不补 0 |

测试专用 HTTPUpstream 适配器只委托原调用。共享 service 不被复制或修改，不增加重试。
解析器通过 `ResponseContext` 绑定真实响应的 trace，复用 `Output`/`Stage`，不改普通 gateway 的路由行为。
参见 [已有分段计时说明](stream-timing.md)。

## 3. 测量口径与重叠

所有毫秒值来自同一个 collector 的单调时钟。开始/完成是带时区的 RFC3339 时间，
完成戳由开始时间加单调总时长得到，避免墙钟调整改变经过时间。UI 转为浏览器本地时间，并明确显示 IANA 时区与 UTC 偏移。

| 项目 | 起点 → 终点 | 与其他项目关系 / 缺失口径 |
| --- | --- | --- |
| 总耗时 | handler/service 开始 → service 完成清理、冻结 collector | 包含其余阶段；不含最终 timing 事件写出与 handler 后续状态恢复 |
| 获取连接 | HTTP GetConn → GotConn | 包含连接池、DNS、TCP、TLS 等待；复用连接显示 — + connection_reused，不声明新建连接耗时 |
| 等待响应头 | WroteRequest 成功 → Client.Do 返回完整响应头 | 在解压初始化之前标记，不能用首字节冒充；包括上游等待，不是纯网络传输 |
| 首内容 token | 单次上游调用开始 → 首个解析出的非空白可见文本 | 包含连接、头等待、读取/调度/解析；是内容观测，不是 tokenizer 或模型内部计时 |
| 流输出 | 首可见文本 → 解析器完成/错误/取消返回 | 包含后续读取、解析、调度、本地写出；不是客户端接收时间，可与本地处理重叠 |
| 本地处理（事件编码） | 每次 SSE JSON 编码的显式 Observe 起止 | 时长为不连续编码片段之和；start/end 是所有片段的外包区间，segments 表示数量。排除网络读、写出/背压、最终 timing 事件和其他未采集工作 |

首次语义内容不包括 SSE 注释、心跳、metadata、空字符串、纯空白或首 byte。
文本必须来自具体协议解析器，不根据 `sendEvent(content)` 盲目采集，避免把状态说明或上传提示当成 token。
非流的首内容与流输出显示 `—` + non_stream；成功但未提供实时解析边界的分支说明不可测。
网络阶段只有单次 egress 才呈现；有单次物理 HTTP trace 时优先使用，物理重试的连接/头等待不合成一段。
多次 egress 的首内容/流输出也不跨请求合成。采集上限触发后，省略不完整阶段并注明 collector_limit。

各项有重叠，不能相加；不得用“总耗时减网络”命名为模型计算或本地 CPU 时间。
缺失值不序列化数值，UI 显示 `—` 和原因；真实观测到的 0 保持有效。

## 4. 事件、结果与资源生命周期

增加可选 `timing` SSE 事件，既有 `test_start`/`content`/`status`/`image`/`test_complete`/`error` 保留。
开始事件 outcome=running，无 finished_at/total_ms；最终事件包含不可变白名单结果，outcome 为 success/error/timeout/canceled。
不暴露任意错误字符串。保持原业务错误事件；超时也通过 trace 的规范错误类型识别，避免字符串化后丢失分类。
Claude/Gemini 在没有协议终止标志的 EOF 现在返回明确错误，与 Responses/Chat 的不完整流口径一致。

keepalive 停止并等待协程退出后才发最终事件。上游 body 仍由原平台函数关闭，原 repository 负责 in-flight 释放。
取消时最终事件可能无法送达；UI 单独标注“此浏览器取消，未确认服务端完成”，不伪造服务端完成戳。
两处 UI 共用 reader，在中断时取消读取，退出时 releaseLock；新测试、换账号、关闭和卸载都会隔离旧请求事件。
旧服务端仅发旧事件时保留原业务结果并显示未提供计时；部分新流缺最终事件时显示不完整，避免当成旧协议成功。

## 5. UI 与兼容性

摘要显示开始/完成（明确本地时区）、总耗时、首内容和结果。
阶段详情用原生 details，默认折叠，包含边界、偏移、重叠说明和不可测原因。
支持英文/中文；小屏单列，长时区及说明可换行。
旧客户端忽略未知 timing 事件即可继续消费。无持久化或 DTO 数据库字段，不需要迁移或重新计费。

## 6. 安全与范围审查

公开 DTO 是固定字段白名单：时间戳、范围/时长、片段数、模式、结果、稳定原因码。
禁止嵌入 Snapshot、Attempt、任意 event map、route、proxy/account ID、IP、URL、headers、credentials、请求/响应正文或原始错误。
测试包含敏感形状的虚构路由/ID，校验投影不含这些字段；不以真实生产配置或账号验证。
远端 fixture 的 adapter 只把虚构请求转向进程内 httptest HTTP，仍使用真实 repository transport；浏览器只重放这些 synthetic SSE。
截图/raw artifact 不提交到源码。未使用付费/生产账号，无 merge/release/deploy。

## 7. 回归与远端验收方法

本机仅文件阅读、编辑、gofmt、git diff --check 和 git/GitHub 操作；绝不执行 Go build/test/run/generate、Docker 或前端动态命令。
所有动态验证位于 `.github/workflows/account-connection-timing.yml`，checkout 并断言精确 PR head SHA。

- 先写生命周期回归，再实现；remote 在独立 baseline archive 中运行同一测试，要求是实际断言失败的 RED，不能以编译失败冒充 RED，不回滚交付 checkout。
- 前端同一真实组件事件序列回归在独立 baseline archive 验证 RED；GREEN 使用当前 head。
- `go test -race -tags=unit ./internal/service ./internal/handler/admin ./internal/integration/accountconnectiontiming -run ...`：真实 packages 编译与 synthetic 用例，保留 API-key/OAuth 管理边界回归，并运行既有账号、图片、compact、BPS、Grok 等定向回归。
- `go test -race ./internal/pkg/requesttiming`：全部 collector 测试；可控 clock 验证具体毫秒、首事件不覆盖、范围、分段累计和头时间不被正文准备覆盖。
- `go test ./internal/service ./internal/handler/admin -run '^$'`：无 unit tag 的普通 packages 编译；不使用 file-list 伪装集成。
- 真实 admin handler → service → repository HTTP transport → synthetic SSE：通道门控制 headers/content/terminal，无窄 sleep 范围断言；API-key/OAuth Responses、Chat、HTTP 拒绝、EOF、metadata-only、非流；可控取消/超时与真实客户端断开；同一服务实例的并发请求隔离。
- handler 捕获的 `.sse` artifact 由两处真实 Vue modal 通过 fetch/reader 消费，覆盖中英、旧协议、缺终态和取消清理；不是仅 mock timing 字段。另跑类型、定向 lint、locale 完整性。
- 远端 Chromium 挂载两处实际组件，重放同一个 handler artifact，验证英文/中文、Asia/Seoul UTC+09:00、默认折叠、390px 无水平溢出，并保存截图与 verification.json。

验证状态以 PR 中当前 exact head 的 run/job 链接、已读取的 raw 和 artifact 为准，本文不把待运行项写成通过。
没有进行真实厂商/生产联调；委托路径只展示实际可观察的阶段。浏览器证据来自隔离 Vite harness，而非生产部署。

## 8. Agent 披露与交付

Hermes 协调模型 gpt-6.1-sol；推理强度不适用（工具未提供）。
Codex 是唯一实现者，未派生子 Agent。Hermes 另有独立验收子 agent，模型 gpt-6.1-sol，推理强度未知（runtime 未提供），参与独立静态与远端 raw 验收。
本次 Codex session `01a18d6e-f0f1-72c1-8e3e-84c145366772` 的
turn_context.model=`gpt-6.1-sol`、effort=`xhigh` 已从本机 session 元数据核实，cwd 匹配隔离工作树；不公开原始 session。
分支允许且已执行 commit/push/create Draft；PR 保持 Draft，不 merge、不 release、不 deploy。
完整 8 节 PR 正文持续更新实际结果、失败修复、未覆盖项及当前 exact head 证据。
