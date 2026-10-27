# 凭证运营异常识别

本补修复用凭证运营的 `TokenGuardV2View`、`AccountTokenGuardV2Service`、`OpenAIOAuthReauthService` 和现有 Worker 诊断协议。巡检继续存入 `probe_detail` / `last_probe_at`，重登继续存入任务 `error` / `finished_at`。API 新增可选 `probe_failure`、任务 `failure` 和 `refresh_failure`，只含白名单 `category/code/stage/action` 及原操作时间。

## 分类证据

| 输入 | 展示 | 后续处理 |
| --- | --- | --- |
| `phone_verification_required`，或实际登录协议的 `page.type=add_phone` | 需要手机号验证 | 人工在官方页面完成 |
| `account_deactivated/account_disabled/account_banned` | 账号已被停用 | 官方渠道确认 |
| `account_deleted` | 账号已删除 | 官方渠道确认 |
| `account_retired`，或完整白名单 `Your account has been deleted or deactivated.` | 账号已删除或停用 | 不推断两者区别 |
| `invalid_grant/token_revoked/refresh_token_reused` 等明确码 | 凭据失效，需重新登录 | 既有凭据恢复流程 |
| `invalid_password/password_rejected` | 密码错误或被拒绝 | 人工检查配置 |
| `mfa_required/otp_required/totp_rejected` | 需要 MFA / 验证码 | 人工完成官方验证 |
| `access_denied/security_check_required` 等 | 授权拒绝 / 安全验证 | 保留安全止发 |
| 限流、超时、网络、服务错误的明确码或对应传输状态 | 相应临时异常 | 既有预算/间隔 |
| 未识别、损坏、旧 Worker 泛化错误 | 未知异常 | 显示阶段与人工排查提示 |

只读取真正的顶层 `error` 对象或 OAuth `error` 字符串；不递归搜索输入、debug 或 quoted user text。机器码精确匹配，旧文本仅完整白名单匹配。拒绝重复字段、过大响应及非法枚举；未知非空 code 不回退 message。裸 401 沿用原鉴权重登策略，裸 403 仅归授权拒绝，不推断封号；明确账号退役/手机号/安全验证在巡检中不触发重登。保留原来的策略拒绝止发和所有预算、暂停、租约、CAS、ownership。

## 刷新历史与兼容

迁移 `309_credential_operation_refresh_failure.sql` 为原巡检表添加 `refresh_failure_code`、`last_refresh_failure_at`，旧行为空。只记录已纳管账号的实际 token 刷新失败尝试；后续重试可能恢复，页面明确标注此含义，成功巡检不会擦除历史或把账号当前状态冒充旧错误。历史写入不修改凭据、账号状态、调度、预算或租约，较早时间不能覆盖较新记录。保存历史失败不改变刷新返回值。原刷新策略依赖的安全机器码（包括 `invalid_grant` 竞争恢复）保留在错误链中。

旧 API / Worker 缺少新字段时仍显示安全 unknown；未记录的阶段/时间显示未知，不填当前时间。原始 provider body、未标记的 token/password/totp/cookie、手机号、邮箱及 URL query 不进入新字段。新增枚举出口先 allowlist 再序列化，不依赖不完整的正则脱敏。

升级需先应用新增列迁移；旧二进制可忽略新列。回滚二进制保留历史列，无需删除数据。不改变配置开关，不涉及支付账本或发布部署。

## Worker 所有权与验证边界

运行时固定引用 [poxiao33/toSub2](https://github.com/poxiao33/toSub2/tree/8548397e89bf80e508eda64a87e0d556d43abc84) 的 `src/protocol-login.mjs`（blob `ff2db2507e401c29fa78705e44f5b40a3108a8c6`）。扩展本仓库 `openai_codex_oauth_adapter.py` 及 `openai_codex_diagnostics.mjs`，继续只修改逐任务私有副本，不修改外部仓库。使用实际响应 body / page.type 及已知阶段；不依赖原始 stdout，不绕过挑战或轮换出口。

Session Studio 服务端不在本仓库，源码归属未确认。复用已有 terminal result 的 `error` 信号，不假定服务端新增字段；只收到 `provider_failed` 时保持未知原因。旧密码 Worker 无详细信号时也保留未知，不从笼统错误伪造诊断。

`tools/fixtures/credential-operation-errors.json` 使用虚构内容和现有协议形状，无真实账号、邮箱或 IP；它们是离线行为样例，不能作为现场根因。每类有结构化正例及否定/引用反例。精确 head CI 把离线 Worker 的实际失败报告写入 PostgreSQL，经原服务和 Gin handler 输出，再交给现有页面 Vitest；原始日志及中间 JSON 保留为 CI artifact。本机只跑离线 Python/Node、定向 Vitest、gofmt 与静态检查，Go/存储接缝及打包在远端 CI 运行。
