# 复用密码/TOTP Worker 登录 Excel/BPS

现有 toSub2 worker 的密码和 TOTP 步骤可以用于官方 Excel OAuth，但需要从 Excel 授权 URL 开始，使用同一条 PKCE/state 会话完成登录。先取得 ChatGPT Web 会话再切 Excel 授权，在本次真实验证中被要求重新登录。

本改动新增显式的隔离 CLI，复用 `process_password_claim` 和现有 toSub2 运行时。普通队列任务默认仍为 Codex；本改动没有增加管理 UI、修改数据库、让现有队列自动选择 Excel，或部署生产 worker。

## 为什么旧 OAuth 凭据不能直接转换

本 PR 不提供“仅凭原 Codex access token / refresh token，直接转换为 Excel/BPS Token”的功能。区别在于凭据所属的 OAuth 客户端与授权会话，不能按账号是否开启 2FA 来判断 Token 是否兼容。

- Codex 与 Excel 使用不同的 `client_id` 和注册授权流程。修改本地账号字段或 BPS 请求头，不会改变已签发 Token 的签名内容或服务端授权记录；直接改 JWT 内容会使原签名不再匹配。
- refresh token 用于续签其配套授权，不能自行改成另一个客户端的授权。本次已测试“旧 Codex RT + Excel client ID”，返回 HTTP 401 / `invalid_client`。尚未验证到官方允许的 Codex → Excel Token 交换方式。
- 同账号、同工作区、同代理配置下，原 Codex Token 调 BPS 返回 403，重新完成官方 Excel 授权所得的会话则能完整生成。该对照证明凭据来源影响本次准入，但没有单独隔离出 `client_id`、scope 或其它会话字段中哪一项是 BPS 的决定性检查，不能把 403 直接解释为账号封禁。

| 已有材料 | 本次支持的处理方式 |
| --- | --- |
| 只有原 Codex AT/RT，无法再次完成账号认证 | 不支持直接转换；不能仅改 client ID、scope、Cookie 或请求头 |
| 用户能完成官方 Excel 登录 | 重新进行 Excel OAuth，获取官方签发的新凭据；账号无需因这项授权而强制开启 2FA |
| 邮箱、密码及 TOTP 密钥 | 使用本 PR worker 自动完成 Excel 登录及 2FA 验证，再取得新凭据；这条路径已实测成功 |
| 已有有效的官方 Excel OAuth 会话 | 保留配套 AT/RT/ID Token、client ID、工作区和有效期，按 Excel 方式导入/刷新，无须重复转换 |

因此，“2FA 转换”在本 PR 中指**用密码/TOTP 自动重新登录并换取 Excel 授权**，不指用 TOTP 给旧 Token 重签名。没有开启 2FA 的账号仍可通过官方登录完成授权；本 PR 真实自动化验证覆盖的是密码 + TOTP 账号，不能将其推广为所有无 TOTP/邮箱验证码流程都已验证。

同一用户的 Codex 与 Excel 会话应分别保存，不混用新旧 RT 或客户端编号。成功重新登录只确认 BPS 可调用，仍不代表可以取得 Codex 门票或保证模型质量。

## 使用

先准备 root 或操作者所有、0600 的 JSON 输入文件，父目录 0700。字段示例中的值均为假值：

```json
{
  "account_id": 123,
  "login_email": "fixture@example.invalid",
  "password": "synthetic-password",
  "totp_secret": "SYNTHETIC_BASE32_SECRET",
  "expected_workspace": "expected-workspace-id",
  "proxy_url": "socks5://127.0.0.1:1080"
}
```

密码、TOTP 密钥、实际代理密码、工作区标识不得提交到 Git。`expected_workspace` 应取原账号，避免选到另一个组织；如没有该字段，只验证所得 Excel 会话的邮箱、客户端和内部工作区一致性。代理由输入显式指定，不能从本例推断业务出口。

```bash
TOSUB2_PYTHON=/opt/sub2api-reauth/venv/bin/python3 \
  /opt/sub2api-reauth/venv/bin/python3 tools/openai_excel_2fa_login.py \
  --input-file /root/private/excel-login.json \
  --tosub2-root /opt/sub2api-reauth/current/tosub2 \
  --output-dir /root/private/new-excel-session
```

必须使用新输出目录。成功产物：`credentials.json`、`account.json` 和 `result.json`。其中凭据文件敏感；`private-protocol-output.json` 即使登录失败也可能包含会话数据，只留在受限目录，禁止普通日志或整目录公开。此 CLI 不调用生产账号写回接口；返回 `production_account_updated=false`。若后续手动刷新了 RT，应使用刷新后的完整会话，不能混合新旧 Token。

toSub2 支持约定会在临时副本中校验；依赖布局变化时拒绝运行，原共享 runtime 不修改。适配使用官方 Excel client ID、固定 callback、`organization.read` scope、`/api/accounts/authorize` 和 `/oauth/token?unified=true`。不会通过修改原 Codex Token 或 client ID 伪造 Excel 会话。

登录复用 toSub2 密码/TOTP 方法，保留证书验证。Excel 分支禁用代理轮换和额外安全挑战的自动处理；遇到邮箱/电话验证码等交互要求退出，不将密码或验证码转交给第三方服务。该分支登录超时为 10 分钟；原 Codex worker 默认行为不改。

## 已验证范围（2026-10-08）

在一个用户明确授权的既有账号上进行了隔离验证，未更换生产队列 worker：

| 检查 | 结果 |
| --- | --- |
| 先 ChatGPT Web 登录再 Excel 授权 | 密码和 TOTP 通过，随后 Excel 授权返回登录页；未产生 Excel Token |
| 直接 Excel PKCE 内完成密码/TOTP | 成功取得完整 access/refresh/id Token，客户端、邮箱、工作区、有效期一致 |
| Excel 凭据调用 BPS 短请求 | HTTP 200、完整 `response.completed`、OK，约 21 秒 |
| Excel refresh token 刷新 | HTTP 200，RT 轮转，身份一致性校验通过 |
| BPS 回复是否带原生票据 | 仅 `__cf_bm` / `__cflb`，无 `x-codex-turn-state` / `__oailb`；响应模型声明为 Luna |

因此已确认 2FA → Excel OAuth → BPS 的可用性，但没有证明 BPS 返回原生 Codex 票据、不降智或半小时票据有效期。未测试其它账号/套餐、邮箱 OTP 分支、所有工作区选择或刷新后的新生成请求。用户另报的一组 7 次请求和 22,329 输入 token 不属于这里自行执行的请求统计，不能合并为已复核证据。

## 离线检查与打包

```bash
EXCEL_TEST_TOSUB2_ROOT=/path/to/pinned/tosub2 \
  python3 -B -m unittest tools.test_openai_excel_oauth_adapter \
  tools.test_openai_oauth_reauth_worker tools.test_openai_oauth_reauth_engines \
  tools.test_openai_oauth_reauth_runtime_controls -q
node --test tools/test_openai_excel_password_flow.mjs
```

Python 校验覆盖共享 runtime 不被修改、依赖布局失败、客户端/邮箱/工作区冲突、凭据保密以及原 worker 行为。Node mock 验证同 PKCE 流程、错误 state 拒绝和邮箱验证要求不被忽略。

Docker 与可迁移 runtime 包含新增适配文件；CI 对固定 toSub2 提交执行语法与 mock 检查。生产机只使用已有 Python/Node 运行时测试，未编译应用或在生产环境构建 runtime 包。只有在对应构建/CI 结果出现后才能宣称包验证通过。
