# 区域调度同步运行接线（预览）

本功能只同步调度候选，不构成可上线的区域网关。默认关闭；仅配置区域调度订阅模式时，应用的模型请求路由在 API Key 鉴权之前固定返回 503。同时启用 [区域本地鉴权](regional-auth-runtime.md) 后，会先进行本地鉴权，再保持模型准入关闭。`/health` 是进程存活检查，不代表区域请求就绪。不要将 us.tosky.io 生产流量导入该模式。本 PR 不部署任何服务。

## 配置和生命周期

通过 `RUNTIME_REGIONAL_SCHEDULER_FILE=/run/secrets/regional-scheduler.json`（或 YAML 的 `runtime.regional_scheduler_file`）指定严格解析的 JSON 配置。文件包含密钥，应挂载为只读 secret。未知字段、无效角色/周期、TLS 配置错误、损坏状态、丢失状态但未显式 bootstrap 均使启动失败。

发布进程要求 `runtime.role=full`；订阅进程要求 `runtime.role=gateway`。每进程管理一个区域调度流；此版本没有多区域流管理 UI。下面所有路径都是部署时需要提供的文件，密钥占位符不能直接使用。

```json
{
  "mode": "publisher",
  "policy": {"region_id": "us", "max_snapshot_age_seconds": 60, "max_clock_skew_seconds": 5},
  "keys": {"us-v1": "<base64 编码的 32 字节随机密钥>"},
  "key_id": "us-v1",
  "allowed_account_ids": [123],
  "buckets": [{"GroupID": 10, "Platform": "openai", "Mode": "single"}],
  "bootstrap": false,
  "listen": "127.0.0.1:9443",
  "ca_file": "/run/secrets/us-sync-ca.pem",
  "cert_file": "/run/secrets/sync-identity.pem",
  "key_file": "/run/secrets/sync-identity-key.pem",
  "trusted_server_cert_sha256": "<仅订阅端配置：发布服务器叶证书 SHA-256 hex>",
  "trusted_client_cert_sha256": ["<获准拉取或查询状态的客户端叶证书 SHA-256 hex>"],
  "interval_seconds": 5,
  "lifetime_seconds": 60
}
```

订阅端设置 `mode=subscriber`，另填 `endpoint=https://<主站内部域名>:9443/internal/regional/scheduler` 和 `state_file=/var/lib/sub2api/regional/us-scheduler.packet`。父目录须已存在且仅服务身份可写。订阅端的 `buckets` 不参与选择；完整替换文档中的缺失桶是权威空结果。两端的 region、分配账号和密钥必须一致。第一次创建流/本地文件时才显式设置 `bootstrap=true`；成功同步后改回 false。bootstrap 不是丢失版本历史后的修复方法。

内部监听器强制 TLS 1.3 和客户端证书验证，不信任转发头、不使用环境 HTTP 代理、不跟随重定向。客户端证书除链校验外还按叶证书 SHA-256 指纹精确匹配 allowlist，订阅端另对发布服务器证书做指纹校验；仅共享 CA 不会自动获准读取流。身份文件需包含 serverAuth/clientAuth 用途，服务证书 SAN 必须匹配 endpoint。每个区域使用独立加密密钥和 CA 信任域。密钥轮换时，保留恢复已落盘密文所需的旧 key ID；仅在新密钥完成发布和各订阅节点落盘后移除旧密钥。

后台任务启动即尝试同步，单次限时 15 秒，失败指数退避至 5 分钟。关闭时取消同步、关闭内部监听、等待 worker 退出并释放文件锁；本地候选读不调用同步任务。订阅模式不启动 SchedulerSnapshotService 的 Redis 重建/outbox worker。应用其他服务仍有数据库/Redis 初始化及后台访问，因此这也不是独立离线启动能力。

## 一致性、互斥发布和恢复

迁移 `277_regional_scheduler_streams.sql` 保存每个 region 的单调版本及加密全量包。每轮发布在一个 PostgreSQL REPEATABLE READ 事务内执行：

1. 获取按 region 划分的事务 advisory lock；其他实例跳过本轮。此处是每轮唯一发布者，不是固定主进程选举。
2. 锁定持久流记录，读取 revision。并发旧事务若遇到新提交会序列化失败，不能以旧数据库视图覆盖新版本。
3. 将账号、分组仓储的 Ent client 和原生 SQL 全部绑定到此事务，读取所有桶、账号关系、代理及分组。
4. 校验分配范围和完整文档，生成 revision+1 的加密包；版本与包原子提交后才对内部 HTTP 可见。

没有 Redis 租约，也不依赖进程本地 mutex 或发布节点本地文件分配版本。失败不会刷新旧包过期时间；提交结果不确定时，本轮不暴露新包，下轮从数据库高水位继续。实例启动从数据库恢复已提交包，过期包不服务。流记录丢失时，正常启动和后续发布失败；恢复数据库备份须保持所有接收者已接受的版本下限。

同一流的所有发布实例必须使用一致的分配和桶配置；本 PR 不提供分配版本共识或账号区域所有权协议。变更分配需暂停该流的所有发布实例并协调配置；不得用 allowlist 实现在线跨区域迁移。多发布节点可以短暂服务各自缓存的旧提交包，订阅者拒绝版本倒退，TTL 不因失败重试延长。

订阅者先持久化密文包和版本，再原子切换读视图。文件旁的操作系统锁排除共享同一文件的本机进程；应使用本地持久卷，每 Pod 独立文件，不支持 NFS/跨主机共享文件。进程崩溃会释放锁，不删除锁文件。重启恢复过期包的版本下限，但拒绝用它调度；损坏或持久化失败时关闭候选准入。

## 监控

在同一 mTLS 内部监听器提供：

- 发布端 `GET /internal/regional/scheduler`：可用时返回加密全量包，否则 503。
- 两端 `GET /internal/regional/scheduler/status`：返回模式、快照可用性、版本、过期时间、尝试/失败计数及最后成功时间；快照不可用返回 503。计数在进程重启后重置。此 ready 只表示调度快照新鲜，**不表示可承接模型流量**。

日志仅记录同步失败的角色和区域，不输出 endpoint、TLS 密钥、账号凭证或解密 payload。监控应针对持续失败、临近过期、过期/缺失快照和版本停滞告警。同步失败但旧快照尚新鲜时仍允许本地候选读取，到期后拒绝。

## 仍会跨区的路径和后续 PR

| 路径 | 当前依赖 | 后续范围 |
|---|---|---|
| `SchedulerSnapshotService.ListSchedulableAccounts` | 本 PR 订阅模式下仅本地完整快照 | 已接入，无回源 |
| `SchedulerSnapshotService.GetAccount`、网关 `hydrateSelectedAccount` | Redis 单账号缓存/主站账号仓储；OpenAI 调度还有直接 `accountRepo.GetByID` | 凭证补全、状态及账号本地读取 |
| `GetGroupByID/GetGroupByIDLite`、profit control、composite route resolver | 分组仓储/数据库 | 分组和策略本地读取 |
| API Key/User 权限、撤销、订阅校验 | 缓存/主站数据库 | 本地鉴权快照及过期拒绝 |
| OAuth token 刷新、账号排空、状态写回 | 主站服务及缓存 | 区域独占归属、迁移和凭证生命周期 |
| sticky session、并发/等待槽、限速等 | 当前 Redis/concurrency service | 区域内协调，禁止共用跨区请求缓存 |
| 余额和订阅额度、usage 写入 | 主站账务和 Redis | 主站额度租约、区域预留、持久 outbox、幂等结算 |

导出包含敏感完整账号数据，allowlist 只限制导出/接收范围，不证明区域独占。父子账号目前被文档校验拒绝。`simple` 运行模式与桶/分组语义也尚未形成完整区域协议。以上任一缺口都意味着不能开放生产模型请求；后续依次完成本地鉴权、区域归属、额度租约及区域账本，再做 operate 部署、灰度、真实延迟和账单验收。

## 验证范围

针对性单元测试覆盖运行时 mTLS、加密落盘、本地读取、启动恢复、损坏/缺失状态、文件独占、失败不续期和预览路由阻断。PostgreSQL 集成测试覆盖两个发布实例互斥、发布失败回滚、持久版本恢复，以及两次桶查询之间并发提交账号凭证/分组/成员变化仍保持同一事务视图。现有 regional 协议/传输测试覆盖过期、回滚/冲突、完整替换及损坏包拒绝。

测试不替代真实跨区断连、模型首 token 延迟和账单对账。尚无生产部署或生产请求验证。
