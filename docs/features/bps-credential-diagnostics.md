# Excel / BPS 凭证诊断

账号 API 现在返回安全的 `excel_bps_credential_state`，状态包括 `pending`、`not_expired`、`expired`、`revoked`、`auth_failed` 和 `unknown`，并可附带过期时间、错误码和手动恢复提示。Token、ciphertext、哈希和上游正文不会返回。

凭证状态展示复用在账号状态、用量列和编辑窗口。JWT 未过期只表示本地时间未到期，不代表上游认证成功。凭证恢复不会自动解除管理员停用或暂停。

Excel grant 的成功写入、刷新替换和失败清理都按当前 ciphertext 条件更新；迟到响应不会修改新凭证。诊断写入和 scheduler outbox 在同一事务中提交。无数据库迁移，状态复用现有 `accounts.extra`。

来源参考 Sub4API v1.1.5 的 `openai_bps_credential_state.go` 与 `account_repo_bps_credentials.go`；本 fork 保留独立 Excel grant 表和原有授权 worker，不引入 BPS 独立平台。
