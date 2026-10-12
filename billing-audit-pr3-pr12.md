# Sub2API 计费护栏复审报告（PR #3 → #12）

- 仓库：`/home/ziyue/dsh/sub2api-ui`，`main` @ `4e2a90d9f`（= PR #12 merge）
- 审计区间：`2c1982301..4e2a90d9f`，10 个 PR / 77 个文件（+7157 −251）
- 本轮性质：**复审**（含对作者自己 PR #12 的对抗性复核）。v1 报告（`billing-audit-pr3-pr11.md`）在本仓库中，v2 为其超集。
- 实测手段：本机 Go 1.27.1，直接调用仓库内真实函数做定向探针（临时 `zz_*_test.go`，已全部删除，工作区干净）；`go build ./...`、`go vet -tags=unit|integration`、`go test -tags=unit ./internal/{service,repository,config,handler}/...` 全绿；前端 `vitest`/`vue-tsc`/`eslint` 全绿。
- 未执行：真 PG/Redis 集成套件（testcontainers）、生产实例压测。

## 0. 结论摘要

| 编号 | 严重度 | 来源 | 一句话 |
| --- | --- | --- | --- |
| H1 | 高 | **PR #12 引入** | 内联 base64 判定改为"扫描全部长串"后，`jsonKeyBefore` 反向回扫退化为 Θ(n²)：1.6MB 请求体实测 **6.19s** CPU，可被任意持 key 用户打成 DoS |
| H2 | 高 | **PR #12 修复不完整** | 熵闸门"≥40 种字符"漏判**文本的 base64**（实测 37 种），仍按 2 字节/token → **低估 1.84×**，v1-F2 声称关闭的 write-off 通道仍有缺口 |
| H3 | 高 | PR #9 引入，PR #12 未覆盖 | 带参数的 data URI（`data:image/svg+xml;charset=utf-8;base64,…`）无法回溯键名 → 大图按 1 token/字节 误判：1.2MB 实测 **1 200 072** vs 正确 1 663（750×）→ 贴底用户误 403 |
| H4 | 中 | 既有（PR #8 起） | 分段 base64（MIME/`\n` 换行、url-safe `-`/`_`）不被识别：文本位**低估 ~1.8×**，多模态位反过来**高估 ~120×** |
| H5 | 严重 | 既有（区间外引入） | 计费去重键取自客户端 `X-Client-Request-ID`：恒定 header 即可让后续请求"已服务但分文不收" |
| H6 | 中 | PR #3 引入 | 管理员**减**余额/调低余额时也清"钱包已耗尽"标记（`admin_user.go:550`、`user_service.go:1173`），护栏被错误解除 |
| H7 | 中 | PR #3/#4 引入 | 加钱但不清标记的路径：OAuth 首次绑定赠送余额（`auth_oauth_first_bind.go:81`）、退款回滚加分（`payment_refund.go:698`） |
| H8 | 低 | PR #12 引入 | `request_spend_cjk_tokens_per_rune` 无上界：1e6 → 6000 字估出 60 亿 token（全量 403）；`MaxInt64` 触发乘法溢出 → 闸门 fail-open |
| H9 | 低 | PR #12 | marker 路径用 `||` 绕过熵闸门，与函数注释"低熵长串留文本口径"自相矛盾 |
| H10 | 低 | PR #3/#4 | 结算封顶（只收到 collected）后，API key 配额/限流仍按**全额**累加，账实不一致 |
| H11 | 信息 | PR #12 | 扫描已无快路径：普通大 body 实测 **5.5ms/MB**（15MB → 82ms），叠加 H1 后需设上限 |
| H12 | 低 | PR #3 | legacy 兜底路径：`NewBalance` 为 nil 时不打耗尽标记也不发低余额通知；`deps.userRepo` 为 nil 会 panic |

**v1 发现（F1–F9）复核**：F1 ✅（`image_url` 已修，实测 1 000 051 → 1 651）、F3 ✅（开关已加，上界见 H8）、F4 ✅（三处接入，逐路径核对**无预留泄漏**）、F5 ✅、F6 ✅、F7 ✅、F8 ✅、F9 ✅；**F2 部分修复**（裸高熵 base64 已按稠密计，但 H2/H4 仍是缺口）。

## 1. 本轮新发现

### H1【高 · PR #12 引入】`jsonKeyBefore` 反向回扫退化为 Θ(n²) → CPU DoS

- 位置：`backend/internal/service/gateway_request_spend_estimate.go` 的 `inlineBinaryPayloadStats`（对每个 ≥512 字节的串调用 `jsonKeyBefore`）与 `jsonKeyBefore`（沿 `isDataURIChar` 反向回扫到值的起始引号）。
- 触发体：把 `"A"×512 + ";"` 重复 k 次放进任意 JSON 字符串值即可（全部是 data-URI 字符，无引号打断）。每个串的回扫都要跨越它前面的**所有**串。
- 实测（本机）：

  | k | body | 耗时 |
  | --- | --- | --- |
  | 400 | 205 KB | 85 ms |
  | 800 | 410 KB | 245 ms |
  | 1600 | 821 KB | 1.51 s |
  | 3200 | 1.64 MB | **6.19 s** |

  4× 体积 ≈ 4–16× 时间，明确二次方；线性外推 16MB ≈ 10 分钟 CPU。
- 方向：与计费无关，纯 CPU/可用性；任意持 key 用户并发少量请求即可打满 CPU。
- 旧实现为什么没有：marker 版先做 `bytes.Contains(body, "base64")`/`"data":` 快路径，该构造体不含标记 → 立即返回。
- 最小修复：把回扫长度钳到一个常量（JSON 键名 + data URI 前缀不会超过 ~512 字节），超出即按"未知键"处理（归稠密 = 保守方向）；或改成"先找到值的起始引号再读键名"的单向扫描。

### H2【高 · PR #12 修复不完整】熵闸门 ≥40 种字符漏判"文本的 base64"

- 位置：`looksLikeBase64Payload`（`requestSpendBase64DistinctBytes = 40`）。
- 实测（1.2MB base64，放在 text 字段）：

  | 被编码内容 | base64 字符种类 | 判定 | 估算 | 按 0.92 token/字节的真实值 |
  | --- | --- | --- | --- | --- |
  | 英文自然文本 | **37** | 文本 | 600 043（2.0 字节/token） | ≈1 104 000 → **低估 1.84×** |
  | JSON 文档 | 54 | 稠密 ✅ | 433 379 | — |
  | Go 源码 | 51 | 稠密 ✅ | 573 379 | — |
  | 中文文本 | 61 | 稠密 ✅ | 533 379 | — |
  | 全 0 数据 | 1 | 文本 ✅（BPE 压缩好，正确） | 400 043 | — |

- 影响：把"一份文档 base64 后粘进 prompt"这种常见用法重新推回 write-off 通道（贴底用户可故意用英文文本的 base64 让预检低估）。
- 最小修复：阈值降到 ~24（十六进制 16 种、`abab` 2 种仍排除），或对 ≥4096 字节的超长串直接按稠密计；同时把 `LowEntropyRunsStayText` 里那个"重复 14 种字符"的构造体换成真实低熵样本。

### H3【高 · PR #9 引入，PR #12 只修了一半】带参数的 data URI 被按字节稠密计

- 位置：`isDataURIChar` 不含 `=`，因此 `data:image/svg+xml;charset=utf-8;base64,…` 的反向回扫停在 `charset=`，`jsonKeyBefore` 返回 `""` → 不在 `multimodalPayloadKeys` → 归稠密（1 token/字节）。
- 实测（1.2MB blob，放在 `image_url.url` 下）：

  | data URI 形式 | binary | dense | 估算 |
  | --- | --- | --- | --- |
  | `data:image/png;base64,` | 1 200 000 | 0 | **1 663** ✅ |
  | `data:image/svg+xml;charset=utf-8;base64,` | 0 | 1 200 000 | **1 200 072** ❌ |
  | `data:image/png;name=a.png;base64,` | 0 | 1 200 000 | **1 200 068** ❌ |
  | `data:text/plain;charset=utf-8;base64,` | 0 | 1 200 000 | **1 200 070** ❌ |

- 影响：`;charset=utf-8;base64,` 是 SVG/text 内联数据的常见写法；误判后最坏费用放大 750×，余额只有几美元的用户带图即 403（PR #9 想解决的"天文上界"问题在这条路径上完全复现）。
- 为什么 PR #12 没修到：v1 只发现了"键名不在白名单"（`image_url`）这一种表现，没发现"键名回溯失败"这一整类。
- 最小修复：回扫不要依赖字符白名单 —— 反向找到最近的**未转义 `"`**（值的起始引号）后再向前读键名；或至少把 `=`、`%`、`&`、`~`、`'`、`(`、`)`、`*`、`!`、`$` 补进 `isDataURIChar`。

### H4【中 · 既有】分段 base64（换行 / url-safe）两个方向都错

- 位置：`isBase64Alphabet` 不含 `\n`、`-`、`_`，且 `<512` 的碎片在 `findBase64Runs` 阶段就被丢弃（marker 检查因此永不生效）。
- 实测（≈430KB/400KB 的同一份数据）：

  | 形态 | 位置 | 判定 | 估算 | 应有值 |
  | --- | --- | --- | --- | --- |
  | 标准 base64 连续 | text | 稠密 ✅ | 430 043 | ~396k（按 0.92/字节） |
  | `\n` 每 76 字符（MIME 风格） | text（带 `;base64,`） | 文本 ❌ | **220 712** | ~396k → **低估 1.8×** |
  | url-safe（`-`/`_`） | text | 文本 ❌ | **200 043** | ~368k → **低估 1.8×** |
  | url-safe + data URI | `url` 键 | 文本 ❌ | **200 063** | 1 663 → **高估 120×** |
  | 标准 base64 + data URI | `url` 键 | binary ✅ | 1 663 | — |

- 结论：同一个"碎片化"根因，在文本位造成坏账、在多模态位造成误 403。
- 最小修复：把 `\n`、`\r`、`-`、`_`、`\/` 视为**串内分隔符**（先合并相邻碎片再套用 ≥512 + 熵/marker 判定），或对 marker 之后的整段做一次"去分隔符 + 长度/熵"判定。

### H5【严重 · 既有，非本区间引入】客户端可控的去重键 = 免费用量

- 链路：`internal/server/middleware/client_request_id.go`（读 `X-Client-Request-ID`，仅做长度/字符规范化）→ `resolveUsageBillingRequestID`（`gateway_usage_billing.go:278` 返回 `"client:"+id`，且优先于上游 id）→ `usage_billing_dedup` 主键 `(request_id, api_key_id)`（`usage_billing_repo.go:75-95`）→ `gateway_usage_billing.go:423-426`：命中同指纹去重时 `return false, nil`（**不扣费、不报错**，随后仍写 usage_log 全额）；指纹不一致时 `ErrUsageBillingRequestConflict` → `ActualCost=0`（`openai_gateway_usage.go:507-520`）同样不扣费。
- 触发：客户端恒定发送同一个 `X-Client-Request-ID`。第 1 笔正常计费，其后每一笔都走"已存在"分支 —— 上游已经被调用、响应已经返回客户端，钱包分文未动。
- 位置：`backend/internal/service/gateway_usage_billing.go:269-289`、`backend/internal/repository/usage_billing_repo.go:75-110`。
- 影响：不限次数的免费调用（只受限流约束），且完全静默（usage_log 里看的到用量、看不到未扣费）。
- 最小修复：计费幂等键不能由客户端决定 —— 用 `hash(api_key_id ‖ client_id ‖ payload_hash ‖ 服务端单调计数)`，或冲突时改用服务端新生成的 request id 重新扣费，而不是丢弃这笔。
- 说明：该逻辑在 PR #3 之前就已存在（`084d26cbd` 的纯移动拆分带入），**不是本区间引入**；但 PR #3–#12 整条"零超发"护栏建立在这条计费路径之上，这个绕过口子会让所有护栏失去意义，因此列为最高优先级。

### H6【中 · PR #3 引入】减余额也会清"钱包已耗尽"标记

- `internal/service/admin_user.go:550-559`：不判断 `balanceDiff` 符号，一律调用 `InvalidateUserBalanceAfterCredit`（内部会 `ClearBalanceExhaustedMarker`）。
- `internal/service/user_service.go:1173-1178`：管理员 `UpdateBalance` 同样无条件清除。
- 触发：管理员从已耗尽用户扣掉 0.01 → 标记被清；此时预检只剩"余额阈值 + 缓存"，若缓存仍是偏高旧值，就会放行注定扣不到钱的请求（正是标记要堵的窗口）。
- 最小修复：仅当 `balanceDiff > 0` 才清标记（失效缓存可以照旧）。

### H7【中 · PR #3/#4 引入】加钱但不清标记的路径

- `internal/service/auth_oauth_first_bind.go:80-83`：首次绑定第三方赠送余额走裸 ent `AddBalance`，**既不清缓存也不清标记** → 用户绑定了 provider、拿到赠送余额，仍被 403 最多 6 分钟（标记 TTL）。
- `internal/service/payment_refund.go:697-706`：退款回滚把余额加回 `UpdateBalance(+BalanceToDeduct)`，`PaymentService` 未持有 `BillingCacheService`，同样不清标记；其镜像扣款路径还会把缓存留成偏高旧值。
- 最小修复：两处都走 `InvalidateUserBalanceAfterCredit`（或注入 billing cache 后调用）。

### H8【低 · PR #12 引入】CJK 费率无上界（含 int64 溢出 fail-open）

- `config.go` 只校验 `< 0`。实测：`request_spend_cjk_tokens_per_rune: 1000000` + 6000 字 CJK → 估算 **6 000 000 030** token（所有 CJK 请求对所有人 403）。
- 极端值 `math.MaxInt64` 时 `cjkRunes*cjkTokensPerRune` 溢出为负 → 输入上界为负 → 总费用 ≤0 → `estimateRequestSpendUpperBound` 走 `spend <= 0` 分支返回 0 → **闸门整体失效（fail-open）**。
- 最小修复：`Validate()` 限制 1..8（并对乘法做饱和/钳制）。

### H9【低 · PR #12】marker 路径绕过熵闸门，与注释矛盾

`if !precededByBase64Marker(...) && !looksLikeBase64Payload(...)`：`;base64,` + `abab…`（2 种字符）会被强制按 1 token/字节（高估 ~2×），而函数注释写的是"低熵长串留文本口径"。影响小（方向是保守），但注释与实现不一致，建议改成与文案一致或修正注释。

### H10【低 · PR #3/#4】结算封顶后配额按全额累加

`gateway_usage_billing.go:223-233` 用 `cost.ActualCost`（全额）更新 API key 配额与限流，而钱包只收到 `BalanceCollected`。账实不一致：用户被多算配额（对平台有利），但会让 quota 统计与 `usage_log.actual_cost` 对不上。最小修复：配额用 `collectedBalanceCost(p, result)`。

### H11【信息 · PR #12】扫描已无快路径

实测普通大 body：15MB → 82ms（5.5ms/MB）；17MB base64 体 → 60ms。去掉 `bytes.Contains` 快路径后每笔请求都要全量扫描一遍（另有 `countCJKText` 一遍）。建议：把 base64 扫描与 CJK 扫描合并为单趟，或保留"体积小于阈值直接跳过"的快路径。

### H12【低 · PR #3】legacy 兜底路径与统一路径的差异

- `gateway_usage_billing.go:192-197`：走普通 `DeductBalance`（无 `balanceFloorDeductor`）时 `result.NewBalance` 恒为 nil → `settlementReachedWalletFloor` 恒 false → 即使余额正好落在封底也不打标记（缓存已失效，影响有限）。
- 同文件 `:262-266` 注释明确不调用 `finalizePostUsageBilling` → legacy 模式下低余额/配额通知不触发。
- `:193` `deps.userRepo` 为 nil 会 panic（降级/测试装配）。

## 2. v1 发现（F1–F9）复核结论

| 编号 | 复核方式 | 结论 |
| --- | --- | --- |
| F1 `image_url` | 实测 1MB 内联图：字符串形态 **1 651**、对象形态 1 663 | ✅ 已修 |
| F2 裸 base64 | 裸高熵 base64 实测 1 000 043（稠密）✅；但 H2/H4 证明仍有 1.8× 低估缺口 | ⚠️ 部分修复 |
| F3 CJK 可配 | `requestSpendCJKRunesPerTokenValue` 0→1、2→2、负→1；`Validate` 拒绝负数 | ✅ 已修（上界见 H8） |
| F4 三入口接入 | 逐路径核对 Gemini/embeddings/failover 的每个 return：defer 覆盖、提交点唯一、failover 不挂槽位（`reserveSpendWithGuard` 对 `slot==nil` no-op） | ✅ 无预留泄漏 |
| F5 前端守卫 | `SettingsView.spec.ts` 38/38（含新用例）、`vue-tsc`、`eslint` 全绿 | ✅ 已修 |
| F6 NaN/±Inf | `.nan/.inf/-.inf` 三种均在 `Validate` 被拒（实测 `TestLoadRejectsNonFiniteBillingValues`） | ✅ 已修 |
| F7 文档 | 文档/示例配置已补 CJK 费率、预算倍数、reserve 迁移、未覆盖入口 | ✅ 已修（H3/H4 的口径需再补） |
| F8 后台开关即时生效 | `refreshCachedSettings` → `InvalidateInflightReservationBudgetCache`(+Forget)，且该点在 DB 提交之后 | ✅ 已修 |
| F9 nil 顺序 | `EstimateImageRequestSpendUpperBound` 先判 `s == nil` | ✅ 已修 |
| F10 batch hold capture | 仍缺批次凭据校验（区间外既有） | ❌ 未处理（建议单列） |

## 3. 已核对成立的关键不变量

- 结算 SQL：`FOR UPDATE` + `balance > floor` + `GREATEST(balance-amount, floor)`，`collected/shortfall` 由前后余额差量化，余额不可能为负（`usage_billing_repo.go:265-357`）。
- 预留凭据化：过期凭据归还整体 no-op；聚合键 TTL 不被后续预留续期；心跳续期两把键；槽位状态机幂等且用脱离取消的 ctx 归还（`repository/billing_cache.go`、`billing_cache_service.go:339-388`）。
- 7 个接入点 + 本轮新增 3 个入口：归还责任在 drop/关停/正常/早退路径上都闭合（逐路径核对）。
- 耗尽标记：只由"扣到底线/扣费被拒"写入，回源路径永不写；TTL = 缓存 TTL + 1min；`redeem`/`promo`/`affiliate`/管理员加钱会清（H6/H7 是例外）。
- 指标与端点：`GET /api/v1/admin/ops/billing-guard` 走 adminAuth、只读原子计数、无 label → 无基数放大。
- 前端 guard、i18n、示例配置与文档的一致性（除 H1–H4 的口径描述）。

## 4. 建议处置顺序

1. **H5**（免费调用）：计费幂等键改服务端生成，或冲突时重新开一条扣费 —— 这是唯一"能让所有护栏失效"的口子。
2. **H1**（CPU DoS）：限制 `jsonKeyBefore` 回扫长度 —— 一行常量即可，属 PR #12 回归，建议立即修。
3. **H2 + H4 + H3**（折算口径）：熵阈值下调、分隔符合并、data URI 参数回溯 —— 三条都会直接产生坏账或误 403。
4. **H6 + H7**（标记生命周期）：加钱才清标记；补齐 OAuth 首绑与退款回滚。
5. **H8 + H9 + H10 + H12**：配置上界、注释一致、配额按实收、legacy 路径补齐。
6. **H11**：扫描成本优化（合并单趟）。

## 5. 未验证 / 无法验证

- 真 PG/Redis 集成套件与生产压测未执行（H1 的绝对耗时随 CPU 而异，量级已验证）。
- H5 在生产是否已被利用、`RequestPayloadHash` 是否在所有路由都被填充（若为空，指纹退化为常量，冲突分支反而更少触发，但"同请求重复投递"仍免费）。
- H6/H7 的触发频次（依赖管理员/绑定/退款的实际使用）。

---

## 6. 修复记录（PR #14，`fix/billing-audit-pr12-findings`）

| 编号 | 状态 | 修复要点 |
| --- | --- | --- |
| H1 | ✅ | `jsonKeyBefore` 改为"反向找值的起始引号 + 1024 字节预算"：1.64MB 构造体 **6.19s → 11ms**（6.6MB → 49ms，近似线性） |
| H2 | ✅ | 熵阈值 `40 → 24`（英文文本的 base64 实测 37 种字符）；十六进制/重复串仍留文本口径 |
| H3 | ✅ | 键名回溯不再依赖 URI 字符白名单；`;charset=utf-8;base64,` 等四种形态实测均回到 1 块 × 1600（原 1 200 072） |
| H4 | ✅ | MIME 换行 / 转义 `\/` / url-safe `-`·`_` 按同一条串合并统计 |
| H5 | ✅ | 幂等键只取服务端可信来源；指纹不再用客户端 id 兜底；冲突时换新 id 重新落账（不再丢单） |
| H6 | ✅ | 仅 `balanceDiff > 0` 才清"钱包已耗尽"标记 |
| H7 | ✅ | OAuth 首绑赠送、退款回滚加分统一走 `InvalidateUserBalanceAfterCredit` |
| H8 | ✅ | CJK 费率校验 0–8 + 运行期钳制 + 饱和乘法（防全量 403 与溢出 fail-open） |
| H9 | ✅ | 注释/文档与实现对齐（标记命中即稠密，熵闸门只用于无标记长串） |
| H10 | ⚠️ 判定为**既有设计**（已还原） | 曾改为"按实收累加"，但仓库自带集成测试 `TestUsageBillingRepositoryApply_DrainsWalletToReserveFloor` 明确锁定 `quota_used == 全额`（断言文案 "quota reflects the real consumption"）：配额/限流衡量"这笔请求真实消耗了多少"，与钱包能否全额收回无关（实收/坏账由 `balance_collected`/`shortfall` 表达）。属产品语义选择而非缺陷，已还原并在代码注释中记录结论。 |
| H11 | ✅ | 小 body 快路径 + 文档写明扫描成本（≈5ms/MB） |
| H12 | ✅ | legacy 路径回填 `NewBalance`、`userRepo` nil 不再 panic |
| F10 | ✅ | batch image capture 补批次凭据校验（与 release 对称） |

新增回归测试：`DataURIWithMimeParameters`、`SegmentedBase64`、`BacktrackIsLinear`、
`RunesPerTokenValue_Clamped`、`MarkerLifecycle_OnlyCreditClearsMarker`、
`QuotaUsesCollectedAmountOnFloorDrain`、`SkipsWhenHoldNeverReserved`；并把 5 个
**锁定旧（不安全）行为**的用例改写为锁定新不变量
（`PrefersClientRequestIDOverUpstreamRequestID` → `NeverUsesClientRequestIDAsBillingKey` 等）。

验证：`go build ./...`、`go vet -tags=unit ./internal/...`、`go vet -tags=integration`、
`go test -tags=unit ./internal/{service,repository,config,handler}/...` 全绿；CI 的
Integration 作业第一轮因 H10 与既有设计冲突而失败，还原后复跑中。

**仍待运行时验证**：真 PG/Redis 集成套件（testcontainers）、生产压测、H5 在生产是否已被利用。
