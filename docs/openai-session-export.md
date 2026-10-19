# 使用 2FA 导出 ChatGPT Session

凭证运营「2FA 管理」中的“使用 2FA 导出 ChatGPT Session”会使用凭证运营中已经保存的 OpenAI 邮箱、密码和 TOTP，由本地 OpenAI re-auth Worker 完成一次真实的 ChatGPT Web 登录。

## 使用流程

1. 确认账号是独立的 OpenAI OAuth 父账号，并已在凭证运营中配置邮箱、密码和 TOTP。
2. 在凭证运营页面对应账号的“2FA 管理”中选择“使用 2FA 导出 ChatGPT Session”。
3. 完成管理员 step-up 2FA。
4. 管理界面创建异步任务并轮询登录阶段。Worker 会运行 `protocol-login.mjs --web-only --output-mode session`，随后固定请求 `https://chatgpt.com/api/auth/session`。
5. 身份校验成功后，浏览器下载 `chatgpt-session-<account-id>.json`。下载接口只能消费一次。

任务只保存状态和凭据快照哈希，不保存密码、TOTP、Cookie 或 Session JSON。Session 结果仅保存在 API 进程内存中，最多保留 10 分钟；任务最长运行 5 分钟。API 重启会丢弃未下载的结果，必须重新发起导出。

## Worker 能力

Worker claim 请求必须声明 `chatgpt_session_export` 能力。旧版 Worker 不会领取这类任务，仍可继续处理 OAuth 重登任务。新 Worker 通过已认证的 Worker 通道接收短时明文凭据，密码和 TOTP 只通过子进程环境传给登录程序，不出现在命令行、日志或错误回传中。

Worker 必须使用账号配置的代理并禁止自动切换出口。登录完成后必须确认 `web.mfaVerified == true`，Session 响应必须包含未来的 `expires`、`accessToken`，且邮箱及（如果配置）`chatgpt_user_id` 与托管账号一致。导出的 JSON 只保留 `accessToken` 和从 `__Secure-next-auth.session-token`（含分片 Cookie）提取出的 `sessionToken`，其他上游字段全部删除。

## 错误处理

管理界面会分别提示凭证未配置、Worker 离线、密码或 TOTP 被拒绝、身份不匹配、上游拒绝和任务过期。关闭弹窗、切换账号或组件卸载后，迟到的轮询响应会被丢弃；服务端任务由 TTL 自动清理。

## 旧 Cookie 数据

`openai_session_cookies` 表和历史密文保留用于升级兼容，但运行时代码不再读取、写入、替换或删除这些数据。新的账号数据导出也不会包含它们。
