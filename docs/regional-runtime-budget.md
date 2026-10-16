# 区域共享并发运行时（默认不开放模型请求）

配置入口为 runtime.regional_concurrency_file / RUNTIME_REGIONAL_CONCURRENCY_FILE，默认为空。配置文件为严格 JSON，字段采用 Go 导出名。primary 使用主站 PostgreSQL，regional 使用区域 PostgreSQL；同一区域所有 Pod 共用数据库。辅助 Redis 不拥有 SQL 业务槽位。

- Mode: primary / regional；Instance、Epoch 必填。
- primary: PrimaryInstances 为该部署 epoch 的完整主站 Pod 名单；LegacyRedisUser 为迁移前 Redis 准入身份。可先不设 Listen，仅切换主站 SQL 准入。
- regional: Region、Endpoint、ServerPin、CAFile、CertFile、KeyFile、Targets、LifetimeSeconds 必填；Targets 是 Resource(kind/resource_id) 与 Capacity 的列表。寿命 30–3600 秒。必须 role=gateway 且设置本地 auth/scheduler 文件。
- primary 的 Listen 启用独立 mTLS 监听；Scope 固定 region、客户端证书 SHA256 pin、账号/用户/Key 范围和最大 grant/lifetime。此阶段仅注册 slots 操作；quota 路径保持 503。

FIFO 的队列记录、head 判断、槽位申请、取消在同一 SQL 事务。取消保留永久身份；Live 转移锁定精确源成员并按 kind/id 稳定顺序锁预算，不先释放再申请。Live 账号迁移保留联合用户/Key 份额。SQL 槽位不会被 Redis TTL、启动清理或断连自动释放。

broker 先提交原始 payload 再发送，响应丢失或重启重放原 ID。主站过期且尚未发出的 issue 保存永久拒绝记录，HTTP 410 是可核实的未分配回执；只有收到该回执才允许后续新 ID。已分配但响应丢失的 issue 仍返回成功。未确认或过期续约不自动换租约。关闭沿用 drain→本地零占用回执→authority close。

当前 guard 检查完整预期成员 heartbeat、旧 Redis 身份 off、无旧身份连接、旧并发 namespace 排空。**这仍不足以证明因 TTL 丢失的旧请求已经停止，完整部署 inventory 与旧进程终止证明仍需接入。不得仅凭此 guard 开放生产 grant/流量。** 未使用账号区域独占，账号保持共享池。

已验证数据库取消先于迟到申请、跨 grant 同 ID 并发、事务回滚、FIFO、Live 精确转移/账号迁移/abort、broker 确认丢失与过期未发回执、旧凭据/成员/槽位 guard。完整真实应用多 Pod 端到端、可靠部署切换证明、资金与账号状态协调尚未完成，模型入口 503 保持。

迁移 281 只增表；已有永久身份不得删除。关闭入口、停止新接纳、保留已开始请求、结算/outbox 排空、显式关闭 grant 后才回滚。只回退二进制不能把已租出的槽位交回旧 Redis acquisition。
