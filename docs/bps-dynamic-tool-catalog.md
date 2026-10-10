# BPS 动态工具目录

Responses 顶层 `tools` 是当前声明，优先于历史 `additional_tools` / `tool_search_output.tools`。只存在回放声明时，同名工具采用最后一次声明；已有会话目录只是缺省值，新的回放声明可以更新它。未发送 `tools` 的后续请求仍按账号、API key 和可信会话作用域继承目录。

目录项不作为上游消息发送；`tool_choice=none` 不收集工具。当前声明自身的冲突和最终 schema 校验仍然生效，错误不会提交到会话缓存。规则同时保留 namespace 与工具调用原名。

这改变了旧版对“当前定义与旧历史 schema 不同”的拒绝行为。历史调用的完整参数/结果与 native replay 仍走原有路径。无配置、数据库迁移或新模型请求。

验证：basispoints 与 service 包测试；新增动态目录、schema 优先级、更新/继承、作用域隔离及参数拒绝用例。未进行真实客户端或上游生成验收。

行为参考：[Nonary/ghcp_proxy f89ccfc](https://github.com/Nonary/ghcp_proxy/commit/f89ccfcea4497c4e60be891119a389a3cb68b050)，使用现有 Go bridge 独立实现。关联 #357，第 1 项。
