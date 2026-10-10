# 2FA 账号同时使用 Codex 和 BPS

已有密码/TOTP 登录账号继续保留 Codex OAuth。启用账号的 Excel/BPS 开关后，凭证运营后台每分钟检查缺少 Excel 授权的账号，复用已保存且加密的登录邮箱、密码、TOTP 和登录代理，为该账号单独完成官方 Excel OAuth。无需把原 Codex refresh token 转换成 Excel token，也不会替换原账号凭据。

## 使用条件

- 管理员全局 Excel/BPS 开关及账号的 BPS 开关均已开启。
- 账号是付费 ChatGPT OAuth 母账号；PAT、Agent Identity、Free 和影子账号不自动授权。
- 凭证运营已保存该账号的密码/TOTP 登录资料，凭据加密和本地 Worker 可用。TOTP 对未启用 2FA 的账号可为空；已启用 2FA 的账号须保存长期 TOTP 密钥。
- Excel 任务固定由本地密码/TOTP Worker 执行。Codex 任务沿用账号/全局引擎选择，不更改已有 Session Studio 配置。

第一次启用时需等待队列完成。授权前 BPS 请求返回 `basispoints_auth_pending`，可以在凭证运营查看任务及错误。没有保存登录资料的账号仍需先配置凭证运营或手动取得官方 Excel OAuth，普通 Codex refresh token 不能直接充当 Excel refresh token。

每次登录只处理一个 OAuth profile。同账号的 Codex 登录、Excel 登录和 2FA 更换任务互斥；已有任务完成后再排队。Excel 失败重试至少间隔 30 分钟，每次沿用已配置代理，不自动轮换出口。

## 两套凭据与请求路由

- Codex 使用原 `accounts.credentials` 及现有刷新流程。
- Excel 使用独立的 `openai_excel_oauth_credentials` 表，凭据整体以现有 AES-256-GCM 凭据加密器加密；普通账号 API 和导出不返回这份数据。
- BPS Responses、图片、附件和恢复探针统一使用 Excel grant；未选中 BPS 的模型与普通 Codex 请求继续使用原授权。
- Excel 授权会校验官方 client、Token 的邮箱、用户及工作区。Excel ID token 的 `sid` 是会话标识，工作区取 access token 的 `chatgpt_account_id`。
- Excel access token 到期前独立刷新，使用 Excel client ID。并发刷新加进程锁、Redis 锁和密文比较写入，不覆盖 Codex token。
- BPS 401 只移除实际使用且仍匹配的 Excel grant，重新进入有冷却的授权队列；迟到的 401 不删除更新后的 grant，不停用 Codex 账号。原有 BPS 403 和 429 策略继续生效。

迁移 `271_openai_excel_dual_oauth.sql` 为已有重登任务增加 `oauth_profile`，旧任务默认 `codex`，并创建独立 Excel 凭据表。不批量迁移或改写现有 Codex 授权。历史手动导入的 Excel 单授权账号保留原来的主凭据与刷新路径；它们不会自动获得 Codex 授权。

Excel Auth / Excel OAuth 创建的单授权账号（主凭据 `client_id` 为官方 Excel 客户端）只能使用 BPS。全局或账号 BPS 关闭、请求模型未选入 BPS 时，这类账号不参与对应请求的调度；直接转发或手工测试会返回本地通道不可用错误，不向 Codex 发送 Excel 凭据，也不因此停用账号。其模型目录只查询 BPS；原生 WebSocket、图片回落及其他 Codex 入口会被拒绝。普通 Codex 账号搭配独立 Excel grant 的混合路由保持原有行为。

已经因错误通道的 401 被停用的账号不会自动恢复。管理员需要核实凭据、BPS 开关及模型范围后恢复状态。将模型加入 BPS 列表不会授予上游模型权限；BPS 返回的真实认证失败仍按现有策略处理。

## 手动授权 API

原账号重登入口 `POST /api/v1/admin/accounts/:id/openai-reauth` 默认仍创建 Codex 任务。请求体可传 `{"oauth_profile":"excel"}` 单独授权或重登 Excel。任务响应新增 `oauth_profile`，仅包含授权类型，不包含密码或 token。后台自动排队不需要调用这个 API。

## 验证与上线

离线回归覆盖双授权保留、身份/profile 拒绝、并发 Excel 刷新、保存失败、BPS 401 隔离和自动排队。PostgreSQL 集成验证同账号任务互斥、Local/Session Studio 分流、最终写回故障回滚、密文 CAS 和失败冷却。测试不调用真实 OpenAI 登录或模型接口。

升级 API 时须使用包含 `openai_excel_oauth_adapter.py`、`openai_excel_password_flow.mjs` 的 Worker 运行包。旧 Worker 即使返回了 Codex grant，也会因 profile 不匹配被拒绝写入，不能据此认为支持 Excel。备份须包含数据库及凭据加密 key；只回退二进制不会移除新增表或已取得的 Excel grant。
