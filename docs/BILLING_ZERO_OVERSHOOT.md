# 后付费零超发护栏：运维手册

本文覆盖"余额模式后付费计费"的五层准入/结算护栏，说明**每个配置项的默认值、如何判断
护栏是否真的在跑、以及升级时的注意事项**。适用代码：`backend/internal/service/billing_cache_service.go`、
`backend/internal/service/gateway_request_spend_estimate.go`、`backend/internal/repository/billing_cache.go`、
`backend/internal/repository/billing_reservation_db.go`、`backend/internal/repository/usage_billing_repo.go`；
DB 兜底迁移为 `272_billing_balance_reservations.sql` / `273_billing_scope_reservations.sql` /
`274_billing_reservation_fallback_state.sql`。

## 1. 问题背景

网关是**后付费**：请求先转发到上游（成本已经发生），结算时才从钱包扣钱。因此任何
"先放行、结算扣不动"的请求都直接变成坏账。历史上出现过三类表现：

1. 余额被扣到封底线后请求**全部继续成功返回**，`usage_log.actual_cost = 0`，日志只记
   `INSUFFICIENT_BALANCE` 但不阻断（"白嫖"）。
2. 余额贴近封底线时，**最后一笔付不满的请求**先被转发，结算只能扣到封底线，
   差额写成坏账（线上实证 `usage_log` 应收 0.19184 / 实收 0.099472）。
3. 并发突发时 N 个请求读到**同一份余额快照**，每个都判定"够付自己这一笔"而全部放行，
   结算时只有前几笔扣得动（坏账）——即使刚刚用 DB 真值复核过。
4. **user × platform 日/周/月配额**与 **API Key 配额**都只有准入判定、用量在结算时才
   累加：同一用户在平台配额上的并发突发会共用同一份用量快照，配额被超额消耗
   （第 3 类的"额度换成平台配额"版本，用户钱包并不因此少收，但配额上限失效）。
   两类额度都已按第 ③ 层修复（scope=用户×平台 / scope=apikey:<id>，见下一节）。

另外还有一个可用性问题：余额贴近封底时每笔请求都要 DB 复核，150–200 并发会把
PostgreSQL 连接槽打满，复核失败后 fail-closed 成大范围 **503**。

## 2. 五层护栏（按请求生命周期）

| 层 | 位置 | 作用 | 关键代码 |
| --- | --- | --- | --- |
| ① 准入阈值 + DB 真值复核带 | 转发前 | `balance <= reserve` 直接 403；`balance <= reserve + band` 时用 DB 真值复核；结算判定耗尽后写"钱包已耗尽"标记，预检命中即 403 | `checkBalanceEligibility` |
| ② 最坏费用闸门 | 转发前 | 要求 `balance >= reserve + 本次最坏费用`，付不满的请求**不转发**，不产生上游成本 | `estimateRequestSpendUpperBound` |
| ③ 在途预留（并发原子） | 转发前 | Redis 原子累加"已放行未结算"的最坏费用，使准入成为跨请求的原子操作，四类额度各自一个 scope：钱包（scope=用户，`balance - Σ预留 >= reserve`）、订阅（scope=用户×分组，`usage + Σ预留 <= limit`）、user×platform 配额（scope=用户×平台，任一窗口 `usage + Σ预留 <= limit`）、API Key 总额度（scope=`apikey:<id>`，两种计费模式都生效，`quota_used + Σ预留 <= quota`）。一次请求可同时持有其中多条凭据；任一 scope 预留失败时整槽回滚 | `reserveSpendWithGuard` / `reserveSubscriptionSpend` / `reserveUserPlatformQuotaSpend` / `reserveAPIKeyQuotaSpend` |
| ④ 结算封底 + 差额记账（默认） | 结算 | `FOR UPDATE` 锁行后 `GREATEST(balance - amount, floor)`，差额记 write-off 并打耗尽标记；**永不为负**。`billing.settlement_debt_mode=true` 时改为全额入账、余额可为负（欠款，充值抵扣） | `deductBalanceToFloorSQL` / `deductBalanceToDebtSQL` |
| ⑤ Redis 故障回落 | 转发前 | Redis 写失败时抬起跨实例共享的 DB 兜底窗口，并按 scope 切换账本：钱包用 `billing_balance_reservations` + `users FOR UPDATE`，订阅 / user×platform / API Key 用 `billing_scope_reservations` + advisory transaction lock。窗口生效期间所有实例统一走 DB，避免 Redis 与 DB 两本账同时放行。**Redis 与 DB 都失败时 fail-closed（503）**；只有未装配 DB 能力的降级/测试装配才保留 fail-open | `TryReserveUserBalance` / `dbReservationStore` / `reservationFallbackWindowActive` |

核心不变量：`balance − Σ在途预留 ≥ reserve`，且它在"结算扣钱"与"归还预留"以任意
先后顺序发生时都成立。

**Redis 故障时的 DB 兜底**：第 ③ 层的预留正常记在 Redis（带 TTL 自愈）。Redis 侧预留
操作失败时不再直接放行，而是按 scope 改用 DB 账本做同样语义的原子准入：

- **钱包 scope（纯 userID）**：`billing_balance_reservations` 事务先
  `SELECT ... FROM users WHERE id = $1 FOR UPDATE` 锁用户行（与结算路径锁序一致），
  清理已过期预留行，再判定"未过期总额 + 本笔 ≤ 预算"。
- **订阅、user×platform、API Key scope**：`billing_scope_reservations` 事务使用
  `pg_advisory_xact_lock(hashtextextended(scope, 0))` 按 scope 串行化，语义与 Redis Lua
  一致（`sub:<user>:<group>`、`upq:<user>:<platform>`、`apikey:<id>`）。

跨实例一致性由 `billing_reservation_fallback_state` 单行窗口保证：任一实例 Redis 故障时把
`fallback_until` 抬到 `max(现值, now + 预留 TTL)`，其它实例最多在 1s 探测缓存后统一切到
DB，绝不在窗口内混用两本账。窗口长度覆盖切换前已存在的 Redis 预留在 TTL 内全部过期。

触发条件只认**真实的 Redis 后端故障**：请求自身 ctx 被取消/超时（客户端断开）、归还时
凭据已过期、续期时凭据不存在都**不会**切换账本（否则任何一个断开的客户端都会把整个集群
切到 DB 串行准入）。窗口**不随** DB 预留/续期顺延 —— 只由最后一次真实 Redis 故障抬升，
因此 Redis 恢复后最多一个 TTL 即回到 Redis 账本；窗口结束时实例会把 DB 账本上仍存活的
预留导回 Redis（按 requestID 幂等），与进入窗口时的导入对称。同一实例上并发触发的切换
经 singleflight 合并为一次导入；窗口探测失败也按 1s 缓存，DB 变慢时不会每个请求都再探测。
DB 兜底也失败时返回 `ErrReservationBackendsUnavailable`，服务层转成 HTTP 503（fail-closed）；
仅未装配 DB 兜底的降级/测试装配才继续 fail-open。

代价是故障期每个 scope 的准入被事务锁串行化 —— 优先保"不产生坏账"而不是吞吐。

**订阅模式**（订阅计费）复用第 ③ 层：scope 为 `用户 × 分组`，护栏是
`usage + Σ预留 < daily/weekly/monthly limit`，堵住"并发请求共用同一份用量快照、
配额被超额消耗"的超卖。

**user × platform 日/周/月配额**（余额模式）同样复用第 ③ 层：scope 为
`upq:<userID>:<platform>`，三个窗口分别校验 `usage + Σ预留 <= limit`，取最紧的
窗口作为 Redis 侧的 `maxTotal`。它与余额 scope 是两套独立额度，同一次请求会在槽位上
持有**两条凭据**（`BillingReservationSlot` 多绑定），归还/续期以凭据为单位；平台预留
失败时余额绑定会整槽回滚，不会白占额度到 TTL 到期。

**API Key 总额度**（`api_key.quota`，两种计费模式都生效）同样复用第 ③ 层：scope 为
`apikey:<id>`，护栏是 `quota_used + Σ预留 <= quota`。它参考 new-api 的 token 额度预留
（`model.TryReserveTokenQuota` 同时动 `RemainQuota`/`UsedQuota`）：准入只读鉴权缓存里的
`quota_used` 快照、而 `quota_used` 在结算时才递增，所以并发突发会让同一个 key 一起
穿过 `quota_used >= quota` 判定。预留后该 key 的额度上限在并发下同样成立；不限量
（`quota <= 0`）的 key 不产生预留。

> **已结算用量账本（补上"快照冻结"缺口）**：预留只覆盖"已放行、未结算"的请求。
> 鉴权缓存里的 `quota_used` 是快照（L1 15s / L2 300s），而结算只递增 DB：等一批请求
> 结算完并归还完预留，聚合预留归零而快照仍是旧值，于是同一个 key 在一个快照周期内可以
> 把剩余额度**再放行一遍**（≈ 一个完整额度上限的超发）。
> 修复：统一计费路径在事务提交后用 `RETURNING quota_used` 的真值发布到 Redis 高水位账本
> `billing:apikey_quota_used:<keyID>`（Lua 保证只增不减），准入取
> `max(鉴权快照, 账本)`。账本值恒 ≤ DB 真值，因此只会让准入更保守。
> 配套：管理员重置 `quota_used` / 调整 `quota` 时清除账本（否则残留高水位会误拦）。
> 监控：`api_key_quota_ledger_read_error` / `api_key_quota_ledger_write_error`（必须为 0）、
> `api_key_quota_ledger_stale_rescue`（非零 = 账本正在兜住快照冻结）。

> **订阅用量的同步可见性**：订阅缓存里的 `usage` 同样被预检当作准入依据
> （`usage + Σ预留 < limit`）。结算后的累加若走异步队列，就会出现"预留已归还、usage
> 仍是旧值"，下一批请求重新拿到一整份剩余额度。修复：结算路径改为**同步**累加
> （`SyncSubscriptionUsageAfterSettlement`），失败或缓存键缺失时失效订阅缓存，
> 让下一次预检回源 DB 真值；API Key 限流窗口用量同理。回源写回另加"变动代号"
> 条件写回（`billing:sub_gen:<uid>:<gid>`），防止迟到的旧快照把结算后的 usage 覆盖回去。
> 监控：`subscription_usage_sync_error`、`api_key_rate_limit_sync_error`（必须为 0）。

> **切账本时的存量预留搬运**：Redis 故障切到 DB 兜底账本前，先把 Redis 上仍存活的
> 预留凭据（`billing:resv_item:*`，含 PTTL）导入 `billing_balance_reservations` /
> `billing_scope_reservations`，再抬升共享窗口。否则 DB 账本对已放行的请求一无所知，
> 同一份额度会被两本账各算一遍（少算 = 重复放行）。搬运失败不阻塞窗口抬升（可用性优先），
> 以 `reservation_fallback_import_error` 暴露残余风险。
> 监控：`reservation_fallback_imported` / `reservation_fallback_import_error`。

> **user × platform 配额的静默丢增量**：累加脚本在缓存键不存在 / entry 为旧 schema 时
> 静默返回 0（此前调用方只看 error，永远看不到"这次累加被跳过"）。修复：暴露脚本返回值，
> 被跳过或 Redis 写失败时删除缓存并**同步写 DB**（`IncrementUsageWithReset`），
> 让这笔消费成为新真值来源 —— 否则在 flusher 模式下该增量永久丢失、usage 永久偏小。

## 3. 配置项清单

全部位于 `billing:` 段。

| 配置 | 默认 | 说明 |
| --- | --- | --- |
| `minimum_balance_reserve` | `0.1` | 钱包封底线（不可花）。预检 `balance <= 本值` 即 403。设 0 表示不保留。 |
| `settlement_debt_mode` | `false` | 结算“收不满”的口径。`false`=封底核销（余额永不为负，差额记 write-off，不再追偿）；`true`=债务模式（全额入账、余额可扣成负数，欠款由后续充值抵扣，对齐 new-api）。两种模式都防白嫖（预检 `balance <= reserve` 即 403），差别只在追偿 vs 核销。**开启前请确认充值/催收/对账流程能识别负余额用户**（负余额是新的取值域）。 |
| `balance_recheck_band` | `1.0` | DB 真值复核带宽（USD）。余额 `<= reserve + band`（且至少 `2*reserve`）时回源核对。**带宽越大越安全，但贴底用户的每笔请求都会多一次 DB 往返**。 |
| `request_spend_precheck_disabled` | `false` | 反向命名，零值安全。`true` 关闭第 ② 层（不建议）。 |
| `request_spend_default_max_output_tokens` | `8192` | 请求未声明输出上限时的缺省上界。 |
| `request_spend_safety_multiplier` | `1.0` | 预检安全系数。`>1` 更保守（多拦、零坏账），`<1` 更激进。 |
| `request_spend_min_output_tokens` | `0` | **输出上界下限（默认关）**。部分上游桥不执行请求声明的 `max_tokens`（实测声明 64/190 仍产出 999 token），信任小声明值会让预检低估。`>0` 时按 `max(声明值, 本值)` 预检。**只抬高偏小的声明值，对大声明值逐位不变**。 |
| `request_spend_cjk_tokens_per_rune` | `0`（= 内置 `1`） | CJK 每字 token 上界，**校验范围 0–8**（超出即启动失败；运行期再钳到 8，防止误配 1e6 让 CJK 请求全量 403、或极大值让乘法溢出把闸门变成 fail-open）。内置 `1` 是按生产上游（deepseek）实测 0.5–1 token/字 校准的**已校准上游**上界，对**字节回退型分词器不是上界**（cl100k/o200k 生僻汉字、Llama 系 CJK 词表可达 2–3 token/字）。路由到这类模型（GPT 系 / Llama 系）的部署应设为 `2`（或 `3`）。 |
| `inflight_reservation_budget_multiplier` | `1.0` | 在途预留聚合闸门的预算倍数：`1.0` = 严格（`balance − Σ预留 ≥ reserve`，并发下零坏账，但并发量被"可花余额 / 单笔最坏费用"卡住）；调高即用**有界坏账**换并发（`settlement_shortfall_count` 会随之上长）。**可被后台 `/admin/settings` 的同名项覆盖，后台值优先，保存后立即生效**。 |
| `database.max_open_conns` | `32` | 必须**显著低于** PG `max_connections`（默认 100）；多实例按实例数均摊。 |
| `database.max_idle_conns` | `8` | 建议为 `max_open_conns` 的 25%–50%。 |

### 关于 `request_spend_min_output_tokens`

默认 `0` = **信任请求声明的值**（与历史行为一致，升级零行为变化）。

- **作用范围（先读这个）**：本下限只"抬高偏小的声明值"。声明 `max_tokens: 64` 且下限为
  8192 时按 8192 估；声明 `max_tokens: 64000` 时**逐位不变**、完全不受影响。因此它
  **不会**让"大 max_tokens 请求更早被 403" —— 那来自最坏费用闸门按声明上限计价，
  属于零超发的既有取舍，与本下限无关。
- **何时打开**：观察 ops 端点的 `settlement_shortfall_count`，斜率 > 0（仍在产生
  write-off，典型是 responses 桥不执行 max_tokens）时把它配成 `8192`（= 未声明时的
  缺省上界）；恒为 0 则保持 0 即可。
- 打开后影响范围有限：只有**余额贴近封底**的用户才可能因此被提前 403；余额充足的
  用户完全不受影响（`balance` 远大于最坏费用）。

## 4. 如何验证护栏在跑

### 4.1 护栏健康端点

```
GET /api/v1/admin/ops/billing-guard
```

只读进程内原子计数，不查库、不依赖 ops 监控开关，因此故障期可直接 curl。返回：

```json
{
  "stats": { "...": "见下表" },
  "runtime": { "reservation_ttl_seconds": 600, "reject_reasons": ["..."] }
}
```

### 4.2 指标判读（按优先级）

**第一优先：护栏失效类。这些只要非零，就说明护栏在某些条件下"根本没拦"，而不是"拦得多"。**

| 字段 | 非零意味着 | 处置 |
| --- | --- | --- |
| `settlement_shortfall_count` | 仍在产生 write-off，预检口径有漏网 | 检查 `request_spend_min_output_tokens` / `default_max_output_tokens` 是否偏小 |
| `reservation_fail_open` | Redis 预留失败且**未装配 DB 兜底能力** → 并发护栏静默失效（仅轻量/降级装配才会出现） | 检查 Redis 与生产装配 |
| `reservation_redis_failure` | Redis 预留失败，已切换到 DB 兜底账本（护栏仍生效） | 检查 Redis 可用性；持续增长说明故障未恢复 |
| `reservation_fail_closed` | Redis 与 DB 兜底同时不可用 → 预检 fail-closed，用户看到 503 | 同时检查 Redis 与 PostgreSQL；这是最严重的降级信号 |
| `reservation_db_fallback` | DB 兜底账本已接管本笔准入（**护栏仍成立**，scope 准入被事务锁串行化） | 检查 Redis 可用性；吞吐下降是该降级模式的预期代价 |
| `reservation_db_fallback_error` | DB 兜底操作失败；正式装配会 fail-closed（503），无 DB 能力的降级装配才 fail-open | 同时检查 Redis 与 PostgreSQL 连接 |
| `reservation_fallback_activated` | 本实例抬起共享 DB 兜底窗口 | 与 `reservation_redis_failure` 对照；持续增长说明 Redis 长期不可用 |
| `reservation_fallback_activate_error` / `reservation_fallback_probe_error` | 兜底窗口抬升/探测失败 → 跨实例一致性变弱 | 检查数据库连接与迁移 `240` |
| `reservation_fallback_imported` | 切 DB 账本时把 Redis 上仍存活的在途预留**搬进**了 DB（非零即"两本账语义已对齐"的实证） | 正常现象；数值大说明切换时有大量在途请求 |
| `reservation_fallback_import_error` | 存量预留搬运失败 → DB 账本缺少切换前已放行的预留，护栏会在故障期高估可用额度（最坏重复放行一个预算） | 检查 Redis 读（SCAN/Pipeline）与 PostgreSQL 写 |
| `reservation_release_error` | 预留归还失败 → 额度滞留到 TTL | 检查 Redis 写入与网络 |
| `reservation_renew_error` | 续期失败 → 超长请求可能失去保护 | 同上 |
| `reservation_abandoned` | 结算任务被 drop 语义丢弃 → 该笔未扣费 | 检查 usage_record worker 池容量与 drop 配置 |
| `subscription_usage_sync_error` | 结算后订阅用量**同步**落缓存失败（或缓存键缺失导致累加被跳过）→ 该笔只能靠缓存失效 + DB 回源补齐 | 检查 Redis 写入；持续增长说明预检会在缓存 TTL 内看到偏小的订阅用量 |
| `api_key_rate_limit_sync_error` | 结算后 API Key 限流用量同步落缓存失败 → 限流窗口可能短暂按偏小用量放行 | 检查 Redis 写入 |
| `api_key_quota_ledger_read_error` / `api_key_quota_ledger_write_error` | API Key 已用额度共享账本读/写失败 → 该 key 的额度校验可能退回过期快照 | 检查 Redis 可用性 |
| `api_key_quota_ledger_stale_rescue` | 账本高水位**领先**鉴权快照（护栏正在拦下"快照冻结后的重复放行"） | 非零是正常的，是修复生效的直接证据；恒为 0 说明鉴权缓存很短命或账本未生效 |
| `recheck_skipped_no_user_repo` | 无法读 DB 真值（降级装配）→ 坏账窗口回到修复前 | 检查依赖注入装配 |
| `recheck_fail_closed` | 复核失败 fail-closed → 用户看到 503 | 检查 PG 连接池与 `max_open_conns` |
| `precheck_disabled` | 第 ② 层被配置关闭 | 确认是否为有意为之 |
| `precheck_unavailable` | 部分请求无法给出最坏费用上界（无定价/依赖缺失） | 检查模型定价配置 |

`stats.healthy == false` 且 `degraded_signals` 非空即为上述任一情形的汇总。

**第二优先：误伤程度。** 这些是**正常业务拒绝**，斜率上升不一定有问题，但要区分原因：

| 字段 | 含义 |
| --- | --- |
| `reject_below_reserve` / `reject_marker_active` / `reject_db_truth_below_reserve` | 用户**真的没钱**，正常拒绝 |
| `reject_worst_case_db_truth` / `reject_worst_case_cache_only` | 用户还有余额，但付不满"这一笔"的最坏费用 |
| `reject_inflight_reservation` | 已被同一用户的在途请求占满额度 |
| `worst_case_reject_ratio` | 上述"护栏类"拒绝占全部预检拒绝的比例。持续偏高 = 预检过保守（可能误伤），可下调 `request_spend_safety_multiplier` 或调低 reserve |

**第三优先：在途预留堆积。** `reservation_reserved` 与 `reservation_released` 的差值
趋势反映当前在途量；`reservation_expired_release` 增长说明有请求在预留 TTL 内未完成
结算（结算任务丢失，或心跳未生效）。

### 4.3 服务器实测口径（复测建议）

1. 把某测试用户余额打到贴近封底（如 0.2），用小额请求消耗到 0.1 附近。
2. 小额请求（最坏费用 < 可花余额）→ 应放行，`usage_log.actual_cost` 精确。
3. 大 `max_tokens` 请求（最坏费用 > 可花余额）→ 应**转发前** 403，`reject_worst_case_*` +1。
4. 150 并发打贴底用户 → 预期 `403 insufficient balance`，**0 个 503**；同时
   `settlement_shortfall_count` 不应增长。
5. 充值 +0.1 后 → 应立即恢复放行（耗尽标记在加款路径被清除）。

## 5. 升级注意事项

1. **`database.max_open_conns` 默认值从 256 降到 32**。若你的部署高吞吐、且未显式配置
   该值，升级后吞吐可能下降，需要显式调大并**同步调大 PG `max_connections`**；
   多实例部署按实例数均摊（例：3 实例建议 ≤ 30）。
2. **`request_spend_min_output_tokens` 保持默认 0**（不改变现有部署行为，见第 3 节）。
   它是按 write-off 指标决定是否打开的"修补开关"，不是本次升级的强制变更。
3. 新增 `billing.balance_recheck_band`（默认 1.0）。它使余额 ≤ 1.1 的用户每笔请求都
   回源查 DB；同一用户的并发复核已由 singleflight 合并为一次。若你的流量中"贴底小额
   高频"占比很高，可适当调小该带宽。
4. **`minimum_balance_reserve` 默认值从 `0.000001` 变为 `0.1`**：钱包保留金从"名义上
   保留"变成"真实不可花 $0.10"，余额 ≤ 0.1 的用户会在转发前 403。配置文件里显式写了
   旧默认值 `0.000001` 的部署会被自动迁移为 `0.1`（`config.go` 的 legacy 迁移，仅匹配
   该精确值并打 WARN 日志）；想保持可花完余额请显式配 `0`。
5. 配置里的 `billing.minimum_balance_reserve` / `balance_recheck_band` /
   `inflight_reservation_budget_multiplier` / `request_spend_safety_multiplier`
   现在会拒绝 `NaN` / `±Inf`（`NaN` 曾能绕过所有 `< 0` 判断并让封底、最坏费用闸门与
   DB 复核静默失效，`+Inf` 会让预留闸门永久放开）。
6. 配置变更后请用第 4.1 的端点确认 `runtime` 段与实际配置一致。
7. **本次升级新增三张表**：`billing_balance_reservations`（迁移 `238_*`，钱包 scope）、
   `billing_scope_reservations`（迁移 `239_*`，订阅 / user×platform / API Key scope）与
   `billing_reservation_fallback_state`（迁移 `240_*`，跨实例共享兜底窗口）。迁移随二进制
   内嵌并在启动时自动应用，无需手工执行；但回滚到旧版本前请确认旧版本不会因这些表报错。
   预留表行数上界是"同时在途的请求数"，每次准入/续期清理本 scope 的过期行，不需要额外清理任务。

## 6. 已知边界与未覆盖入口

护栏当前覆盖以下入口：

**余额模式的 6 条文本链路**：`/v1/messages`（Anthropic 链 + OpenAI 链）、
`/v1/responses`（两条链）、`/v1/chat/completions`（两条链）。

**按次计费的 OpenAI 图片链路**：`/v1/images/generations`、`/v1/images/edits`
（OpenAI 平台 → `OpenAIGatewayHandler.Images`）。图片走**独立的最坏费用上界**
（`EstimateImageRequestSpendUpperBound`：同源于结算用的 `calculateOpenAIImageCost`，
按尺寸档 × 张数计；分组配置了图片单价时取**最贵档**作为上界），并与文本链路共用同一套
在途预留槽位。

`/images/batches` 走独立的 batch image hold 机制，已单独覆盖。

同样接入的入口：Gemini 文本（`/v1beta`）、OpenAI embeddings、SystemOne、独立 `/web_search`、
`/alpha/search`、Grok TTS/STT、Grok realtime（按 1 分钟音频价预留，会话期间续期）、
Grok 媒体（图片按张、视频按秒；视频在后续轮询时才结算：创建请求把预留凭据写进待结算记录
`GrokVideoPendingBilling.reservations` 并续期到 1 小时，认领结算的轮询请求接回凭据，扣费后归还；
客户端始终不轮询时预留最多占用 1 小时后自愈），以及 **OpenAI Responses WebSocket**
（**按 turn** 预留：每个 `response.create` 在转发前单独估算并预留，结算任务扣费后归还；
后续 turn 的复检不重复计 RPM）。准入用的最坏费用取「token 上界」与「计费同口径估算」
（覆盖按次/搜索/托管搜索/音频/媒体）的较高者。

以下入口**尚未接入**上述护栏，仍可能出现"先服务后写坏账"：

- `/images/generations/async`、`/images/edits/async`、`/images/tasks/:id`
  （异步图片任务，`AsyncImage` handler）
- `/v1/live`、`/realtime/calls`（实时语音）、`/v1/realtime`

> 已从本清单移出：**Anthropic 提示过长后的兜底分组重试**（`gateway_handler.go` 的 fallback
> group 分支）。PR#16 起该支路会按兜底分组重新估算最坏费用并**替换**旧分组的在途预留
> （`recheckSelectedGroupRouteEligibility`），不再只做基础阈值检查。

其他已知边界：

- **Redis 不可用 ⇒ 按 scope 降级为 DB 兜底**：`TryReserveUserBalance` 报错时先抬起共享
  `billing_reservation_fallback_state` 窗口，钱包 scope 走 `billing_balance_reservations` 行锁事务，
  订阅 / user×platform / API Key scope 走 `billing_scope_reservations` advisory-lock 事务（护栏
  **不失效**，但对应 scope 的准入被串行化）。Redis 与 DB 同时失败时正式装配 fail-closed
  返回 503；只有未装配 DB 能力的降级/测试装配才保留 fail-open。
  监控：`reservation_redis_failure`、`reservation_fail_closed`、`reservation_fallback_activated`、
  `reservation_fallback_activate_error` / `reservation_fallback_probe_error`、
  `reservation_db_fallback`、`reservation_db_fallback_error`。
- **余额缓存的条件写回（防旧快照复活）**：余额缓存是"未命中回源 + 异步写回"，读取方从 DB
  读到值到写回之间可能发生扣费。每次余额变动（扣费 / 加钱失效）会递增 `billing:balance_gen:<uid>`
  代号；回源方在**发起 DB 读取之前**取一份代号，写回时只有代号未变才发布，否则丢弃这次写回
  （下一次读取回源真值，自愈）。用代号而非时间戳是为了免去多实例时钟偏移问题。
  代号键的 TTL 为 10 分钟，过期后退化为"不设防"，与修复前行为一致。
- **预留后端返回无法解释的状态**：`tryReserveBalanceScript` 只认 `'1'`/`'0'` 两种答复，
  其它值一律按**拒绝**处理（交给调用方的 guard 再判一次），不走 fail-open —— 避免"状态不一致"
  被误当作"后端故障"从而在完全没有预留的情况下放行。
- **预留 TTL 自愈**：预留默认 TTL 10 分钟，长请求由心跳（TTL/3）续期；若结算任务
  丢失且心跳已停止，额度最多滞留 10 分钟。
- **聚合总额由存活凭据实时求和**：Redis 账本为 `billing:reserved:{scope}`（ZSET，成员=
  requestID、分数=过期时刻）+ `billing:reserved_amt:{scope}`（HASH，金额），每次预留/归还/
  续期先清理过期成员再求和。旧实现用一个 INCRBYFLOAT 标量并在每次接受时刷新它的 TTL，
  泄漏的凭据金额（进程崩溃 / 发布 / 结算丢失）在用户持续有流量时**永不衰减**，用户余额充足
  却被持续 403；现在泄漏最多存活一个 TTL。三个键共享 `{scope}` hash tag，兼容 Redis Cluster。
  **滚动发布兼容**：钱包 scope 的聚合同时计入旧版钱包预留键（`billing:inflight:{uid}` /
  `billing:inflight_amt:{uid}`，同一 hash tag），新实例的每笔钱包预留也以 `v2:` 前缀镜像写入
  旧键，旧实例因此同样能看到新实例的在途请求；双方求和都跳过对方镜像，不重复计数。
  （PR3–22 自身旧格式的标量聚合键 `billing:reserved:<scope>` 未在生产部署过，不做兼容。）
- **结算失败不立即归还预留**：结算遇到瞬时 DB 错误会以同一 request_id 重试（dedup 保证幂等，
  共 3 次）；仍失败时 usage_log 记 `actual_cost=0`，预留**保留到 TTL** 而不是立即归还
  （否则这份额度会立刻放给下一笔请求，把一笔未收回的成本放大成一串）。
  监控：`settlement_failed_uncharged`（必须为 0，非 0 时按 request_id 对账补扣）、
  `reservation_held_on_settlement_failure`。
- **持有预留的结算任务永不丢弃**：`overflow_policy=drop/sample` 只对统计类任务生效；
  持有在途预留的结算任务一律走 mandatory 路径（池满时内联执行），否则被丢弃的请求既不扣费
  预留也被立即归还，等于免费放行。
- **无法定价的请求**：token 上界会同时按请求模型与渠道映射后的模型定价取较高者；两套估算
  都无法定价时，若配置了 `billing.inflight_reservation.max_reservation_usd`，按该金额预留
  （记 `precheck_unpriced_fallback`），否则维持旧行为（不预留）。**生产建议配置**该值，
  否则自身无价的别名会跳过全部在途预留。
- **客户端可控的计费因子已计入上界**：`service_tier`、`reasoning.effort` /
  `output_config.effort`（档位价）、Chat `n` / Gemini `candidateCount`（按候选条数放大输出，
  上限 128）、Gemini `generationConfig.maxOutputTokens`。
- **服务端产生的计费已计入上界**：
  - `previous_response_id`：每轮 Responses 结算后记录"本轮输入 + 输出"到
    `openai:resp_ctx_tokens:<response_id>`（24h），下一轮预检把它加进输入上界。会话不经本网关
    （或记录过期）时读不到，按 0 计 —— 该部分仍由结算封底兜底。
  - 托管 `web_search`：按工具声明的 `max_uses`（未声明 1 次，上限 64）叠加按次费，并按每次
    10k token 把检索结果计入输入上界。
  - 1h 缓存写入：请求带 `cache_control` 时，额外按"输入全部计为 1h 缓存写入"估一次取较高者。
  - 图片输出 token：额外按"输出全部计为图片输出 token"估一次取较高者（无图片单价的模型
    回退为输出价，不改变结果）。
- **保守方向的误伤**：输入 token 按**四段口径**估计 —— 普通文本 `字节数/2`；
  **CJK 主导文本**（非结构字节中 CJK 占比 ≥ 90%）按 `每字 1 token`
  （`request_spend_cjk_tokens_per_rune` 可调高，上限 8；该费率按生产上游校准，对
  字节回退型分词器不是上界）；**多模态块内**的 base64（`url`/`image_url`/`data`/
  `file_data` 字段承载，含 OpenAI Responses 的字符串形态 `image_url` 与**带 mime
  参数的 data URI**（`data:image/svg+xml;charset=utf-8;base64,…`），即真实图片/音频
  负载）按固定 allowance（1600/块，与分辨率无关）。其中 `data` 是极常见的通用键名
  （任何自定义 JSON 都可能用它表示数据），因此还要由**父键**确认是否为多模态：
  只有 `source`（Anthropic）/ `inline_data`·`inlineData`（Gemini）/ `input_audio`
  （OpenAI）才算多模态块；父键明确但不是上述之一（如 `{"payload":{"data":"…"}}`）
  时回落稠密/文本口径，父键无法确证（顶层 `data`、数组元素）时保持按多模态折算，
  以免把真实图片降级为稠密口径而误 403。
  `url` / `file_data` 同属通用键名（`{"image":{"url":…}}` 是媒体，`{"payload":{"url":…}}`
  就可能只是长文本），只有父键命中媒体白名单（`image_url`/`input_image`/`input_audio`/
  `image`/`audio`、`input_file`/`file`/`document`）或值前带 `;base64,` 标记才算多模态块；
  **落在灰区时口径取 `max(稠密, 1 块固定额度)`**：按字节折算（1 token/字节）高于块额度时
  用稠密，否则用块额度。两种口径在原始约 1.6KB 处相交，只取其中之一必然在某一侧低估 ——
  这正是审计 R1 实测到的交叉点（固定额度与按字节折算在 512B–1.6KB 一侧相差最多 2.3 倍）。
  **其它位置的 base64**（含没有
  任何 `;base64,` / `"data":"` 标记的裸长串，判定依据是长串前最近的 JSON 键名）按
  **1 token/字节** 稠密计（实测 20KB base64 被上游分词为 18907 token ≈ 0.92 token/字节，
  若按图片折算会低估 4 倍、生产实测产生过一笔 $0.0034 的 write-off）。
  分段写法（MIME/PEM 每 76 字符换行、url-safe 的 `-`/`_`、转义 `\/`）按**同一条串**
  合并识别；无标记的长串再按"字符种类 ≥ 24"判定是否真是 base64（英文文本的 base64
  实测 37 种、十六进制 16 种、`abab…` 2 种），避免把低熵长串按稠密费率高估。
  对超长单行文本等异常内容仍会高估（方向安全）。
- **键名回溯有字节预算**（1024）：`jsonKeyBefore` 只回扫有限字节，避免"A×512;"这类
  构造体触发 Θ(n²) 反向扫描（实测 1.64MB 请求体曾耗 6.19s CPU）；超预算按"未知键"
  处理（归稠密，方向保守）。
- **`userRepo` 未装配的降级部署**：无法做 DB 真值复核，护栏退化为"只按缓存判断"，
  会通过 `recheck_skipped_no_user_repo` 计数并出现在 `degraded_signals` 中。
- **计费幂等的安全前提（重要）**：`usage_billing_dedup` 的主键是
  `(request_id, api_key_id)`，其中 `request_id` **只取服务端可信来源**
  （强持久 money-event id（`web_search:` 等）> 上游响应 id > 服务端生成
  `generated:`），**绝不读取客户端可控的 `X-Client-Request-ID` / `X-Request-ID`**；
  这两个 header 也**不在上游透传白名单内**（否则"回显客户端 id 为响应
  `x-request-id`"的上游会让客户端重新取得幂等键的控制权 —— 恒定一个 id 就能让多笔
  真实调用折叠成一笔、静默去重不扣费，使整条护栏失效）。
  由此推出两条运维含义：
  1. **上游必须返回可信、互不相同的 request id**（或返回空 → 由服务端生成）。若某上游
     对多笔调用复用同一个 id，第二笔起会走"指纹不同 → 换服务端新 id 重新落账"（不丢单，
     会在日志打 `ALERT: usage billing request id conflict`）；该笔 `usage_log.request_id`
     会同步为实际计费键（形如 `<原id>:conflict:<服务端id>`），便于按 request_id 对账。
  2. 幂等以"同一笔调用只提交一次"为前提（worker 池的 `TrySubmit` 成功即仅入队一次，
     失败按 overflow policy 单次降级）。若将来引入记账重试/人工重放，必须复用同一个
     上游 id，否则会被视为两笔独立扣费。
- **API Key 总额度已接入在途预留，但 5h/1d/7d 限流窗口没有**：`Usage5h/1d/7d` 由
  `APIKeyService.UpdateRateLimitUsage` 在结算时递增，准入仍只看鉴权缓存快照，并发突发
  可以超出窗口上限（属于限流而非计费）。要严格限流可复用同一套预留机制
  （scope=`apikey:<id>:<window>`），目前**尚未实现**。
- **API Key 额度与钱包/订阅是并行额度**：同一次请求最多会持有三条预留凭据
  （钱包 + 平台配额 + key 额度，或订阅 + key 额度），归还/续期以凭据为单位；
  任一 scope 预留失败都会整槽回滚，不会留下占着额度的残留凭据。
- **Redis 故障时的 DB 回落（第 ⑤ 层）**：Redis 写失败时不再直接放行，而是按 scope 使用
  `billing_balance_reservations` 或 `billing_scope_reservations` 做回落预留，语义与 Redis 路径
  一致（含拒绝与整槽回滚），并用 `billing_reservation_fallback_state` 保证所有实例在窗口内
  统一切换账本。**Redis 与 DB 都失败时 fail-closed（503）**，只有未装配 DB 兜底的降级/测试
  装配才 fail-open（记 `billing_reservation_fail_open_total`）。与 new-api 的对照：new-api 回落为
  `UPDATE ... WHERE quota >= ?` 条件更新（直接改额度）；sub2api 的 Redis 预留是 TTL 凭据
  （要支持精确退还与续期），因此回落同样用"预留表 + 聚合上限"而不是直接扣余额。
- **债务模式（`billing.settlement_debt_mode=true`）**：结算不再夹封底，余额可以被扣成负数（欠款），
  `usage_log.actual_cost` 保持真实成本、不产生 write-off 差额；`settlement_shortfall_count` 不再增长。
  监控口径需随之调整（关注负余额用户数/欠款总额，而不是 write-off 斜率），并且预检仍以
  `balance <= reserve` 拒绝后续请求，不会因为欠款而持续放行。注意：**`/images/batches` 的 batch image
  hold 仍按 `balance >= hold + reserve` 预冻结**，不产生欠款；未接入护栏的入口
  （Gemini 文本、embeddings、异步图片等）在两种模式下都仍有“预检缺位”的窗口。
