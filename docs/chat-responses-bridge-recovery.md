# Chat → Responses 流式桥的恢复边界

适用路径：`/v1/chat/completions` 入站、Responses SSE 上游、Chat SSE 下游。
OAuth 原生 Responses 账号以及走这条桥的兼容账号均受影响；raw Chat 直转路径不受本次改动影响。

## 修复的触发条件

上游可能先返回 `response.created`，随后以 `error` 或 `response.failed` 报容量过载。
旧实现把 created 转成 assistant 角色帧立即下发（小于 64KiB 的请求尤为明显），使这次尝试已提交，无法再进入已有重试/切号流程。
此时首个协议事件已计作首字，最终失败又可能被 Ops 记成 `Recovered upstream error / 200`。

## 修改后的行为

- 角色等元数据先暂存；在正文、推理、拒绝文本或工具调用出现时，按原顺序释放。
- 在实际输出前不发送桥内部的保活注释，以免保活固化响应、阻断恢复。
- 实际输出前的可重试错误返回既有 `UpstreamFailoverError`，沿用原有有界重试与切号预算；不新增永久禁号或冷却策略。
- 已输出正文、推理或工具调用后不重放请求；在同一流上返回错误，并明确标记为请求失败，供 Ops/SLA 统计使用。
- 首字时间从首个可用的 Chat 输出块开始；角色、usage、结束帧和过载错误不会产生首字时间。
- 正常完成仍保留 usage、角色帧顺序与单个 `[DONE]`；部分失败已取得的用量继续保留。

不新增配置、依赖或数据库迁移。回滚代码即可恢复原行为；历史错误行不自动回填。
输出前延后响应头/保活可能暴露原有反向代理空闲超时，需要按现有超时策略评估。
用户/账号并发队列在进入桥之前产生的保活不属于本次修复范围。

## 验收与证据边界

离线用例位于 `backend/internal/service/openai_gateway_chat_bridge_recovery_test.go` 和
`backend/internal/handler/openai_chat_bridge_recovery_test.go`。
覆盖小/大请求、同步/保活读取路径、裸错误与 failed 事件、部分输出与工具重放保护、成功结束、切号恢复和重试耗尽。
用例是协议 mock，不是生产上游复测；本次未在本地执行测试或构建，交由 CI 验证。

本修复解决网关的恢复及观测缺口，不承诺消除上游过载；源码 PR、发布和生产部署分别验收。
