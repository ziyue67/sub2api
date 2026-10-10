# BPS 新子代理任务完整性

BPS 无法读取 Codex 的加密任务正文。末尾的新 `agent_message` 含顶层 `encrypted_content` 或 content 中的加密片段时，在省略历史、图片压缩和上游生成之前返回 400，并提供脱敏路径。末尾跟随 `additional_tools`、`tool_search_output` 或 compaction trigger 不会隐藏新任务。

管理员已有的忽略加密内容开关继续支持旧历史：已被后续消息推进的加密 agent message 可以替换为明确省略提示，保留原位置和可读内容；现在也覆盖顶层加密字段。关闭该开关时，加密历史仍被拒绝，不把密文写成归属元数据。

`collaboration.spawn_agent`、`send_message`、`followup_task` 的 `message.encrypted` 仅在明文提示词展示层移除；原 schema、校验、缓存和输出 `encrypted_function_args: []` 保留。本改动没有开放 BPS 的 v2 manifest 声明。

无配置/迁移。旧版会接受的“新任务只有可读标题、正文已省略”现在会被拒绝，需要客户端重发任务原始明文。

验证：协议、service 包测试及真实 Forward 的 mock 上游检查，确认新任务在任何提交前拒绝。未做真实客户端嵌套协作验收。

行为参考：[Nonary/ghcp_proxy f89ccfc](https://github.com/Nonary/ghcp_proxy/commit/f89ccfcea4497c4e60be891119a389a3cb68b050)。独立 Go 实现。关联 #357 第 2 项、#153 的历史兼容。
