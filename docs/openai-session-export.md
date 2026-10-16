# OpenAI 账号导出 ChatGPT Session

账号管理中，OpenAI OAuth 账号的“更多操作”菜单新增“导出 ChatGPT Session”。

1. 在浏览器中登录该账号的 ChatGPT，打开开发者工具的 Network 面板，访问 `https://chatgpt.com/api/auth/session`。
2. 从该请求的 Request Headers 中复制 `Cookie` 的值，至少包含 `__Secure-next-auth.session-token`；若 Cookie 被分块，需包含全部 `.0`、`.1` 等分块。
3. 在账号管理中打开该账号的“更多操作 → 导出 ChatGPT Session”，粘贴 Cookie，然后点击“请求并下载 Session”。启用敏感操作二次验证时，按提示完成验证。
4. 服务端使用该账号配置的代理向 `https://chatgpt.com/api/auth/session` 发送 GET 请求。成功后下载 `chatgpt-session-<账号ID>.json`，保留上游响应字段。

直接在已登录的浏览器打开上述地址时，浏览器会自动发送 ChatGPT Cookie，因此无需手动输入；这不代表接口无需登录态。Sub2API 服务端无法使用用户浏览器的 ChatGPT Cookie，Sub2API 页面也不能直接读取另一个站点的 Session 响应。当前功能仍需提供 Cookie，不支持仅凭已保存的 OAuth token 一键导出。

Cookie 只用于本次请求，不保存到账号配置或审计日志。导出的 Session 文件包含登录凭据，应按敏感文件保管，不要提交到 Git 或公开附件中。OAuth access/refresh token 不能代替网页登录 Cookie；没有 Cookie 时不会合成或下载 Session。

支持持有独立凭据的 OpenAI OAuth 账号；API Key 账号和 Spark 影子账号不支持。系统会优先比对 `user.id` 与账号的 `chatgpt_user_id`，后者缺失时比对邮箱；无法确认身份或身份不一致时拒绝导出。此校验确认用户身份，不保证 Session 属于某个特定工作区。缺少账号身份元数据时，需要先完善账号身份信息。

关闭导出窗口、切换账号或离开页面后，晚到的响应和二次验证要求都会被丢弃，不再弹出验证框或下载文件。

Cookie 过期、未登录、ChatGPT 安全验证、代理不可用、请求超时、上游重定向或无效响应都会明确报错，不会生成文件。代理查询失败时不会改走直连。单次上游请求超时为 20 秒，响应上限为 1 MiB，不跟随重定向。

## 管理 API

`POST /api/v1/admin/accounts/:id/export-session`

请求 JSON：`{"cookie":"__Secure-next-auth.session-token=<网页登录 Cookie 值>"}`。需要现有管理员认证；观察员仅可操作授权分组内的账号；沿用账号导出的 step-up 策略。响应通过标准 API envelope 的 `data` 字段返回真实 Session JSON，设置 `Cache-Control: no-store`。该接口不保存 Cookie 或 Session，不修改账号 token，不进行 token 刷新。

## 界面截图

以下截图使用本地离线组件预览和虚构账号，展示菜单与弹窗；没有连接真实 ChatGPT 账号。

修改前：

[修改前的 OpenAI 账号菜单](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/images/openai-session-export-before.png)

修改后：

[新增 Session 导出入口](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/images/openai-session-export-menu.png)

导出弹窗：

[Session 导出弹窗](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/images/openai-session-export-dialog.png)
