# Sub2API 计费护栏复审报告（PR #3 → #16）：并发准入与"超额"对照 new-api

- 仓库：`ziyue67/sub2api-ui`，基线 `main` @ `f7a45e28b`（= PR #16 merge）
- 本轮工作分支：`codex/billing-audit-pr3-pr16`（从上述基线拉出）
- 审计区间：`2c1982301..f7a45e28b`（PR #3 → #16），本轮重点复核新增的 PR #15 / #16 并对全区间收敛
- 参考实现：`QuantumNous/new-api` @ `b0bf258`（浅克隆于本机临时目录，仅作对照，未改其代码）
- 前序报告：`billing-audit-pr3-pr11.md`（v1）、`billing-audit-pr3-pr12.md`（v2）、`billing-audit-pr3-pr14.md`（PR #14 收敛）
- 本轮性质：**增量复审 + 修复**。复审发现 2 条并发超额通道（F1/F2），已按 new-api 的思路修复；
  随后把与 new-api 的两处实质差距（F3 Redis 故障回落 DB、F4 债务 vs 核销）也补齐（F4 为默认关闭的开关）

本轮实测（Windows + Go 工具链，均为实跑）：

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | ✅ |
| `go vet -tags=unit ./internal/...` | ✅（无输出） |
| `go test -tags=unit -count=1 ./internal/service/...` | ✅ 193.5s（含 openai_ws_v2 3.7s） |
| `go test -tags=unit -count=1 ./internal/config/...` | ✅ 3.0s |
| `go test -tags=unit -count=1 ./internal/handler/...` | ✅（handler 40.6s / admin 1.2s / dto 0.4s / quotaview 0.4s） |
| `go test -tags=unit -count=1 ./internal/repository/...` | ❌ 仅 3 个 pg_dump 用例（**与本次改动无关，环境性**：Windows 无 POSIX `sh`，见 §7） |

## 0. 结论摘要

| 编号 | 问题 | 严重度 | 状态 |
| --- | --- | --- | --- |
| **F1** | user × platform 日/周/月配额只有准入判定（用量在结算才累加），并发突发可超额消耗平台配额 | 高（额度失效、投诉/对账） | ✅ **本轮修复** |
| **F2** | API Key 总额度（`api_key.quota`）同样只有准入判定，并发突发可让 `quota_used` 超出 `quota` | 中（钱照收，仅额度上限失效） | ✅ **本轮修复** |
| **F3** | 预留写 Redis 失败时 `reserveSpendWithGuard` **fail-open**，并发原子性在 Redis 故障期间消失 | 中 | ✅ **本轮修复**（DB 预留表回落，见 §4.4） |
| **F4** | 结算走"封底 + 差额核销（write-off）"，new-api 则允许余额扣成负数（记为债务） | 设计差异 | ✅ **本轮实现**为开关 `billing.settlement_debt_mode`（默认关，保持原行为，见 §4.5） |
| **F5** | 预留凭据 TTL 过期后聚合总额仍偏高；长请求依赖心跳续期 | 低（保守方向） | ⚠️ 已知边界，已写入运维手册 |
| **F6** | API Key 的 5h/1d/7d 限流窗口仍是"准入只读快照 + 结算才累加" | 低（限流非计费） | ⚠️ 记录，未修复 |

**一句话**：PR #16 已经把"钱包余额 / 订阅限额"两条并发超卖通道堵死（原子预留 + 精确归还 + 多绑定槽位）；
本轮把同类通道在 **平台配额** 与 **API Key 额度** 上补齐，使四条额度（钱包、订阅、平台配额、key 额度）
在准入处语义一致：`已用 + Σ在途预留 ≤ 限额`。对 new-api 的对照显示，剩余的实质差距为
**Redis 故障时的 DB 回落**（F3）与 **债务 vs 核销**（F4）：本轮两项均已实现（F3 默认生效；
F4 是默认关闭的开关，保持既有上线行为不变）。

## 1. PR #3 → #16 演进

| PR | 内容 | 结论 |
| --- | --- | --- |
| #3–#12 | 见 v1/v2 报告 | 结论继承（H1–H12 / F1–F10） |
| #13 | 提交 v2 复审报告 | 纯文档 ✅ |
| #14 | 修复 H1–H12 + F10（预检折算口径、幂等键、标记生命周期、legacy 路径、batch hold 校验） | 已实证修复；引入 N1–N4 观察 |
| #15 | 修复 PR #14 的 N1–N4（幂等键透传、对账一致性、`data` 键上下文）+ 文档 | 见 `billing-audit-pr3-pr14.md` 后续提交 |
| **#16** | **钱包在途预留原子化**：`billingCache.TryReserveUserBalance`（Lua 内比较 `maxTotal`）、凭据精确归还、心跳续期、多绑定槽位、handler 接入、`group_route_failover` 重定价 | 本轮重点复核对象 |

PR #16 引入的核心不变量：`balance − Σ在途预留 ≥ reserve`，且该式在"结算扣钱"与"归还预留"以任意
先后顺序发生时都成立；`BillingReservationSlot` 从"单绑定"升级为按凭据多绑定（`bind` 支持
`idle→held` 与 `held→held`）。

### 1.1 五层护栏（当前代码）

| 层 | 位置 | 作用 |
| --- | --- | --- |
| ① 准入阈值 + DB 真值复核带 | 转发前 | `balance <= reserve` 直接 403；贴近封底时 DB 复核；结算判定耗尽后写"钱包已耗尽"标记 |
| ② 最坏费用闸门 | 转发前 | `balance >= reserve + 本次最坏费用`，付不满的请求不转发 |
| ③ 在途预留（并发原子） | 转发前 | Redis 原子累加"已放行未结算"的最坏费用，四类 scope 各自护栏（见 §5） |
| ④ 结算封底 + 差额记账 | 结算 | 默认 `GREATEST(balance - amount, floor)`，差额记 write-off；永不为负。`billing.settlement_debt_mode=true` 时改为全额入账、余额可为负（债务，充值抵扣） |
| ⑤ Redis 故障回落 | 转发前 | Redis 写失败 → DB 预留表 `billing_reservations`（scope 级 advisory lock 串行化）继续做原子准入；只有 Redis 与 DB 都失败才 fail-open |

## 2. new-api 怎么解决"并发 + 超额"

### 2.1 钱包与令牌额度：原子预扣 + Redis 不可用时回落 DB 条件更新

`model/quota_reserve.go`：

- `userQuotaReserveScript`（L20-31）：校验缓存条目属于该用户且 schema 一致 → `HGET Quota` →
  `quota < amount` 返回 `0`（不足）→ 否则 `HINCRBY Quota -amount` 返回 `1`。**判定与扣减在 Lua 内原子完成**。
- `tokenQuotaReserveScript`（L42-55）：对 token 同时动 `RemainQuota` 与 `UsedQuota`，语义与
  sub2api 的 API Key 额度（`quota` / `quota_used`）一一对应。
- `TryReserveUserQuota`（L162-199）：Redis 命中不足 → 直接拒绝；**Redis 报错或 cache miss 且水合失败 →
  `reserveUserQuotaDB`**（L144-149：`WHERE id = ? AND quota >= ?` 条件更新，`RowsAffected == 1` 才算成功）；
  预扣成功后若 `persistUserQuotaDelta` 失败，用 `cacheApplyUserQuotaDelta` **补偿回滚**。
- `TryReserveTokenQuota`（L201-240）：同构；`unlimited` 的 token 跳过余额校验但仍记 `Remain/Used`。

这正是 sub2api 原先（F3）缺失的那一环：**Redis 不可用时不是 fail-open，而是退化成 DB 条件更新**。
本轮已按同一思路实现（见 §4.4）：因为 sub2api 的预留是 TTL 凭据（需要精确退还/续期），回落不是直接
改余额，而是写一张带 `expires_at` 的 `billing_reservations` 表并在 scope 级串行化下校验聚合上限。

### 2.2 生命周期：preConsume → Settle(delta) → Refund

`service/billing_session.go` + `service/funding_source.go`：

- `WalletFunding.PreConsume`（L42-55）调用 `model.TryReserveUserQuota`，失败返回
  `ErrInsufficientWalletQuota`（L30-33，允许 `wallet_first` 偏好回落到订阅）。
- `BillingSession.Settle`（L43-81）用 `delta = actualQuota - preConsumedQuota` 只结算差额，
  `settled` 标志保证幂等；`Refund`（L84-125）异步、幂等，订阅侧走
  `RefundSubscriptionPreConsume(requestId)` 并带 3 次重试（`refundWithRetry`，仅限事务型退款）。
- `shouldTrust`（L317-341）：额度大于 `common.GetTrustQuota()` 时跳过预扣（"信任额度"旁路），
  用少量坏账风险换吞吐——sub2api 没有对应机制（sup2api 一律预检，非 issue，仅记录差异）。

### 2.3 超额后的处理：允许余额为负（债务），而非核销

`model/user.go`：`DecreaseUserQuota`（L1380）→ `decreaseUserQuota`（L1397-1403）执行
无下限的 `quota = quota - ?`。也就是说 new-api 在"预扣不足但请求已完成"时把差额留在账上成为
**债务/负余额**，由后续充值抵扣；sub2api 默认选择"结算封底 + 差额核销（write-off）+ 打耗尽
标记"，两者都能防住"白嫖"，差别在**账务口径**：new-api 追偿、sub2api 止损 + 记账。

本轮把两种口径都做成可实现项：默认仍是核销（历史线上问题 `usage_log.actual_cost = 0` 就是核销
缺失造成的），新增 `billing.settlement_debt_mode=true` 可切到 new-api 的债务口径（见 §4.5）。

## 3. 对照表

| 维度 | sub2api（本轮修复后） | new-api |
| --- | --- | --- |
| 钱包并发准入 | Redis Lua 原子`TryReserveUserBalance`（比较 `maxTotal`，失败即拒） | Redis Lua 原子预扣 + **DB 条件更新回落** |
| 单请求凭据 | `billing:resv_item:<scope>:<requestID>`，精确归还；缺失即 no-op（防晚到归还吃掉他人预留） | 预扣即扣减金额，退还是"加回去"（`IncreaseUserQuota`，非幂等、不重试） |
| 订阅/套餐 | 复用同一套预留机制，scope=`sub:<user>:<group>`，DB 事务 + 幂等退款 | `SubscriptionFunding` + `RefundSubscriptionPreConsume(requestId)` + 重试 |
| 平台配额 | ✅ 本轮新增 scope=`upq:<user>:<platform>` | 无对应概念（new-api 无"用户×平台"维度） |
| API Key 额度 | ✅ 本轮新增 scope=`apikey:<id>`（key 额度与钱包并行计费） | token 额度预留（`RemainQuota`/`UsedQuota`） |
| 长请求保护 | 预留 TTL 10min + 心跳（TTL/3）续期 | 无 TTL（预扣是真实扣减，不需续期） |
| 结算差异 | 默认封底 + 差额核销（write-off，余额永不为负）；`settlement_debt_mode=true` 时与 new-api 一致：全额入账、允许负余额（债务），后续充值抵扣 | 允许负余额（债务），后续抵扣 |
| 故障降级 | Redis 写失败 → **DB 预留表回落**（`billing_reservations`，advisory lock 串行化）保持原子性；Redis+DB 都失败才 fail-open（记录指标） | Redis 失败 → DB 条件更新（保持原子性） |

## 4. 本轮修复

### 4.1 F1：user × platform 配额在途预留

- 新增 `userPlatformQuotaSnapshot`（日/周/月 limit + usage 快照）：
  - `hasLimit()`：三个窗口都没配 → 不产生预留；
  - `reservationBudget()`：取三个窗口 `limit - usage` 的**最小值**（未配置的窗口不参与）作为 Redis 侧 `maxTotal`；
  - `limitExceeded(inFlight)`：复用订阅的边界语义（`usage >= limit` 或 `usage + inFlight > limit` 即拒），
    并附带 `window_resets_at` metadata。
- `loadUserPlatformQuotaEligibility` 由"只回 error"改为**同时返回判定用的快照**，使"判定依据"和"预留依据"
  来自同一次缓存读（避免快照漂移）；Redis 故障时按 §4.4 回落 DB 预留表，ctx 取消仍 fail-open。
- 新增 `reserveUserPlatformQuotaSpend`（scope=`upq:<userID>:<platform>`），走与余额/订阅同一套
  `reserveSpendWithGuard` 原子语义。
- `BillingReservationSlot` 升级为**多绑定**：同一次请求可同时持有"钱包 + 平台配额"（订阅模式下为
  "订阅 + key 额度"）多条凭据，归还/续期按凭据逐条执行；第二条 scope 预留失败时**整槽回滚**，
  不会白占额度到 TTL 到期（`CheckBillingEligibility` 中显式 `reservationSlot.Release(ctx)`）。

### 4.2 F2：API Key 总额度在途预留（参考 new-api 的 token 额度预留）

- 新增 `reserveAPIKeyQuotaSpend`（scope=`apikey:<id>`），护栏 `quota_used + Σ预留 <= quota`，
  失败返回既有错误 `ErrAPIKeyQuotaExhausted`（HTTP 429），并打日志：
  `billing preflight rejected api_key=%d (quota inflight reservation): used=... reserved=... would exceed quota=...`。
- 只在 `quota > 0`（非不限量）且本次请求有最坏费用上界时生效；未配置额度 / 无上界 / 缓存不支持预留时静默 no-op。
- **两种计费模式都生效**（余额与订阅），因为 key 额度与钱包、订阅是并行的第三道额度；同样是第三类 scope，
  失败时整槽回滚。
- 边界语义与既有实现对齐：`quota_used >= quota` 仍判拒（无预留时的旧边界），第二项只在有在途预留时收紧。

### 4.3 新增单测

`backend/internal/service/billing_cache_service_reservation_test.go`：

| 用例 | 锁死的行为 |
| --- | --- |
| `TestPlatformQuotaReservation_BlocksConcurrentOversell` | 平台配额 daily=0.30、每笔最坏 0.10：前 3 笔放行、第 4 笔 403，被拒请求的两条凭据都回滚 |
| `TestPlatformQuotaReservation_AtomicStoreLeavesNoResidue` | **生产路径**（底层缓存实现 `TryReserveUserBalance`）：越界在 Lua 内被挡下、不留残留，仅回滚已绑定的钱包预留 |
| `TestPlatformQuotaReservation_UsesTightestWindow` | 三个窗口取最紧：daily 宽松、weekly 只够 2 笔时第 3 笔按 weekly 错误拒绝 |
| `TestPlatformQuotaReservation_HandOffReleasesBothScopes` | 移交结算后收尾兜底不得提前归还；结算完成时两个 scope 全部归还 |
| `TestPlatformQuotaReservation_NoopWithoutLimits` | 未配置平台限额时不写无意义的预留键 |
| `TestAPIKeyQuotaReservation_BlocksConcurrentOversell` | key 额度 quota=0.30：前 3 笔正好用满、第 4 笔 429，且余额预留随拒绝回滚 |
| `TestAPIKeyQuotaReservation_NoopForUnlimitedKey` | `quota<=0`（不限量）不产生预留 |
| `TestAPIKeyQuotaReservation_RollsBackSubscriptionBindingOnReject` | 订阅模式下订阅预留已绑定、key 额度被拒 → 整槽回滚，零残留 |

测试桩同步升级：`reservationCacheStub` 改为按 scope 记账（`receipts` / `reservedNano` 双 map，
预留金额用 nano 整数累积以复现 Redis `INCRBYFLOAT` 的十进制语义），并新增
`atomicReservationCacheStub` 以覆盖生产用的原子预留分支（`TryReserveUserBalance`）。

### 4.4 F3：Redis 故障时回落 DB 预留表（对齐 new-api 的 DB 条件更新）

- 新增表 `billing_reservations(scope, request_id, amount, expires_at, created_at)`（migration `238`，
  PK=`(scope, request_id)` 保证幂等，`expires_at` 上有索引）。
- 新增 `repository.billingReservationDBStore`（`billing_reservation_db.go`）：
  - `TryReserveUserBalance`：事务内 `pg_advisory_xact_lock(hashtext($scope))` 按 scope 串行化 →
    清理/忽略过期行 → `SUM(amount)` 聚合现有预留 → `已用+在途+本笔 > maxTotal` 则拒绝 → 写入本笔凭据；
  - `ReserveUserBalance`（无上限预算）、`ReleaseUserBalanceReservation`（只删自己的凭据，缺失返回
    `ErrBillingReservationExpired`）、`RenewUserBalanceReservation`（心跳续期）。
- `service.BillingCacheService` 新增可选依赖 `ReservationFallbackStore`（`SetReservationFallback`，由
  `cmd/server/wire_gen.go` 装配），`reserveSpendWithGuard` 的两条 Redis 错误分支改为
  "先试 DB 回落，DB 也失败才 fail-open"；回落成功的槽位照旧按凭据归还/续期，同一请求不会双计。
- 不装配回落 store 时行为与修复前完全一致（fail-open），保持测试/降级部署的零值安全。
- 边界：DB 回落只保証“准入预留”的原子性，结算扣费仍走原 `usage_billing` 事务；
  回落期间多一次 DB 往返（仅在 Redis 故障时发生）。

### 4.5 F4：债务 vs 核销做成开关 `billing.settlement_debt_mode`（默认关）

- 新增配置 `billing.settlement_debt_mode`（`BillingConfig.SettlementDebtMode`，默认 `false`）：
  - `false`（默认/零值安全）：保持封底核销 —— `GREATEST(balance - amount, floor)`，余额永不为负，
    差额记 write-off 并计入 `settlement_shortfall_count`；
  - `true`：债务模式 —— 全额入账、余额可扣成负数（欠款），`usage_log.actual_cost` 保持真实成本，
    不再产生 write-off；对齐 new-api `model/user.go` 的 `decreaseUserQuota`（无下限扣减）。
- 统一结算路径：新增 `deductBalanceToDebtSQL` / `deductBalanceToDebt` 与调度器
  `deductUsageBillingBalanceWithMode`（`usage_billing_repo.go`，由 `r.settlementDebtMode` 选择）；
  债务 SQL 同样用 `FOR UPDATE` 锁行，并发结算仍然串行化；无返回行=用户不存在（无“扣不动”分支）。
- 降级（legacy）路径：`userRepository.DeductBalanceAllowNegative` + 服务端可选接口
  `balanceDebtDeductor`（`gateway_usage_billing.go`），保证 repo=nil 的兜底路径与统一路径语义一致。
- 两种模式都防白嫖：预检仍要求 `balance > reserve`（负余额用户下一次预检直接 403）。
- 开启前需确认充值/催收流程能识别负余额用户（充值先抵扣欠款）。

### 4.6 新增单测（F3/F4）

| 用例 | 锁死的行为 |
| --- | --- |
| `TestReserveRequestSpend_FallsBackToDBWhenRedisUnavailable` | Redis 写失败时改用 DB 回落预留，不再 fail-open |
| `TestReserveRequestSpend_FallbackRejectIsFailClosed` | 回落 store 达到上限时拒绝（fail-closed），不是放行 |
| `TestDeductUsageBillingBalanceWithMode_DebtModePushesWalletNegative` | 债务模式全额入账：0.30 扣 0.75 → `-0.45`，`Shortfall==0` |
| `TestDeductUsageBillingBalanceWithMode_FloorModeStillClamps` | 开关关闭时仍走封底 SQL（零值安全） |
| `TestDeductUsageBillingBalanceWithMode_DebtModeUserNotFound` | 债务模式无返回行=用户不存在，且不需额外 EXISTS 查询 |
| `TestApplyUsageBillingEffects_DebtModeKeepsFullCost` | 仓库接线：债务模式 `NewBalance<0`、`BalanceShortfall==0` |
| `TestNewUsageBillingRepository_ReadsBillingConfig` | 配置接线：不传 cfg 时零值安全（照旧封底） |
| `TestLoadSettlementDebtModeDefaultsToWriteOff` / `TestLoadSettlementDebtModeFromFileAndEnv` | 默认 `false`；YAML 与环境变量都可显式开启 |
| `TestGatewayServiceRecordUsage_LegacyFallbackDebtModeKeepsFullCost` | legacy 兜底路径在债务模式下不走封底、全额入账 |

## 5. 文档同步

`docs/BILLING_ZERO_OVERSHOOT.md`（运维手册）更新：

- §1 增加第 4 类表现（平台配额 / API Key 额度只有准入判定）；
- §2 第 ③ 层改写为"四类 scope"（钱包 / 订阅 / 平台配额 / key 额度），并说明多绑定槽位与整槽回滚；
- §2 第 ④ 层补充 `settlement_debt_mode` 债务模式，并新增第 ⑤ 层（Redis 故障回落 DB 预留）；
- §3 配置表新增 `settlement_debt_mode`；
- §6 把 API Key 额度预留、Redis 故障回落、债务模式三条边界改为现状描述（含 new-api 的对照）。

## 6. 已修复项与剩余边界

### 6.1 F3：Redis 故障时预留 fail-open → 已修复（DB 回落）

现状：`reserveSpendWithGuard` 遇 Redis 写失败时先尝试 DB 回落预留（`billing_reservations`，
scope 级 `pg_advisory_xact_lock` 串行化 + 过期行清理 + `SUM(amount)` 聚合校验），成功则语义与
Redis 路径一致；**只有 Redis 与 DB 都失败**时才记 `RecordBillingReservationFailOpen()` 放行。
对照 new-api（`quota_reserve.go:144-199`）的差距已收敛：它用 `UPDATE ... WHERE quota >= ?` 条件
更新，sub2api 用"预留表 + 聚合上限"达到同样的跨请求原子性（因为 sub2api 的预留是 TTL 凭据，
不能直接扣余额，否则退还/续期语义会变成真实资金变动）。

剩余边界：

1. Redis 与 DB **同时**不可用时仍为 fail-open（必现的降级选择：完全 fail-closed 会让全站中断）；
   此时 `billing_reservation_fail_open_total` 会增长，应当告警。
2. DB 回落期间多一次数据库往返（仅在 Redis 故障时发生）。
3. 结算扣费仍走原有 `usage_billing` 事务，DB 回落只覆盖准入侧的在途预留。

### 6.2 F4：债务 vs 核销 → 已实现为开关（默认保持核销）

`billing.settlement_debt_mode` 默认 `false`（封底核销，与历史行为逐位一致）；置 `true` 后结算
全额入账、余额可为负（债务），由后续充值抵扣，对齐 new-api `decreaseUserQuota`。
**追偿能力**差异由开关交给业务选择：需要“先服务后追偿”时打开，并配套负余额可识别的充值/催收
流程；对账口径也随之变化（不再有 write-off 差额，`actual_cost` 等于真实成本）。

剩余边界：

1. **batch image hold**（`/images/batches`）仍按 `balance >= hold + reserve` 预冻结，不产生欠款；
   债务模式不影响该路径（冻结是预扣，不存在“收不满”）。
2. 未接入护栏的入口（Gemini 文本、embeddings、异步图片、实时语音、视频等）在债务模式下同样会
   把超额部分记成欠款而不是核销，但“预检缺位 → 未识别耗尽”的窗口仍然存在（见手册 §6）。
3. 负余额对**下游对账/报表**是新的取值域，切换前需确认 BI/导出不会把负余额当成异常数据丢弃。

### 6.3 其他

- **API Key 的 5h/1d/7d 限流窗口**（`Usage5h/1d/7d`，结算时 `UpdateRateLimitUsage` 才递增）
  是同一类"准入只读快照"问题，但属于限流而非计费；若需要严格限流，可复用同一套预留（scope=`apikey:<id>:window`）。
- **预留 TTL 与心跳**：TTL=10min、心跳=TTL/3。进程崩溃且结算任务丢失时，额度最多滞留 10 分钟（保守方向）。
- **未接入护栏的入口**（Gemini 文本、embeddings、异步图片、实时语音、视频等）见手册 §6 清单。

## 7. 验证记录

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | ✅ 通过（F3/F4 落地后重跑） |
| `go vet -tags=unit ./internal/...` | ✅ 无输出 |
| `go test -tags=unit -count=1 -run "Test(PlatformQuotaReservation|APIKeyQuotaReservation|BillingReservationSlot|ReserveRequestSpend|SubscriptionReservation|ReleaseReservation)" ./internal/service/` | ✅ `ok ... 0.60s` |
| `go test -tags=unit -count=1 -run "DebtMode|ReadsBillingConfig|FloorModeStillClamps" ./internal/repository/` | ✅ 5/5 PASS（债务模式负余额、封底零值安全、无用户、仓库接线、配置接线） |
| `go test -tags=unit -count=1 -run "TestLoadSettlementDebtMode" ./internal/config/` | ✅ 2/2 PASS（默认 false；YAML/环境变量可开启） |
| `go test -tags=unit -count=1 -run "TestGatewayServiceRecordUsage_LegacyFallback" ./internal/service/` | ✅ `ok ... 0.427s`（legacy 封底/债务/耗尽三例） |
| `go test -tags=unit -count=1 ./internal/service/...` | ✅ `ok ... 193.543s`（含 openai_ws_v2 3.705s） |
| `go test -tags=unit -count=1 ./internal/config/...` | ✅ `ok ... 3.024s` |
| `go test -tags=unit -count=1 ./internal/handler/...` | ✅ handler 40.575s / admin 1.200s / dto 0.410s / quotaview 0.426s |
| `go test -tags=unit -count=1 ./internal/repository/...` | ❌ 仅 3 个 pg_dump 用例：`TestPgDumperHoldsMigrationLockThroughReaderClose` / `...ReleasesMigrationLockWhenProcessFails` / `...ReportsUnlockFailureAndDiscardsConnection`（`exec: "sh": executable file not found in %PATH%`，Windows 无 POSIX `sh`，**与本次改动无关**，基线同样失败） |

**未运行**：`-tags=integration`（需要真 PG/Redis，testcontainers）、前端 `vitest`/`vue-tsc`、
生产压测、`golangci-lint`。第 4.1 节的生产路径（Lua `TryReserveUserBalance`）由仓库层集成测试
`backend/internal/repository/billing_cache_reservation_integration_test.go` 覆盖，但需 Redis 才能实跑。

## 8. 变更文件清单（本轮）

| 文件 | 变更 |
| --- | --- |
| `backend/internal/service/billing_cache_service.go` | 新增 `userPlatformQuotaReservationScope` / `apiKeyQuotaReservationScope` / `reserveUserPlatformQuotaSpend` / `reserveAPIKeyQuotaSpend`；`BillingReservationSlot` 多绑定；`CheckBillingEligibility` 三条 scope 串联与整槽回滚；平台配额快照化 |
| `backend/internal/service/billing_cache_service_reservation_test.go` | 按 scope 记账的预留桩 + 原子预留桩；新增 8 个用例（平台配额 5 + key 额度 3）+ F3 回落 2 个用例 |
| `backend/internal/repository/billing_reservation_db.go`（新增） | DB 回落预留仓储：scope 级 `pg_advisory_xact_lock` 串行化 + 过期清理 + `SUM(amount)` 聚合校验；提供 `Reserve` / `TryReserve` / `Release` / `Renew` |
| `backend/migrations/238_billing_reservations.sql`（新增） | `billing_reservations(scope, request_id, amount, expires_at, created_at)` + 过期索引 |
| `backend/internal/service/billing_cache_service.go` | 新增 `ReservationFallbackStore` 接口、`SetReservationFallback` 与 `reserveWithFallbackStore`；`reserveSpendWithGuard` 两条 Redis 错误分支改为“先试 DB 回落” |
| `backend/internal/config/config.go` | 新增 `BillingConfig.SettlementDebtMode`（`billing.settlement_debt_mode`，默认 false）+ viper 默认值 |
| `backend/internal/repository/usage_billing_repo.go` | 新增 `deductBalanceToDebtSQL` / `deductBalanceToDebt` / `deductUsageBillingBalanceWithMode`；仓库字段 `settlementDebtMode` 由配置注入 |
| `backend/internal/repository/user_repo.go` | 新增 `DeductBalanceAllowNegative`（legacy 路径的债务扣款） |
| `backend/internal/service/gateway_usage_billing.go` | 新增可选接口 `balanceDebtDeductor`；legacy 兜底路径在债务模式下改走债务扣款 |
| `backend/cmd/server/wire_gen.go` | 装配 `BillingCacheService.SetReservationFallback(repository.NewBillingReservationDBStore(db))` |
| `backend/internal/repository/usage_billing_repo_unit_test.go` | 新增债务模式 4 个用例（负余额/封底零值/无用户/仓库接线） |
| `backend/internal/config/config_test.go` | 新增债务模式默认值 + YAML/环境变量加载用例 |
| `backend/internal/service/gateway_record_usage_test.go` | 新增 legacy 债务路径用例 |
| `docs/BILLING_ZERO_OVERSHOOT.md` | 五层护栏（含 DB 回落与债务模式）、新增 `settlement_debt_mode` 配置行与边界说明 |
| `billing-audit-pr3-pr16.md` | 本报告 |
| `billing-audit-pr3-pr16.md` | 本报告 |
