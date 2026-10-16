# 区域本地鉴权运行接线（预览）

本功能是调度同步后的第二步，仍不是可上线的区域网关。默认关闭；配置区域鉴权后，入口先用本地快照校验 Key、用户、分组权限、订阅有效期及 IP ACL。有效请求随后固定返回 `503 regional gateway admission is not enabled`，不会进入既有主站鉴权、计费或模型 handler。无效凭证返回 401，IP 不允许返回 403，快照缺失/过期/不可读返回 503。`/health` 保持 200，只表示进程存活。不要因此开放 us.tosky.io 的生产流量。

## 配置

主站 `runtime.role=full`、区域节点 `runtime.role=gateway`，分别设置 `RUNTIME_REGIONAL_AUTH_FILE=/run/secrets/regional-auth.json`（或 YAML `runtime.regional_auth_file`）。此配置可与调度配置并存，**必须使用不同的监听地址、状态文件和加密密钥**。每进程每类只支持一个区域流。JSON 严格解析，身份材料和恢复要求与 [调度运行时](regional-scheduler-runtime.md) 相同。

```json
{
  "snapshot_kind": "auth",
  "mode": "publisher",
  "policy": {"region_id": "us", "max_snapshot_age_seconds": 60, "max_clock_skew_seconds": 5},
  "keys": {"us-auth-v1": "<base64 编码的独立 32 字节随机加密密钥>"},
  "key_id": "us-auth-v1",
  "auth_lookup_key": "<base64 编码的另一个 32 字节随机 HMAC 密钥>",
  "allowed_user_ids": [123],
  "bootstrap": false,
  "listen": "127.0.0.1:9444",
  "ca_file": "/run/secrets/us-auth-ca.pem",
  "cert_file": "/run/secrets/auth-identity.pem",
  "key_file": "/run/secrets/auth-identity-key.pem",
  "trusted_server_cert_sha256": "<订阅端填写：发布端叶证书 SHA-256 hex>",
  "trusted_client_cert_sha256": ["<允许拉取/查询状态的客户端叶证书 SHA-256 hex>"],
  "interval_seconds": 5,
  "lifetime_seconds": 60
}
```

订阅端另设 `mode=subscriber`、`endpoint=https://<主站内部域名>:9444/internal/regional/auth`、`state_file=/var/lib/sub2api/regional/us-auth.packet`。两端必须一致配置 region、用户分配、加密密钥及 HMAC 密钥。空用户分配只允许空快照，不代表所有用户。不能填写调度的 buckets/allowed_account_ids；鉴权文件必须显式声明 `snapshot_kind=auth`，旧调度文件不声明 kind 仍保持兼容。

`allowed_user_ids` 只是此阶段人工配置的鉴权同步范围，不是账号区域独占协议。同一用户的非删除有效 Key 全量导出；最多 4096 个 Key，明文文档最多 4 MiB，超过限额会使整次发布失败，不截断。批量同步扩容/分页协议留待后续设计。

HMAC 摘要代替明文 API Key，避免将可直接使用的 Key 传给区域节点；密码哈希和联系邮箱不进入文档。完整权限文档仍是敏感数据，只在 mTLS 内传输加密包，落盘也只保存密文。HMAC 密钥与包加密密钥各自独立、各区域独立，配置文件按 secret 管理。加密 key ID 轮换需保留恢复旧包的密钥。当前 HMAC 密钥不支持双密钥过渡，轮换应停用该区域入口并协调两端配置/重新同步；不同 HMAC 密钥会拒绝鉴权，不会回源。

## 一致性、撤销和恢复

迁移 `278_regional_auth_streams.sql` 保存 `(region, auth)` 的单调版本与包，独立于调度流。事务 advisory lock 确保每轮只有一个发布者，行锁及 REPEATABLE READ 避免旧视图分配新版本。Key 投影、用户及允许分组边、分组模型权限、用户分组禁用模型/RPM 覆盖、订阅起止时间，都绑定同一个事务的 Ent client/SQL executor。任意读取或校验失败都会回滚；不得将失败的权限读取当作“不限制”。

订阅者落盘完整包和版本后才切换视图。每次鉴权（含 `GetByKeyUncached` 的 WebSocket turn 复查）都检查当前持久快照及过期时间，不使用 L1/Redis，不请求主站、不触发 last-used 写入。缺失 Key 是权威否定；删除、停用和权限收回由下一份全量替换生效，旧包不能回放。每次返回独立对象，handler 修改不会污染后续鉴权。

这是**有界延迟撤销，不是实时全局撤销**：成功同步后立刻按新视图拒绝；断连期间旧视图在原始过期时间前仍有效，到期后拒绝。保守撤销窗口包括发布事务读取至完成的时间（当前单次任务限时 15 秒）、配置快照寿命及节点时钟误差；必须保持时钟同步。失败重试/重启不得延长已有快照的 TTL。Key 自身和订阅自身到期单独在每次鉴权时检查，不等下一次同步。

首次初始化才使用 `bootstrap=true`，成功后改回 false。文件锁、损坏/缺失状态拒绝启动、持久化失败关闭准入、恢复版本下限与调度流相同。多发布实例必须使用一致的用户分配及密钥配置；此版本不管理配置共识，调整分配要协调所有发布者和订阅者，不能让旧配置实例持续重新授权。

## 权限覆盖和限制

复用现有版本化 `APIKeyAuthSnapshot` 投影，包含用户状态/角色、允许分组、公开分组限制、Key/IP/并发配置、分组状态、模型 allowlist、用户禁用模型、推理强度、stream-only 等约束。订阅分组另外附有效订阅起止时间；未开始、到期、撤销或删除的订阅拒绝鉴权。这里的订阅只表达访问资格，不批准任何消费。Key 配额耗尽状态保守拒绝。

模型权限字段可被后续 handler 读取，但当前关闭模型 handler，因此本 PR 不声称已完成针对具体请求模型的全链路权限执行。投影内余额、用量和限额也不是可花费额度：主站额度预留/租约、区域预留/outbox/结算、并发/RPM 协调还未实现。账号凭证补全、分组调度读取、区域账号独占/迁移和主站依赖清单仍见调度文档。

预览入口只接受 Authorization Bearer、x-api-key、x-goog-api-key，不接受 URL Key（包括 Gemini 原生路由）；这不是对主站既有路由行为的修改。预览错误目前使用应用统一错误体，完整模型路由开放时还需验证各协议响应兼容性。

应用整体仍会初始化 PostgreSQL/Redis，也仍有其他服务后台访问。此 PR 证明本地鉴权方法和预览入口不走这些依赖，不证明应用能脱离主站独立启动，也不证明所有请求链路已无跨区访问。

## 监控

同一受认证的内部监听器提供 `GET /internal/regional/auth`（仅发布者）和 `GET /internal/regional/auth/status`（两端）。status 包括 ready、revision、expires_at、attempts、failures、last_success；不可用返回 503，不输出用户、Key 或权限文档。ready 只表示鉴权快照可用，不能作为开放模型流量的条件。应监控同步失败、版本停滞和临近到期。同步失败指数退避最多 5 分钟，期间到期会关闭鉴权。
