# Excel OAuth 与 BPS 准入对照（2026-10-08）

同一自有测试账号、工作区、代理配置、curl 客户端与 BPS 请求格式，只切换凭据来源。模型请求为 `gpt-6-astra`、medium、流式，提示 `Reply with OK only.`，不带借用 Cookie、不重试。

| 步骤 | 结果 |
| --- | --- |
| 原 Codex OAuth 调 BPS | HTTP 403，JSON 错误关键词 blocked，1.340 秒 |
| 同账号经官方 Excel PKCE 浏览器授权后的 Token | HTTP 200，response.completed，回答 OK，14.709 秒 |
| Excel RT 按其原客户端刷新 | HTTP 200，RT 已轮转 |
| 刷新后的 Excel Token 再调用 BPS | HTTP 200，response.completed，回答 OK，22.973 秒 |
| 原 Codex RT 以 Excel 客户端调用刷新端点 | HTTP 401 / invalid_client，未产生新凭据 |

两次成功响应声明的模型都是 `gpt-5.6-luna`，与请求 Astra 不同。这里确认的是测试账号的 BPS 403 被新的授权会话解除，未证明 Astra 模型质量恢复、工具往返可用或其他账号也能恢复。生产自然流量未隔离，代理配置相同不等于独立测量了公网 IP。

两份 JWT 解码后，issuer、audience、用户和工作区一致。旧客户端为 `app_EMoamEEZ73f0CkXaXp7hrann`，新客户端为 `app_fnr0pYvVwwFDocDumLG3H2Bp`；新会话 scope 包含 `organization.read`。这些是声明对比，不是独立的 JWT 签名验证，也不是证明只有某个字段决定准入。不能修改签名 JWT 或给旧 RT 换个 client_id 来制造新授权。

此外，同一旧 Token 补齐 CPA v0.2.10 的请求头后仍返回 403；没有据此证明所有请求头差异都无关。先前“全部账号被风控”的结论应撤回，原结果只证明当时的凭据与请求路径被拒绝。

## 官方协议核对

从[官方 manifest](https://bps.openai.com/basispoints/api/office/manifest.xml)定位并读取 [x-square-G80magJa.js](https://bps.openai.com/basispoints/extension/360590d7-f8f9-4d88-bf75-0edfe0a4b9f3/assets/x-square-G80magJa.js)：

- 授权：`https://auth.openai.com/api/accounts/authorize`，PKCE S256、Excel client ID、`platform=PC`、`audience=https://api.openai.com/v1`。
- scope：`openid offline_access email profile organization.read`。
- 浏览器回调：官方 extension 路径下的 `/auth/callback`，state 使用 `bps.<随机值>.PC`。
- 令牌交换和刷新：`https://auth.openai.com/oauth/token?unified=true`。
- 刷新表单只含 refresh grant、RT、client ID，不附加 Codex scope。

设备码流程在测试登录中收到 `deviceauth_disabled`，因此改用普通浏览器授权。普通回调页因没有 Office 宿主显示失败，但在保存本轮 PKCE verifier、核对 state 后仍成功兑换。PR 复用后台已有的手动回调粘贴流程。

## 代码验收边界

真实上游对照使用独立脚本及浏览器，未部署本 PR 二进制。后端回归覆盖旧导入器覆盖 client ID 的失败复现、修复后的凭据保留/去重、state/redirect 绑定、Excel 交换/刷新契约和错误正文保护；前端验证覆盖 BPS OAuth 入口、创建参数、模型开关、原 OAuth 路径及 locale key。界面截图为真实 Vue 组件本地预览，不是生产验收。

参考项目：

- [1812095643/sub2api-excel2api-plugin](https://github.com/1812095643/sub2api-excel2api-plugin/commit/ea32f88a64ac625164c5cf9313c0b24bc4addb75)：最新提交 2026-09-25，流解析修复；未见 Excel 客户端授权实现。
- [2han9wen71an/cpr-plugin-oai-basispoints v0.4.0](https://github.com/2han9wen71an/cpr-plugin-oai-basispoints/commit/bdb5302beacbe09c227bcc978ae56809e02beaf9)：2026-10-06 更新 CPR 宿主接口，仍复用宿主 Codex OAuth。
- [CPA v0.2.10](https://github.com/JaxsonWang/cpa-plugin-oai-basispoints/releases/tag/v0.2.10)：2026-09-30 更新凭据派生请求头；本次没有复制其源码。

报告不包含真实邮箱、用户/工作区 ID、Token、授权码、PKCE verifier 或完整回调 URL。账号数据和生产配置未因本次对照被修改。
