# 质量运维复用 Jev 判题（Draft）

质量规则的 `quality.judge.kind` 省略或为 `llm` 时保持原来的普通分组模型判题。新增 `jev` 明确选择复用安全审计保存的 TypeSafe 配置，不接入普通账号选号，也不允许失败时回退普通模型。现有内置确定性判题路径保留。

Jev 使用已有原生 `/v1/systemone`，不是 Chat Completions 模型。公开协议来源：[TypeSafe OpenAPI](https://api.typesafe.ai/openapi.json)（2026-10-27 读取）；仓库实现见 `backend/internal/pkg/typesafe/client.go` 与 `systemone.go`。

- `noul` 是某个问题为是/真的概率，范围 `[0,1]`，不是通用质量分。
- `choice` 从 criteria 中选择概率最高的一项，附置信度及各项概率。
- `score` 是有序 criteria 等级下标的概率加权期望，可为小数，不能任意解读为百分制。

使用独立的质量 `choice`（correct / incorrect / unknown），不读取安全违规概率或审计阈值。`correct`/`incorrect` 只有同时满足置信度与对应选项概率至少 0.9 才采纳；这是未经真实语料校准的保守起点，不是准确率承诺。其余保留 unknown。严格校验选项、有限值 `[0,1]`、完整概率分布（总和容差 0.001）、最大概率选项、重复字段和类型；拒绝响应、缺字段、格式错误不补零。

题目、参考答案、候选回答分别置于 JSON state 的 `question`、`reference_answer`、`candidate_answer`，字节上限为 32000/4000/64000，state 序列化后上限 128000，拒绝无效 UTF-8。站长判题提示词上限 16000，单独进入 instructions；三个 criteria 固定，不插入候选回答。固定约束要求把 state 当数据，不执行角色标记或其中的命令，不重新解题；拒绝作答与证据不足应选 unknown。模型判断仍可能受注入影响，结构隔离不能保证语义准确率。

复用语义：安全审计关闭、模式 off 或全局风控关闭时不发送 Jev 请求，返回 unknown 与明确原因；不会为了判题打开审计、修改其配置或触发审计副作用。缺少配置、凭据失败、限流、超时、取消和非法响应也只是不确定，不能作为质量不合格触发停调或作为恢复依据。

启用条件与活动审计引擎独立：即使当前活动引擎是 OpenAI，只要全局风控与内容审计开启、mode 不是 off，保存的 TypeSafe profile 仍可复用。TypeSafe profile 的模型须为 `jev-latest`。读取仅取既有设置并解析，不走审计 Check、运行快照刷新、hash、队列、处罚、通知或审计记录入口。

站长在质量规则编辑器选择「Jev（复用安全审计 TypeSafe 配置）」，填写判题提示词，不需要判题分组或普通模型。观察员无权通过旧定时测试写 API 选择 Jev 或启用已保存 Jev 规则。设置读取使用服务内部现有权限路径；质量 API/预设只含 kind 与 rubric，结果只含安全 reason code、模型标识、规则版本与独立耗时，不包含 Key、URL、提供商错误原文或用量。密钥仍由安全审计设置管理。

```json
{"judge":{"kind":"jev","group_id":0,"model_id":"","prompt":"比较参考答案与完整回答，无法可靠比较时选择 unknown。"}}
```

客户端复用已有 systemone 请求、URL 规范化、有界响应读取（4 MiB）与 HTTP 连接池；额外使用既有上游 URL allowlist 和解析 IP 校验，不跟随重定向。开启 URL 白名单时需允许已保存 TypeSafe 域名；代理失败不会直连回退。API key、URL query/userinfo/fragment 不会进入结果。私有主机是否允许沿用服务端 URL 安全策略，不能通过质量配置单独放宽。

每个服务进程最多 4 个 Jev 判题并发；满额立即 unknown，不排无界队列。全程最多 30 秒，每次沿用审计 timeout，最多 `min(retry_count+1,3)` 次。仅显式 401/403/429/529/5xx 拒绝可在预算内轮换；传输失败、超时、已接受但非法响应、400/413/422 不重试。每次尝试重读配置，Key 轮换/禁用在下一尝试或下一判题生效；已在途请求不能追溯撤回。质量路径复用 Key 轮换/冷却算法但隔离内存状态，不改变审计 Key 的健康和选择；仍共享上游实际额度，可能竞争提供商限流。

被测生成继续走原有账号与通道；Jev 只使用审计 profile，不选择任何被测或普通业务账号，不写账号健康/质量/延迟采样。Jev 结果 `quality_judgment.latency_ms` 是独立判题耗时，`latency_ms` 保留被测生成耗时；`finished_at` 包含整个样本处理完成时间。结果进入既有 quality_round_id、模型隔离、ownership、完整轮次、停止和恢复流程；已有探针、准入和上游 5xx 固定糖果题确定性分支仍先执行。

不增加账本、支付、结算或虚构的 Jev token/成本指标；未测量速度与成本，不承诺收益。本任务不调用真实 Jev，不部署、不合并。离线 mock 证明协议与安全门，不证明真实判题准确率、抗注入能力或收益；PR 保持 Draft，真实语义校准和人工 UI 验收待单独完成。

回滚前先将 Jev 规则改回普通 LLM 或禁用；旧二进制不理解 `kind`，不能直接用旧版本执行 Jev 规则。持久化复用既有计划与判题 JSON 字段，无新增迁移。

英文说明：Omitted `kind` and `llm` preserve ordinary grading. `jev` explicitly reuses the saved TypeSafe audit profile through native System One choice grading. Disabled auditing or risk control prevents the call. Missing credentials, rejection, rate limits, cancellation, timeout, refusal and invalid output remain inconclusive with no ordinary-account fallback. This change has no measured speed/cost claim and has not called a real Jev service.

真实历史回放确认现有审计端点在原生信封增加 `id` / `provider`。仅将这两个字段作为可选、非空、无控制字符的字符串兼容，UTF-8字节上限分别128/64；忽略其值，不记录或返回。其他未知顶层字段、别名、所有层级重复键、超过16层的嵌套或4 MiB响应继续拒绝，choice/置信度/概率门不变。公开OpenAPI未声明这两个扩展，本兼容范围来自既有端点的受限结构证据，不泛化成任意metadata。匿名fixture全部为虚构值；旧完整head的预期测试失败与新head通过在远端CI保留日志。
