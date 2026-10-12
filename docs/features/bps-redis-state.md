# BPS Redis 会话状态

`gateway.excel_bps_state_redis_enabled` 默认关闭。开启后，稳定账号/API Key/session scope 的工具回放和目录可在实例间共享；Redis 故障返回 `basispoints_state_unavailable`，不使用不完整本地历史。状态按 2 小时闲置续期，单条最多 1 MiB，总计最多 1024 条和 16 MiB。

不会写入 Authorization、access token、代理密码或完整请求头。启用前确认 Redis 与所有网关实例使用同一安全命名空间。回滚时关闭开关，已有 Redis 状态按 TTL 清理。
