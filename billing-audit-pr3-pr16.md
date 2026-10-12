# Sub2API 计费护栏审计报告（PR #3 → #16）

- 仓库：`ziyue67/sub2api-ui`，`main` @ `f7a45e28b`（= PR #16 merge，2026-09-18）
- 审计区间：`2c1982301..f7a45e28b`，PR #3–#16 共 14 个合并提交 / 88 文件（+8845 −399）
- 本轮性质：**增量审计 + 横向对标**。前序三轮已覆盖 PR3→#11（v1，F1–F9）、PR3→#12（v2，H1–H12）、
  PR3→#14（v3，N1–N4 及其修复）。本轮独立工作量集中在 **PR #16**，并首次引入
  **参考实现对标**：`QuantumNous/new-api` @ `b0bf258`（本机 `D:/C++VS pro/new-api`）。
- 源码获取：**全新克隆** `C:/Users/jun23/sub2api-ui-pr16-audit`（工作区干净，`git status` 无输出），
  新分支 `audit/pr3-pr16-newapi-concurrency`。*注：`D:/C++VS pro/sub2api-ui` 工作区当时正被另一进程写入
  （`billing_cache_service.go` 处于未提交修改状态，短时间内的两次编译给出的错误都不一致），
  因此本轮全部结论均取自**全新克隆**，未使用该工作区。*
- 实测手段（本机 Windows，全部实跑，日志 `/tmp/pr16-verify.log`）：
  - `go build ./...` ✅（rc=0）
  - `go vet -tags=unit ./internal/...` ✅（rc=0，无输出）
  - `go test -tags=unit -count=1 ./internal/{service,repository,config,handler}/...` ✅ 全绿
    （service 193.3s / repository 5.0s / config 4.2s / handler 39.8s）
  - `gofmt -l internal/{service,repository,handler}` → **2 个文件不合规**（见 R7）
  - 定向探针（临时 `zz_pr16_probe_test.go`，**已删除**，两个工作树均确认干净）：
    直接调用仓库内 `EstimateRequestInputTokensUpperBound`，并在 `504473d6b`（PR16 前）与
    `f7a45e28b`（PR16 后）两个提交上做 **A/B 尺寸扫描**（见 §5 R1 与附录）
  - **未运行**：`-tags=integration`（testcontainers 真 PG/Redis）、前端 vitest/vue-tsc、golangci-lint、
    生产压测；new-api 侧**未运行任何测试**（仅静态阅读 + 其自带测试源码作为设计意图证据）

## 0. 结论摘要

| 对象 | 内容 | 结论 |
| --- | --- | --- |
| **PR #15** | N1–N4 修复（幂等键透传 / 对账一致性 / `data` 键父上下文） | 已在 v3 报告 §7 记录，本轮复核**未被推翻** |
| **PR #16** | 原子预留（`tryReserveBalanceScript`）、路由切换预留替换、`url`/`file_data` 键上下文收紧、依赖升级 | **主目标达成**：把「累加预留 + 上限判定」合并为单次 Redis Lua，消除了"先累加后判定再回滚"的 TOCTOU 与"拒绝流量给历史泄漏续命"两个缺陷；但引入了 1 项**方向性反转**（R1）与若干工程性缺口（R7/R8） |

**一句话**：PR #16 是这条护栏线上**架构意义最大的一次改动**——它把代码注释里自己写下的
"彻底消除该取舍需要把护栏判定下沉进本脚本……留作后续改造"（`billing_cache.go:158-159`）
真正落地了。剩余风险不在"改错了"，而在**新口径的适用边界**（R1）与**降级路径的护栏强度**（R2/R9）。

**与 new-api 对标的一句话**：new-api 走的是"**准入即真扣余额**"路线（余额本身就是并发控制原语，
不存在第二份预留账本），sub2api 走的是"**余额快照 + 影子预留账本**"路线。前者结构上不会超额、
但代价是把余额写成热键并要求批量落库且**禁止用陈旧 DB 余额做准入**；后者保留了"结算封底、
余额永不为负"的强约束，但必须自己维护两份状态的一致性。两条路线各有取舍，**最值得移植的是
new-api 的三条具体做法**：缓存水合的 fence（R9）、Redis 故障时的 DB 条件更新兜底（R2）、
以及"批量模式下绝不回退到陈旧 DB 余额"的显式禁令（§3）。

## 1. PR #16 逐项核验

PR #16 = `504473d6b..f7a45e28b`，11 文件（+321 −96）。提交历史上它是一个**单亲提交**
（`parent` 只有 `504473d6b`，非真合并），内容完整对应下列四项。

### 1.1 原子预留：`tryReserveBalanceScript`（主要改动）✅

- 位置：`backend/internal/repository/billing_cache.go:186-205`（Lua）、`:413-440`（Go 封装
  `TryReserveUserBalance`）；调用点 `backend/internal/service/billing_cache_service.go:1649-1665`。
- 语义：一次 Lua 内完成「读聚合 → 累加 → 与 `maxTotal` 比较 → 只在通过时落凭据并刷新 TTL」，
  返回 `{accepted, total}` 三元组（`0`/`1`/`-1`）。
- **修复的两个既有缺陷**：
  1. **TOCTOU 与回滚窗口**：旧路径是"先 `reserveBalanceScript` 累加 → Go 侧判预算 → 不通过再
     `release` 回滚"。被拒请求也完整走完脚本、留下过状态；新路径被拒请求**不写任何键**。
  2. **拒绝流量给历史泄漏续命**：旧脚本在键已存在时会 `PEXPIRE` 续期（注释 :142-153 已记录该故障：
     "并发突发时拒绝量远大于放行量，泄漏被反复续期、永不消失，用户会在余额远高于封底时就因在途
     预留被持续 403，而且只要还有流量就永远无法自愈"）。新路径只有**被接受**的请求才刷新 TTL。
- 与旧路径的兼容：`reserveSpendWithGuard` 保留旧 `ReserveUserBalance` 作为
  `atomicBillingReservationStore` 未实现时的回退（`:1670`）。生产实现 `billingCache` 实现了新接口，
  因此**线上恒走原子路径**；旧脚本现仅测试/降级装配可达（★见 R2 同类的"降级路径强度"讨论）。
- 实测：新增集成测试 `TestTryReserveIsAtomicAndIdempotent`
  （`billing_cache_reservation_integration_test.go:68-118`）用 8 个 goroutine 同一起跑线抢
  `maxTotal=0.30`、单笔 `0.10`，断言**恰好 3 笔被接受**、总额 `0.30`、同 `requestID` 重放不重复累加。
  ⚠️ 该测试带 `-tags=integration`，本机未执行（CI 报告为绿）；**"恰好 3 笔"这一核心不变量本轮
  未经本机独立复现**，见 §8。

### 1.2 路由切换时替换预留 ✅（有一处未收口，见 R5）

- 新增 `recheckSelectedGroupRouteEligibility`（`backend/internal/handler/group_route_failover.go:100-128`）：
  `slot.Release()` → `slot.ResetForRetry()` → 用**新路由重新估算的最坏费用**（`WithMaxRequestSpend`）
  与新 scope 重新做 `CheckBillingEligibility`（`WithBalanceReservation`）。
- 新增 `BillingReservationSlot.ResetForRetry`（`billing_cache_service.go:354-365`）：只把 `released`
  态复位为 `idle`，`settling` 态永不复位 —— 方向正确（防止"结算尚未扣钱、预留先被复用"）。
- 覆盖：`gateway_handler.go`（Messages，:703/:750/:1088/:1129）、`openai_chat_completions.go`
  （:212/:241/:430）、`openai_gateway_handler.go`（Responses，:702/:737/:992）。
- **兜底分组分支的关键修复**：旧代码在 fallback group 上**刻意不挂预留槽位**（注释：避免二次挂槽留下
  永不归还的凭据），只做资格检查——即"按兜底分组倍率结算，但按主分组的预留放行"。PR #16 改为
  "释放旧预留 + 按兜底分组重估 + 重新建立预留"，且复用同一 slot 不会新增第二份凭据（新预留会拿到
  新的 `requestID`）。这是本 PR 里**方向最正确**的一处修复。
- 等价性核对：`cloneAPIKeyWithGroup`（`gateway_handler.go:1665-1674`）确实设置 `cloned.Group = group`，
  因此 `recheck...` 内部用 `apiKey.Group` 与旧代码显式传 `fallbackGroup` **等价**，无行为漂移。

### 1.3 多模态键名上下文收紧（`url` / `file_data`）⚠️ 方向对、边界未覆盖（R1）

- 位置：`gateway_request_spend_estimate.go:680-711`（`multimodalPayloadKeyIsBinary`）。
  `data` 保持"需父键白名单"（PR #15 的 N4 修复）；`image_url` 直接判真；**新增**：`url`/`file_data`
  只有在「值前有 `;base64,` 之类标记」或「父键属于媒体白名单」时才算多模态块，否则回落稠密/文本口径。
- 实测（同一份高熵 base64，A/B 两提交，详见附录）：1.2MB 的 `{"payload":{"url":…}}`
  **1 627 → 1 200 027**（约 1 token/字节稠密），修掉了 N4 报告中"131/737×"低估面；
  `{"image":{"url":…}}` / `{"input_file":{"file_data":…}}` 仍为 1 块（1 626 / 1 631），未误伤真实媒体。
- **但新增的交叉点没有被测到**：见 R1。

### 1.4 依赖升级 ⚠️ 工程性（R8）

`backend/go.mod`/`go.sum` 同 PR 改了 `golang.org/x/{crypto,mod,net,sync,term,sys,text,tools,exp}`、
`google.golang.org/grpc 1.82.1→1.83.2`、`go.opentelemetry.io/otel 1.43→1.44`、`genproto/rpc` 等。

## 2. 参考实现：new-api 怎么解并发与超额

> 源码：`QuantumNous/new-api` @ `b0bf258`（本机 `D:/C++VS pro/new-api`）。以下均为**静态阅读**结论，
> 未在其上运行测试；引用的测试名是 new-api 自带的，作为**设计意图证据**而非本机验证结果。

### 2.1 核心选择：准入即真扣余额（没有第二份预留账本）

`model/quota_reserve.go`：

```lua
-- userQuotaReserveScript (:20-31)
if Id/CacheSchema 不符 或 Quota 字段不存在 then return -1 end   -- 缓存失效
local quota = HGET(Quota)
if quota == nil or quota < ARGV[1] then return 0 end            -- 余额不足
HINCRBY Quota -ARGV[1]                                          -- 原子扣减
return 1
```

- 三层取值：**Redis Lua 原子比较并扣减** → miss 时 `GetUserCache` 水合后重试一次 → 仍不可用则
  **降级为 DB 条件更新** `UPDATE users SET quota = quota - ? WHERE id = ? AND quota >= ?`
  （`:144-149`），`RowsAffected == 1` 即成功。令牌维度同构（`:42-55` / `:151-160`）。
- 预扣成功后 `persistUserQuotaDelta` 落库；落库失败 → `cacheApplyUserQuotaDelta(id, +quota)` **补偿缓存**
  并把失败返回给调用方（`:191-197`）。批量模式下入队（`BatchUpdateEnabled`，`:106-121`）。
- **关键差异**：sub2api 的预留是"影子账本"（`billing:reserved:*`），余额与预留在两个键里；
  new-api 直接把余额当预留，**不存在两份状态需要对齐**，因此也不需要「凭据键防误还」
  （sub2api `resv_item`）、「预留 TTL 自愈」、「心跳续期」这一整套补偿机制。

### 2.2 为并发超扣专门写的注释与测试（最值得借鉴的部分）

- `TryReserveUserQuota` 注释（`:162-164`）：**"缓存命中时以缓存余额为准（避免批量模式下过期的
  数据库余额放大并发超扣）"**。
- `TestRedisBatchReserveNeverFallsBackToStaleDatabaseBalance`（`quota_reserve_test.go:106-139`）：
  余额 10，批量模式（DB 尚未落库仍是 10）→ 扣 8 成功 → 再扣 3 **必须失败**，且缓存 `Quota==2`；
  `batchUpdate()` 后 DB 才变成 2。测试名就是结论：**陈旧 DB 余额不得授权第二次消费**。
- `TestReserveFallsBackToDatabaseWhenRedisIsUnavailable`（`:173-192`）：关掉 Redis 后条件更新仍然
  拒绝超额（20→15，再扣 16 失败）→ **降级不丢不变量**。
- `TestSynchronousReserveCompensatesCacheWhenPersistenceFails`（`:194-221`）：用户被删 → 返回
  `ErrRecordNotFound` 且缓存被补偿回原值。
- `TestTokenCacheInitPreservesLiveQuotaAndFenceBlocksStaleSnapshot`（`:223-261`）：
  ① **缓存初始化不得覆盖已被原子预扣的余额**（已存在的哈希只刷 TTL）；
  ② 变更期间用 **fence**（`invalidateTokenCacheForMutation`）删除缓存并拦截并发读者手里的过期快照，
  fence 过期后才允许重新从 DB 水合。→ 这正是 §4 建议 R9 的来源。

### 2.3 会话化结算：`BillingSession`

`service/billing_session.go` + `service/billing.go` + `service/funding_source.go`：

- 生命周期：`preConsume` → `Reserve(target)`（发送前按实际入参补充预扣）→ `Settle(actual)` →
  `Refund()`。`Settle` 只处理差额 `actual - preConsumed`，正差额补扣、负差额退款。
- 状态机：`settled` / `refunded` / `fundingSettled` 三个布尔 + `sync.Mutex`。
  `fundingSettled` 的作用是"资金来源已提交，令牌调整失败不能再退资金"（`:41-42`、`:70-73`）。
  这与 sub2api 的 `BillingReservationSlot` 五态（idle/held/settling/released + 幂等保护）是**同构**设计，
  可互为参照。
- `Refund` 是**异步**的（`gopool.Go`，`:108`），注释明确"钱包 `IncreaseUserQuota` 是非幂等操作，
  不能重试"（`funding_source.go:71-73`），订阅退款因为有 `requestId` 幂等保护才允许重试。
  → 与 sub2api 的 `usage_billing_dedup(request_id, api_key_id)` 幂等键是同一类问题的两种答案：
  new-api 用"不重试"规避，sub2api 用"服务端可信 id 去重"解决。

### 2.4 信任额度旁路：超额换吞吐的显式取舍

`shouldTrust`（`billing_session.go:317-350`）：`trustQuota > 0` 且用户/令牌余额都 `> trustQuota` 时，
`effectiveQuota = 0`，**完全跳过预扣**，只在结算时扣。订阅永不启用信任旁路（注释给了三条理由）。
非图片路径的补充预扣注释更直白（`:266-269`）："全额无条件扣减，余额不足的部分记为**欠费**
（余额可为负），不中断请求"。

→ 也就是说 **new-api 明确接受"信任用户可能出现负余额/欠费"**；sub2api 则用
`minimum_balance_reserve`（默认 $0.10）+ `GREATEST(balance - amount, floor)` **硬保证余额不为负**，
把差额记成有界 write-off。两种取舍都自洽，但 sub2api 的更适合"上游成本不可追回"的中转场景。

### 2.5 限流与并发闸门

- `common/limiter/lua/rate_limit.lua`：**Redis Lua 令牌桶**（`tokens`/`last_time` 哈希 + `TIME`），
  `middleware/model-rate-limit.go` 按模型做 RPM/TPM。
- `middleware/rate-limit.go`：固定窗口限流（`redisFixedWindowTake`）+ 内存兜底。
- **没有显式的"在途请求数"闸门**（除任务插件 `in_flight_count` 外，全仓 grep `Concurrent`/`InFlight`
  无请求级并发槽实现）。
- 对照：sub2api **有**完整的多层在途槽位系统 —— `ConcurrencyHelper.AcquireUserSlotWithWait`
  （`gateway_helper.go:451-516`）+ 账号槽 + API Key 槽，带**等待队列**（`CalculateMaxWait` 限长）、
  流式 ping 保活、超时；覆盖 `/v1/messages`、chat/completions、responses、gemini v1beta、
  openai live、grok media。**这一层 sub2api 明显强于 new-api**，是本轮唯一"sub2api 领先"的维度。

### 2.6 溢出钳制与可审计性

`common/quota_math.go:82-102`：`saturateQuota` / `saturateQuotaBounded` 在溢出/下溢/NaN 时返回
`*QuotaClamp{Op, Kind, Original, Clamped}`，`AuditMap()` 可直接进管理端 info；
`service/quota_saturation_test.go:105-122` 断言**饱和发生在扣费之前**（`PreConsumeBilling` 直接
返回 `ErrorCodeModelPriceError`，不产生任何扣减）。
对照 sub2api：`saturatingMul` → `MaxInt32`，配置层钳到 8（`request_spend_cjk_tokens_per_rune`），
`config.go:3257` 拒绝 `>8` —— 两者都安全，但 new-api 的"饱和事件带原值进审计"更利于排障。

## 3. 对照总表

| 维度 | new-api @ `b0bf258` | sub2api @ `f7a45e28b` | 评价 |
| --- | --- | --- | --- |
| 准入原语 | **余额原子扣减**（Lua HINCRBY / SQL `WHERE quota >= ?`） | **影子预留账本**原子累加 + 上限判定（PR16 后单次 Lua） | 结构上 new-api 更简单；sub2api 保留了"余额永不为负" |
| 状态份数 | 1（余额即可花额度） | 2（`billing:balance:` + `billing:reserved:`+凭据） | 2 份状态需要 TTL 自愈/心跳/凭据键一整套补偿，PR16 把其中最大的两个洞补上了 |
| 并发超额 | 结构上不可能（余额不落负数；信任旁路例外） | 由 `balance − Σ预留 ≥ reserve` 单一标量封顶；超发面收窄为"结算封底 + 有界 write-off" | 两者都成立；sub2api 的封底语义更严格 |
| 陈旧余额 | **显式禁止**用批量模式下的旧 DB 余额做准入；缓存为准 | 近封底时**故意**回源 DB 真值复核（`balance_recheck_band`）；预留账本为独立封顶 | **两者都对**——因为"谁是权威"取决于写入是同步还是批量：new-api DB 滞后 ⇒ 必须用缓存；sub2api DB 同步写 ⇒ 用 DB 更准 |
| Redis 故障 | **降级为 DB 条件更新，不变量保持** | 预留 **fail-open**（`RecordBillingReservationFailOpen` + 放行），并发护栏整体消失，仅剩阈值+DB复核+耗尽标记+结算封底 | **R2：本轮最可落地的差距** |
| 缓存回源覆盖 | fence + "已存在的哈希只刷 TTL"，禁止旧快照覆盖已扣余额 | 无对应保护（`SetUserBalanceCache` = 无条件 `SET`） | **R9（既有）** |
| 结算 | 会话化差额调整，三态防重复退款，退款异步且对钱包不重试 | 结算封底 `GREATEST(balance-amount, floor)` + 差额记账 + 幂等键 | 各有侧重；sub2api 的幂等键更健壮 |
| 限流 | Lua 令牌桶（按模型）+ 固定窗口 | Redis 固定窗口 RPM（override→group→user 级联）+ 5h/1d/7d 金额窗口 | 相当 |
| 在途并发闸门 | 无请求级实现 | **用户/账号/API Key 三层槽位 + 等待队列** | **sub2api 领先** |
| 溢出保护 | `QuotaClamp` 饱和 + 审计字段 + 扣费前拒绝 | `saturatingMul` 饱和 + 配置校验钳制 | 相当；new-api 可观测性更好 |
| 幂等/重放 | 钱包退款不重试（注释显式说明） | 服务端可信 `request_id` 去重表 | sub2api 更健壮 |

## 4. 可落地建议（按优先级）

1. **（R2）给 Redis 故障期一条 DB 侧原子兜底。** 当前 `balanceReservation()` 拿不到可用预留能力时
   （Redis 报错）直接 `fail-open` 放行，并发护栏整层消失。最小改法：在 `deductBalanceToFloorSQL`
   同款的 SQL 基础上，新增一张"预留列"或直接复用结算路径的
   `UPDATE users SET ... WHERE id = ? AND balance - :reserve - :reserved >= :amount`
   条件更新（成功=放行，`RowsAffected==0`=拒绝），仅在该用户近封底/高并发并发场景启用，避免给所有
   请求加 DB 往返。new-api 的 `reserveUserQuotaDB` 就是可抄的样板。
2. **（R9）给余额缓存加 fence。** 抄 `cacheInitToken` + `invalidateTokenCacheForMutation`：
   - 已存在的余额键只刷 TTL，**不用回源快照覆盖**（`setBalanceCache` 改为 Lua：`EXISTS ? PEXPIRE : SET`）；
   - 余额变更（结算/加款）前后用短的 fence 键让并发读者手里的旧快照失效。
3. **（R1）把新口径的交叉点测出来并写进文档。** PR16 的测试只覆盖了 900KB 那一端（断言 `> 1_000_000`），
   没有覆盖 512B–1.2KB 这一"改动后反而更激进"的区间。建议补一条参数化用例，
   并明确写出取舍：**通用 `url`/`file_data` 键上的"真·媒体但父键不典型"会被按稠密计**。
4. **（R6）`/v1beta` Gemini、`/v1/embeddings`、Grok 媒体、异步图片、realtime 等入口**仍未接入最坏费用闸门+预留
   （文档 §6 已列）。优先级最高的应是把这些入口的 `CheckBillingEligibility` 调用统一升级为
   `recheck...` 同款的"带 worstSpend + slot"形式。
5. **（R5）把 `grok_media.go:267` 切到新函数**，至少在注释/文档里显式记录"该入口无预留槽位，
   故无需替换"，避免后来者以为改造已全局收口。
6. **（R3/R4）两个健壮性小修**：`-1` 分支改为"以本笔金额重建聚合"，Lua 写回用 `string.format('%.17g')`。
7. **（R7/R8）工程卫生**：`gofmt -w` 两文件；依赖升级拆到独立 PR（或至少在 PR 描述里单列并说明回归验证范围）。

## 5. 本轮新发现（v4 系列：**R**）

编号延续 v1=F、v2=H、v3=N、本轮=v4=**R**；每条标注「本区间引入」或「既有」。

### R1【中 · PR16 引入】通用 `url`/`file_data` 键在 512B–1.2KB 区间由保守反转为激进

- 证据（A/B 实测，同一探针跑在 `504473d6b` 与 `f7a45e28b`，高熵 base64，放在 `{"payload":{"url":…}}`）：

  | 原始 base64 字节 | PR16 前 (tokens) | PR16 后 (tokens) | 变化 |
  | --- | --- | --- | --- |
  | 300（<512，不扫描） | 227 | 227 | 0 |
  | **500** | **1 627** | **695** | **−932（−57%）** |
  | 512 | 1 627 | 711 | −916 |
  | 700 | 1 627 | 963 | −664 |
  | 1 000 | 1 627 | 1 363 | −264 |
  | 1 400 | 1 627 | 1 895 | +268 |
  | 20 000 | 1 627 | 26 695 | +25 068 |

- 机理：`requestSpendMinInlineBinaryRun = 512`（`gateway_request_spend_estimate.go:50`）以下不扫描；
  ≥512 且父键不是媒体白名单时，PR16 后按稠密口径（≈1 token/字节 + 固定开销 ≈ 字节数 + 200），
  而 PR16 前是**固定 1 块 allowance（≈1 627）**。两者在**原始 1.2KB 附近交叉**。
- 影响：若上游确实会把非典型父键下的 `url` 字段当媒体抓取并按图片计费（≈1 600+ token），
  则在 512B–1.2KB 区间预检**低估最多约 2.3×** —— 与 N4 报告里"高估 737×"恰是同一个判断的镜像方向。
  这正是评审规范里"改这里极易引入高估→误 403 或低估→write-off，**两个方向都要测**"的典型情形：
  PR16 只测了高熵大负载这一端。
- 缓解事实：严格实现 OpenAI/Anthropic 协议的上游会忽略未知字段（无成本 ⇒ 无坏账）；
  且 512–1200 字节的 URL 才落在该带内。因此严重度取**中**，与 N4 同级。
- 建议：把 `url`/`file_data` 的"通用父键"路径改为
  `max(稠密口径, 1 块 allowance)`，即在无法确证是文本时不让估算**低于**原保守值；
  或为这两个键单独保留"长串即媒体"的旧行为（那会重新打开 N4 的低估面，需权衡）。

### R2【中低 · 既有，PR16 未改变】Redis 不可用时预留 fail-open ⇒ 并发护栏整层消失，且该降级未在文档声明

- 证据：`reserveSpendWithGuard`（`billing_cache_service.go:1650-1657`）在
  `TryReserveUserBalance` 返回 err 时 `RecordBillingReservationFailOpen()` 并 `return nil`（放行）。
  即 Redis 故障期每笔请求都**不带任何预留**通过闸门，`balance − Σ预留 ≥ reserve` 这一核心不变量失效，
  只剩：准入阈值 → 近封底 DB 复核 → 耗尽标记 → 结算封底（有界 write-off）。
- 对照 new-api：同场景（`err != nil || result == cacheQuotaMiss`）降级为
  `reserveUserQuotaDB`（`UPDATE … WHERE id=? AND quota >= ?`），**不变量保持**，服务仍可用。
- 文档缺口：`docs/BILLING_ZERO_OVERSHOOT.md` §6"已知边界"列了 `userRepo` 未装配的降级、
  凭据过期残留、TTL 自愈等，**唯独没有"Redis 不可用时预留 fail-open"**这一条——而它是六条护栏里
  第③层整体失效的唯一场景。
- 建议：见 §4.1；至少在文档里显式声明，并把该指标纳入 `degraded_signals`。

### R3【低 · PR16 引入】`tryReserveBalanceScript` 的 `-1` 分支返回错误 → 走 fail-open

- 位置：`billing_cache.go:190-195`（`if existing ~= false` 且聚合键已消失 → `return {'-1','0'}`）
  + `:428-430`（Go 侧返回 `errors.New("reservation receipt exists without aggregate total")`）。
- 该错误在 `reserveSpendWithGuard` 里与"Redis 不可用"同路处理 ⇒ **无预留放行**（并打 ALERT）。
- 可达性：极低 —— 凭据与聚合的 TTL 始终同步刷新（`:188-190`、`:246-253`），`release` 仅在总额
  `<= 1e-07` 时删聚合（`:230-232`）；只有「另一笔极小金额（≤1e-7）的归还触发了 DEL，而本请求凭据
  尚存」或「聚合键被 maxmemory 淘汰」才会命中，且生产路径每次都会生成新的 `requestID`
  （`newBillingReservationRequestID`），使 `existing ~= false` 本身就近乎不可达。
- 方向问题：把它当成"服务不可用"（fail-open）不符合语义，它其实是"状态不一致"。
- 建议：改为 `accepted=true` 并 `SET` 聚合 = 本笔金额（重建），或 fail-closed 拒绝；
  无论哪种都建议单独计数，不要与 Redis 故障混用一个指标。

### R4【低/信息 · PR16 引入】Lua 手写浮点累加替代 `INCRBYFLOAT`，写回经 `tostring()` 格式化

- 位置：`billing_cache.go:197-202`。旧脚本用 `INCRBYFLOAT`（Redis 内部按 17 位有效数字处理），
  新脚本用 `tonumber(...) + tonumber(...)` 后 `SET tostring(newVal)`。Redis 的 Lua 数字→字符串
  走 `%.14g`，会把 `0.1+0.2 = 0.30000000000000004` 写成 `0.3`。
- 方向：聚合值可能略小于真实和（相对误差 ~1e-14），即**极轻微偏宽松**；在 USD 金额量级下无实际意义。
  新增单测只断言 `InDelta(…, 1e-9)`，覆盖不到该量级。
- 建议：`SET KEYS[1] (string.format('%.17g', newVal)) PX ARGV[3]`，与 `INCRBYFLOAT` 的精度对齐。
- （本机无 Redis，未实测该格式化行为，属**静态阅读 + 推理**结论，已计入 §8 未验证清单。）

### R5【低 · PR16 未收口】`grok_media.go:267` 仍用旧 `checkSelectedGroupRouteEligibility`

- 证据：全仓仅剩这一处调用旧函数（`group_route_failover.go:86-98`），它**不带** `WithMaxRequestSpend`
  **也不带**预留槽位。同族的 3 个 handler 已全部切到 `recheck...`。
- 影响：Grok 媒体在"选号失败后切路由"这条路径上不重估最坏费用。由于该入口的准入
  （`grok_media.go:184`）本身就**没有**最坏费用闸门、也**没有**建立预留槽位，所以"预留未替换"这一
  子项不成立；实际影响是"路由切换后仍无最坏费用闸门"，与文档 §6 已列的"Grok 平台图片未接入护栏"一致。
- 定性：**不是回归**，是"改造未全局收口 + 入口未覆盖"的叠加，属一致性/可维护性问题。

### R6【低 · PR16 引入】文档覆盖率声明未随 PR16 更新

- `docs/BILLING_ZERO_OVERSHOOT.md` §6 仍写着：
  "**Anthropic 提示过长后的兜底分组重试**（`gateway_handler.go` 的 fallback group 分支）：
  该笔按兜底分组的倍率结算，但准入只做基础阈值检查，没有最坏费用闸门"。
- PR16 恰恰修掉了这一条（`gateway_handler.go:1083-1093` 现在会按兜底分组重估 `worstSpend` 并替换预留）。
  §6 未同步删除/改写，导致"已修复"仍被声明为"未覆盖"。
- 建议：更新 §6，并把 §4.1 建议的 Gemini/embeddings/Grok 媒体/异步图片/realtime 等入口补齐优先级说明。

### R7【低 · 工程 · PR16 引入】`gofmt` 不合规文件（2 个）

- `gofmt -l internal/{service,repository,handler}` 输出：
  - `internal\repository\billing_cache_reservation_integration_test.go` ← **PR16 新增代码**，
    `TestTryReserveIsAtomicAndIdempotent` 的匿名 struct 字段未对齐（`requestID string` /
    `total    float64` 列宽不一致）。
  - `internal\handler\user_handler_test.go` ← **既有**（`7dc17e07b`，PR3 期引入，`DeductBalance`
    单行函数体不符合 gofmt 拆行规则），与 v3 报告 §"附带清理"里在 `internal/service`
    清掉的是**同一类问题**——上轮清理只覆盖了 `internal/service` 包，`internal/handler` 漏了。
- 建议：本轮 `gofmt -w` 两个文件；并把 `gofmt -l ./internal/...` 加进 CI，避免同类问题反复。

### R8【低 · 工程 · PR16 引入】依赖升级混入计费修复 PR

- `backend/go.mod`/`go.sum` 随 PR16 一起升级了 `golang.org/x/*` 9 个模块、`grpc`、`otel`、`genproto`。
  功能修复与依赖升级耦合后：出现回归难以二分定位、review 关注点被稀释。
- 建议：依赖升级单独开 PR（或至少在同一 PR 中由 CI 增加一份"仅依赖升级"的等价性说明）。

### R9【中 · 既有，建议单列 PR】余额缓存的回源写回可覆盖并发扣费（缺 new-api 式 fence）

- 证据：`billingCache.SetUserBalance`（`billing_cache.go:336-339`）是**无条件** `rdb.Set(key, balance, ttl)`；
  调用点 `billing_cache_service.go:1477` 用的是**几毫秒前**从 DB 读到的 `fresh`。
  若在这两个动作之间发生一次结算（`deductBalanceScript` 原子递减缓存 + DB `FOR UPDATE` 扣款），
  随后的 `Set` 会把**偏高的旧值**重新写回缓存。
- 影响链：缓存偏高 → `inflightReservationBudget`（`:1550-1560`）算出的 `maxTotal` 偏大 →
  并发准入放宽 → 直到下一次复检/失效/TTL 才纠正；结算侧仍有封底兜底，故为**有界**但在
  "贴底 + 突发"场景下与护栏目标相反。
- 参照实现：new-api 用两招解决同一问题 ——
  ① `cacheInitToken`：已存在的哈希**只刷 TTL**，绝不用 DB 快照覆盖（`quota_reserve_test.go:237-243`）；
  ② `invalidateTokenCacheForMutation` fence：变更期删除缓存并把并发读者手里的旧快照判为 code 0，
  fence 过期后才允许重新水合（`:245-251`）。
- 建议：见 §4.2。属既有问题（PR3–#16 区间未触碰该逻辑），**建议单列 PR**，
  不在本轮计费修复范围内顺带改动。

## 6. 前序结论（F1–F9 / H1–H12 / N1–N4）收敛复核

| 编号 | 状态 | 本轮依据 |
| --- | --- | --- |
| F1–F9 | ✅ 未被推翻 | 相关代码在 `504473d6b..f7a45e28b` 无改动 |
| H1–H12 | ✅ 未被推翻 | v3 已逐项实证；本轮 `go test -tags=unit ./internal/{service,repository,config,handler}/...` 全绿 |
| H10（配额按全额累加） | ✅ 维持"既有产品语义"判定 | 本区间无改动 |
| N1（Anthropic 透传 `x-client-request-id`） | ✅ 已修复且未回退 | `TestPassthroughAllowlistsExcludeClientRequestIDHeaders` 同包单测通过 |
| N2（冲突重试对账一致性） | ✅ 已修复 | `TestGatewayServiceRecordUsage_ConflictRetryAlignsUsageLogRequestID` 通过 |
| N3（幂等前提文档化） | ✅ 已写入 §6 | 本轮复核文档该节仍在，且 PR16 未改坏 |
| N4（多模态键名上下文） | ⚠️ **部分延续**：`data` 键方向已修；`url`/`file_data` 在 PR16 收紧，但**新增了 R1 的镜像风险** | 见 R1 |

## 7. 已核对成立的关键不变量（本区间）

- **预留账本的不变量**：`balance − Σ在途预留 ≥ reserve`；在"结算扣钱"与"归还预留"以任意先后顺序
  发生时都成立（文档 §2 声明，本轮代码路径与 PR16 改动一致）。
- **原子的"接受或拒绝"**：PR16 后被拒请求不写任何键（`tryReserveBalanceScript` 只在通过时
  `SET`），因此不存在"需要二次回滚的中间态"，也不存在"拒绝流量给泄漏续期"。
- **凭据优先归还**：`releaseBalanceScript` 的递减金额取自本请求凭据，凭据缺失一律 no-op，
  晚到归还不吃别人预留。
- **估算器整体线性**：`找串 → 回溯键名（1024 字节预算）→ 三分类`，PR16 未改变该结构；
  新增的 `precededByBase64Marker` 与父键回溯同样有界。
- **余额下限**：结算 `GREATEST(balance - amount, floor)`，余额不为负；差额计入 write-off 可观测。
- **配置跨层一致**：`request_spend_cjk_tokens_per_rune` 校验(0–8)/运行期钳制/文档三处一致（未改动）。
- **工作区干净**：探针文件已删除，两个工作树 `git status` 均无残留。

## 8. 未验证 / 无法验证

- **`-tags=integration`**：`TestTryReserveIsAtomicAndIdempotent` 的"8 并发恰好接受 3 笔"这一核心
  原子性断言**本机未执行**（需 testcontainers 真 Redis）；CI 报告为绿，但本轮未独立复现。
  这是本报告**最重要的未验证项**。
- **new-api 未运行任何测试**：§2 的内容全部来自静态阅读 + 其自带测试源码；`%.14g` 的 Lua 格式化
  行为（R4）本机无 Redis，未实测。
- **R1 的可达性**：需要"上游把通用 `url`/`file_data` 字段当媒体抓取并按图片计费"，
  与 N4 一样**未能枚举具体上游**；严格协议实现忽略未知字段。
- **R9 的窗口宽度**：需要"DB 复检读值"与"缓存 SET"之间恰好发生结算；未做并发时序实测。
- 前端 vitest/vue-tsc、golangci-lint、生产压测未运行。
- 区间内 `frontend/src/views/admin/SettingsView.vue` 等前端改动（`inflight_reservation_budget_multiplier`
  后台可调）本轮未做前端侧审计。

## 9. 建议处置顺序

1. **R2**（Redis 故障期 DB 原子兜底 + 文档声明）—— 唯一能让六层护栏整体失效的场景。
2. **R9**（余额缓存 fence）—— 与 R2 同源（都是"降级/竞态下护栏强度"），建议合并成一个"护栏强度补齐"PR。
3. **R1**（新口径交叉点补测 + 明确取舍）—— 决定 PR16 的多模态收紧是否会打开反向坏账通道。
4. **R6**（文档覆盖率同步）+ **R5**（grok_media 收口）—— 低成本，防止"以为已覆盖"。
5. **R3/R4**（-1 分支语义、Lua 精度）—— 健壮性小修，可与 3 合并。
6. **R7/R8**（gofmt + 依赖升级拆分）—— 工程卫生，随时可做。

---

## 附：本轮 A/B 实测数字（同一探针，两个提交）

高熵 base64（`raw[i] = byte(i*47+13)`）内联进 JSON，直接调用 `EstimateRequestInputTokensUpperBound`。

**按场景（原始 900 000 字节）**

| 场景 | PR16 前 | PR16 后 | 判定 |
| --- | --- | --- | --- |
| `{"content":"<b64>"}`（文本位基准） | 1 200 023 | 1 200 023 | 稠密（不变） |
| `{"payload":{"data":"<b64>"}}` | 1 200 027 | 1 200 027 | 稠密（N4 修复保持） |
| `{"content":[{"source":{…,"data":…}}]}`（Anthropic） | 1 654 | 1 654 | 1 块（不变） |
| `{"parts":[{"inline_data":{…}}]}`（Gemini） | 1 647 | 1 647 | 1 块（不变） |
| `{"input_audio":{"data":…}}`（OpenAI） | 1 637 | 1 637 | 1 块（不变） |
| **`{"payload":{"url":"<b64>"}}`** | **1 627** | **1 200 027** | 稠密（PR16 修复） |
| **`{"payload":{"file_data":"<b64>"}}`** | **1 630** | **1 200 030** | 稠密（PR16 修复） |
| `{"payload":{"url":"data:image/png;base64,<b64>"}}` | 1 638 | 1 638 | 1 块（标记兜底生效） |
| `{"image":{"url":"<b64>"}}` | 1 626 | 1 626 | 1 块 |
| `{"input":[{"image_url":"<b64>"}]}` | 1 630 | 1 630 | 1 块 |
| `{"input_file":{"file_data":"<b64>"}}` | 1 631 | 1 631 | 1 块 |
| `{"file":{"file_data":"<b64>"}}` | 1 628 | 1 628 | 1 块 |

**尺寸扫描（`{"payload":{"url":"<b64>"}}`，即 R1 的交叉点）**：见 §5 R1 表格
（500B: 1 627→695；1 000B: 1 627→1 363；1 400B: 1 627→1 895；20 000B: 1 627→26 695）。

**非 token 类断言**

| 断言 | 结果 |
| --- | --- |
| `go build ./...` | ✅ rc=0 |
| `go vet -tags=unit ./internal/...` | ✅ rc=0，无输出 |
| `go test -tags=unit …/{service,repository,config,handler}` | ✅ 全绿（193.3s / 5.0s / 4.2s / 39.8s） |
| `gofmt -l internal/{service,repository,handler}` | ❌ 2 个文件（R7） |
| 全仓 `checkSelectedGroupRouteEligibility` 调用点 | 仅 `grok_media.go:267`（R5） |
| `ReserveUserBalance`（旧非原子脚本）生产调用点 | 0（仅 `reserveSpendWithGuard` 的降级分支保留） |

---

## 10. 修复记录（R1/R3/R4/R5/R6/R7，2026-09-19）

分支 `fix/billing-audit-pr16-findings`（基线 `f7a45e28b`）。

### 10.1 已修复

| 编号 | 状态 | 改动 | 回归测试 |
| --- | --- | --- | --- |
| **R1** | ✅ | `gateway_request_spend_estimate.go`：抽出 `multimodalMediaParentKeys` 白名单表；新增 `multimodalPayloadKeyContextAmbiguous` 判定"通用键名 + 父键不足以确证媒体"的灰区；`inlineBinaryPayloadStats` 对灰区取 **`max(稠密, 1 块固定额度)`** —— 稠密费率是 1 token/字节，故"块额度不低于按字节折算"等价于 `len(payload) <= requestSpendImageTokenAllowance` 时按块计、否则回落稠密。两个方向都不低于任一单独口径 | `TestEstimateRequestInputTokensUpperBound_GenericMediaKeysNeverBelowBothFloors`（尺寸扫描 500/512/700/1000/1400/2000 + 长串必稠密 + 媒体父键不受影响） |
| **R3** | ✅ | `tryReserveBalanceScript`：`-1` 分支改为**以凭据金额重建聚合**（金额以凭据为准，保证后续 release 正确递减）；Go 侧只认 `'1'`/`'0'` 两种答复，其它值**返回 `accepted=false`**（走调用方 guard 的 fail-closed 判定）而不是返回 error（error 会走 fail-open 放行） | `TestTryReserveRebuildsAggregateWhenReceiptSurvives`（删聚合键保留凭据 → 必须接受且总额等于凭据金额） |
| **R4** | ✅ | 同脚本：`tostring()` 改为 `string.format('%.17g', …)`，与 release/renew 侧 `INCRBYFLOAT` 的 17 位有效数字口径对齐 | `TestTryReserveKeepsFullFloatPrecision`（3×0.1 的聚合必须逐位等于 Go 的 `0.1+0.1+0.1`） |
| **R5** | ✅ | `grok_media.go` 路由切换处改为先 `EstimateRequestSpendUpperBound` 再调 `recheckSelectedGroupRouteEligibility(..., worstSpend, nil)`（该入口无预留槽位，只补最坏费用闸门）；已无调用者的 `checkSelectedGroupRouteEligibility` 删除，避免 golangci-lint unused | 全仓 grep 确认调用点唯一，靠 `go build`/`go vet` 把关（该 handler 无轻量单测夹具） |
| **R6** | ✅ | `docs/BILLING_ZERO_OVERSHOOT.md` §6：把"兜底分组重试没有最坏费用闸门"移出未覆盖清单（并注明 PR#16 已修）；Grok 媒体条目改为准确描述"主链路无闸门、只有切换支路补了闸门"；新增两条已知边界（Redis 不可用 ⇒ 预留 fail-open；无法解释的脚本答复按拒绝处理）；多模态段落补上 `url`/`file_data` 的灰区 `max` 规则 | 文档，无测试 |
| **R7** | ✅ | `gofmt -w internal/{service,repository,handler}`；`find internal -name '*.go' \| xargs gofmt -l` 现已全空（含 PR#16 引入的 repository 集成测试与 PR#3 起的 `handler/user_handler_test.go`） | gofmt 全量复查 |
| **R2** | ✅ | **新增 DB 侧原子预留兜底**（对标 new-api `reserveUserQuotaDB`）：迁移 `238_billing_balance_reservations.sql` 建表 `(user_id, request_id, amount, expires_at)`；`billing_reservation_db.go` 用「事务 + `SELECT … FROM users FOR UPDATE` 锁行 → 清理本用户过期行（TTL 自愈）→ 汇总未过期预留 → `reserved+amount <= maxTotal` 判定 → 幂等 upsert」实现与 Redis 脚本同语义的准入；`billingCache` 的 `TryReserve`/`Release`/`Renew` 在 Redis 报错或报"凭据缺失"时二次走 DB（幂等，正常路径零额外开销）；新增 `NewBillingCacheWithDB` 并在 `wire_gen.go` 接入；新增两个指标 + 两条 degraded signal | 集成套件 `BillingReservationDBFallbackSuite`：Redis 全程不可用下 5 笔抢预算 0.30（单笔 0.10）**恰好 3 笔被接受**、归还后行数归零、过期残留被清理、过期行不被续期复活 |
| **R9** | ✅ | **余额缓存加"变动代号"防旧快照复活**（对标 new-api 的 `cacheInitToken` + `invalidateTokenCacheForMutation` fence）：`deductBalanceScript` 与新的 `invalidateBalanceScript` 在同一次脚本里 `INCR billing:balance_gen:<uid>`；新增 `BalanceGeneration` / `SetUserBalanceIfGeneration`（条件发布，代号不符则丢弃写回、不创建键）；服务层在**发起 DB 读取之前**取代号，回源写回与近封底复核写回都改走条件发布（可选能力 `balancePublishGuard`，未实现该能力的缓存自动退回无条件写回）。用代号而非时间戳以规避多实例时钟偏移 | `billing_cache_generation_test.go`（miniredis，本机可跑）：扣费后的旧快照被丢弃且缓存保持扣费后的值；`InvalidateUserBalance` 递增代号 |

### 10.2 R1 修复的实测前后对比

同一份伪随机高熵 base64，`{"payload":{"url":"<b64>"}}`：

| 原始字节 | PR#15（参照） | PR#16（缺陷） | R1 修复后 |
| --- | --- | --- | --- |
| 300（<512，不扫描） | 227 | 227 | 227 |
| 500 | 1 627 | 695 | **1 627** |
| 512 | 1 627 | 711 | **1 627** |
| 700 | 1 627 | 963 | **1 627** |
| 1 000 | 1 627 | 1 363 | **1 627** |
| 1 400 | 1 627 | 1 895 | 1 895（按稠密取 max） |
| 20 000 | 1 627 | 26 695 | 26 695（按稠密） |

`file_data` 列的数值为 1 630 / 698… / 1 630 / 26 698，同构。副作用检查：
`{"content":"<b64>"}`（纯文本位）与 `{"image":{"url":"<b64>"}}`（确诊媒体）
**逐位不变**（分别为 223/691…/26 691 与恒定 1 626），说明修复只影响灰区，未扩散。

### 10.3 未修复

| 编号 | 原因 |
| --- | --- |
| **R8** | 依赖升级已随 PR#16 合入 `main`，无法在后续 PR 里"拆开"，只能记录为流程建议 |

R2/R9 已在同一分支上落地（见 §10.1）。它们本来是"建议单列 PR"的两条，之所以并入本 PR：
两者都是**护栏强度**问题（而非口径修复），与 §4 的建议 1/2 完全对应，且 R9 的条件写回
需要与 R2 一起改 `billingCache` 的同一批方法，拆开反而增加合并成本。

R2 的**已知限制**（未修，属设计边界）：DB 兜底只支持余额模式（scope 为纯 userID）。
订阅模式的 scope 形如 `sub:<user>:<group>`，在该模式下 Redis 故障仍会退回 fail-open，
需要按"订阅限额"另做一套按窗口的 DB 预留 —— 已写入 `docs/BILLING_ZERO_OVERSHOOT.md` §6。

### 10.5 CI 失败与修复（2026-09-19）

本 PR 首次推送后 CI 的 `test` 作业失败（10m14s），失败点是**本 PR 新增测试自身的断言写错**，
实现本身正确：

- `TestTryReserveKeepsFullFloatPrecision` 断言 `want := 0.1 + 0.1 + 0.1`，期望得到
  `0.30000000000000004`。但 Go 的**无类型常量表达式**会被编译器按任意精度折叠：
  `0.1+0.1+0.1` 先算成精确十进制 0.3，再舍入到最近的 float64 → 得到 `0.3`。
  而实际值（从 Redis 读回的聚合）是 `0.30000000000000004` —— 这恰好**证明了 `%.17g` 修复生效**
  （若仍是 `tostring()` 的 `%.14g`，实际值会是被截断的 `0.3`，测试反而会"通过"）。
- 修复：把期望值改为**运行时累加**（循环 `want += 0.1`），并加一条前提断言
  `require.Equal(0.30000000000000004, want)` 锁住这个易踩的语义，防止后人"简化"回常量表达式。
- 其余包全部 `ok`，本次失败仅影响 `internal/repository`。

### 10.6 第二次 CI 失败：R2 的重构把既有语义改坏（2026-09-19）

第一次修复推送后 CI 再次失败，这次是**真实回归**，不是测试写错：

```
--- FAIL: TestBillingReservationSuite/TestReleaseOnMissingReceiptIsNoop
--- FAIL: TestBillingReservationSuite/TestReleaseReducesAndDeletesAtZero
--- FAIL: TestBillingReservationSuite/TestExpiredReceiptReleaseDoesNotEatOtherReservations
--- FAIL: TestBillingReservationSuite/TestRenewExtendsReceiptTTL
        Error: Expected error with "billing reservation already expired" in chain but got nil.
```

- **根因**：给归还/续期加 DB 兜底分支时，把原来的
  `if reply == 'EXPIRED' { return ErrBillingReservationExpired }` 重构成了
  `if redisUnavailable || reply == 'EXPIRED' { switch 兜底结果 { ... } }`，
  而兜底结果为 `NotApplicable`（本测试用 `NewBillingCache`，`db == nil`）时**没有回退语句**，
  直接落到函数末尾 `return nil` —— 丢了"凭据已随 TTL 回收"这个信号。
- **为什么本机没发现**：这 4 条契约**只被 integration 套件覆盖**（需要 Docker），
  本机跑不了，而 unit 套件里没有任何用例碰到归还/续期的"凭据缺失"分支。
- **修复**：把 `redisUnavailable` 与 `reply == 'EXPIRED'` 两个分支拆开，各自的 `default`
  都补上回退（前者返回 Redis 错误，后者返回 `ErrBillingReservationExpired`），
  与重构前的语义逐条对齐。
- **顺带补强（本轮的真正收获）**：新增 `billing_cache_reservation_contract_test.go`
  （miniredis，**无 build tag，本机可跑**），把这三条契约下沉为单元烟测：
  凭据缺失的归还/续期必须返回 `ErrBillingReservationExpired`；晚到的归还不得吃别人的预留；
  归还后聚合归零。**同类回归下次会在本机当场失败，而不是等 CI 10 分钟**。
  *（教训：把 integration-only 的契约下沉到本地能跑的单测，是这类"重构改坏既有语义"的唯一防线。）*

### 10.4 本轮验证

`go build ./...` ✅ / `go vet -tags=unit ./internal/...` ✅ / `go vet -tags=integration ./internal/repository/` ✅ /
`gofmt` 全量干净 ✅ / `go test -tags=unit -count=1 ./internal/{service,repository,config,handler}/...` ✅

**未运行**：`-tags=integration` 的实跑（新增的两个 R3/R4 用例需要真实 Redis，仅完成编译期校验）、
前端 vitest/vue-tsc、golangci-lint。
