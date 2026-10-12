# Sub2API 计费护栏审计报告（PR #3 → #11）

- 仓库：`/home/ziyue/dsh/sub2api-ui`，`main` @ `33f992205`（= PR #11 merge）
- 审计区间：`2c1982301..33f992205`，9 个 PR / 14 个非 merge 提交 / 74 个文件（+6500 −246）
- 审计对象：后付费「零超发」护栏链——① 余额封底 + DB 真值复核带、② 放行前最坏费用预检、
  ③ 在途预留（并发准入）、④ 结算扣到封底 + 差额记账，以及配套配置/后台开关/指标/文档
- 验证手段（全部在本机实跑）：
  - `go build ./...` ✅（go1.27.1）
  - `go vet -tags=unit ./internal/service/` ✅、`go vet -tags=integration ./internal/repository/... ./internal/service/...` ✅（集成套件可编译）
  - `go test ./internal/{service,repository,config,handler}/...`（无 tag）✅ 全 ok
  - `go test -tags=unit -count=1 ./internal/service/... ./internal/repository/... ./internal/config/... ./internal/handler/...` ✅ 全 ok（service 193.7s / handler 39.8s / repository 4.6s / config 1.1s）
  - 两处定向探针（临时测试文件，已删除、工作区干净）：内联 base64 折算口径、`billing.minimum_balance_reserve: .nan` 校验行为
  - **未运行**：`-tags=integration`（testcontainers 真 PG/Redis 套件）、前端 vitest、生产实例压测

## 1. 逐 PR 结论

| PR | 提交 | 内容 | 结论 |
| --- | --- | --- | --- |
| #3 | `7dc17e07b` `a4ff0f28b` `4f1b7d4b4` `1a569520a` | 恢复 0.1 保留金底线 / 403 fail-closed、结算扣到封底、钱包耗尽标记、DB 复核带可配 | 逻辑成立；配置校验与文档有缺口（F6/F7） |
| #4 | `c8f621005` | 放行前最坏费用预检（WithMaxRequestSpend） | 方向正确；接入入口不全（F4） |
| #5 | `2a63c3e84` | 输出上界下限钳制 + 结算坏账指标 | 成立（默认值 0 = 开箱不生效，已在 #8 说明） |
| #6 | `78b04728f` | 在途预留（并发准入原子化） | 成立；TTL 语义有取舍，已在 #10 收口 |
| #7 | `8393cc352` | 预检复核 singleflight + 连接池默认对齐 | 成立 |
| #8 | `c4cc6f6f0` | 可观测性、凭据化预留、订阅/图片接入、降级计数 | 成立；但存在文档未登记的漏接入口（F4） |
| #9 | `202d03c68` | base64 按「所在位置」折算 | **引入新回归 F1；原坏账通道仍未关闭 F2** |
| #10 | `dfc06cb9a` | 预留 TTL 泄漏修复 + 并发预算倍数后台开关 | 成立（严格模式实测零坏账）；开关的"立即生效"函数是死代码（F8） |
| #11 | `e7f158105` | 输入上界按 CJK 内容感知折算 | 对当前上游（deepseek-v4-flash）有效；1 token/字 不是通用最坏上界（F3） |

## 2. 发现

### F1【高 · PR9 回归】多模态 data URI 落在非白名单键（尤其 Responses 的字符串形态 `"image_url":"data:..."`）被按 1 token/字节稠密计

- 证据：`backend/internal/service/gateway_request_spend_estimate.go:328-332` 的多模态负载键白名单只有
  `{url, data, file_data}`；`:309-314` 用「负载前最近 JSON 键名」二选一：命中白名单 → 1600 token/块，
  否则 → `1 token/字节`（`requestSpendDenseTokensPerByte`）。
- 实测（调用仓库内真实函数）：

  | 请求体形态 | 1 MB base64 的估算输入 token |
  | --- | --- |
  | `{"type":"input_image","image_url":"data:image/png;base64,…"}`（Responses 字符串形态） | **1 000 051**（dense=1 000 000, blobs=0） |
  | `{"type":"image_url","image_url":{"url":"data:…"}}`（Chat 对象形态） | 1 663 |
  | Anthropic `source.data` / Gemini `inline_data.data` | 1 669 / 1 655 |

- 这是**回归**：PR9 之前（`git show c4cc6f6f0:backend/internal/service/gateway_request_spend_estimate.go`）
  的实现只按 `;base64,`/`"data":"` 标记识别，任何长串都算二进制 → 同一请求 1 663 token。
- 可达性：`/v1/responses` 的 `input_image.image_url` 在 OpenAI 协议里就是**字符串**（本仓库自身的
  转换测试用的正是该形态，如 `backend/internal/service/openai_codex_transform_test.go:1108`、
  `image_generation_intent_grok_test.go:54`），而预检用的就是客户端原始 body
  （`openai_gateway_handler.go:414` 读取、`:601-608` 送入 `EstimateRequestSpendUpperBound`）。
- 影响：一张 1 MB 内联图会把最坏费用估成「百万输入 token × 单价 × 分组倍率」——余额只有几美元的用户
  会被 403 误伤；严格预留模式下这一笔就吃掉全部并发额度；`request_spend_safety_multiplier` 默认 1.0 无余量。
- 最小修复：把 `image_url`（以及 `image`、`inline_data` 等承载 data URI 的键）加入
  `multimodalPayloadKeys`，或直接按「值是否为 data URI」判定而非按键名；并补一条字符串形态的回归测试。

### F2【高 · PR9 修复不完整】裸 base64 粘在 text 里（无标记）仍被低估约 1.84×

- 证据：`gateway_request_spend_estimate.go:283-292` 只认三个字面标记 `;base64,` / `"data":"` / `"data": "`；
  没有 `data:` 前缀、也不在 `"data"` 键下的裸 base64 长串（`:302-315`）完全不被识别。
- 实测：1 000 000 字节裸 base64 放进 `text` → `binary=0, dense=0`，估算 **500 043** token；
  按仓库自己实测的 0.92 token/字节，真实约 **920 000** token → 低估 1.84×。
- 现有回归测试只覆盖带 `data:image/png;base64,` 前缀的变体（`billing_guard_metrics_test.go:269-277`），
  因此这正是 PR9 声称已关闭的 write-off 通道，仍然敞开：贴底用户可用裸 base64 让预检低估，
  结算再扣到封底 → 差额写成坏账（与 commit 里 `id=456` 那笔同类）。
- 最小修复：不依赖标记，扫描 ≥512 字节的 base64 字母表长串（代码里已有 `isBase64Alphabet`），
  命中即按稠密费率计；`requestSpendMinInlineBinaryRun` 已定义可直接复用。

### F3【中】CJK「1 token/字」是单上游校准值，不是通用最坏上界

- 证据：`gateway_request_spend_estimate.go:252-256`（`requestSpendCJKRunesPerToken = 1`），
  注释宣称「最坏类别保证」，但依据只有生产实例 166.1.232.118（deepseek-v4-flash）的 0.5–1 token/字观测。
- 风险：字节回退型分词器（cl100k/o200k 生僻汉字、Llama 系小 CJK 词表）对非常用汉字可达 2–3 token/字；
  旧口径 2 字节/token = 1.5 token/字（低估 1.3–2×），新口径 1 token/字（低估 2–3×）——**低估倍数被放大 1.5–2×**。
  网关是多上游（OpenAI/Anthropic/Gemini/Grok）共用同一估算器，模型维度并无区分。
- 最小修复：把每字 token 率做成配置项（或按模型/分词器族选取），默认对未知模型保守取 2；文档同步改为
  「已校准上游的上界」而非「通用最坏保证」。

### F4【中】护栏覆盖面与文档不符：Gemini 文本、OpenAI embeddings、failover 兜底未接入

- 证据：`CheckBillingEligibility` 共 18 个调用点，只有 7 个传了 `WithMaxRequestSpend`/`WithBalanceReservation`
  （`gateway_handler.go:265`、`gateway_handler_responses.go:155`、`gateway_handler_chat_completions.go:146`、
  `openai_gateway_handler.go:608/1367`、`openai_chat_completions.go:144`、`openai_images.go:151`）。
- 其中**按余额计费**却完全没有 ②③ 层护栏的文本入口：
  - Gemini `/v1beta`（`gemini_v1beta_handler.go:325` 无 opts，`:659` 走 `RecordUsage` 计费）
  - OpenAI embeddings（`openai_embeddings.go:101` 无 opts，`:267` 走 `RecordUsage`）
  - Anthropic 提示过长后的兜底分组重试（`gateway_handler.go:1084`，无 opts）——该笔按兜底分组倍率计费，
    预留金额仍是主分组的估计
- `docs/BILLING_ZERO_OVERSHOOT.md:137-160` 的「未覆盖入口」清单列了异步图片、Grok 图片、实时语音、
  WebSocket、视频，但**没有**列上面这三条；因此文档给出的「零超发」范围比实际更乐观。
- 最小修复：给这三处补 opts（Gemini/embeddings 的 body 已在手），或在文档中明确登记为未覆盖。

### F5【中】后台清空新的数字输入框会导致整次设置保存 400

- 证据：`frontend/src/views/admin/SettingsView.vue:3914-3918` 对
  `inflight_reservation_budget_multiplier` 使用 `v-model.number` + `type="number"`；清空后该值为空串 `""`，
  而保存 payload（`:11366-11367`）**原样透传**；Go 侧 DTO 是 `float64`
  （`backend/internal/handler/dto/settings.go:179`），空串 → `json: cannot unmarshal string into … float64`
  → 整个 `PUT /api/v1/admin/settings` 400。
- 同文件相邻字段都有守卫且带同因注释（`:11344-11347` `audit_log_retention_days: Number.isFinite(...) ? … : 180`，
  `:11355-11362` 的 `Math.min/Math.max` 系列），本条是漏网；另外前端不钳制上界，后端对 `<1` 静默归一为 1.0
  （`setting_handler_update.go` 合并逻辑），管理员填 0.5 会得到 1.0 且无反馈。
- 最小修复：`inflight_reservation_budget_multiplier: Number.isFinite(form.inflight_reservation_budget_multiplier) ? Math.max(1, form.inflight_reservation_budget_multiplier) : 1`。

### F6【中低】BillingConfig 校验只查 `< 0`，NaN/±Inf 可绕过并静默改变护栏方向

- 证据：`backend/internal/config/config.go:3200-3217` 全是 `< 0` 判断。本机实测（临时探针，已删）：
  `billing.minimum_balance_reserve: .nan` + `billing.inflight_reservation_budget_multiplier: .inf`
  → `Validate()` 返回 `nil`，`reserve=NaN`、`budget=+Inf`（`NaN<0 == false`、`Inf<1 == false`）。
- 后果（Go 侧逐条推演 + 实测值）：
  - `reserve=NaN`：`minimumBalanceReserve()` 的 `<= 0` 不拦（`billing_cache_service.go:1253`）；
    `balanceBelowEligibilityThreshold` 的 `minimumReserve > 0` 为 false、`balance <= NaN` 恒 false
    （`:1364-1373`）→ **第 ① 层封底判断静默失效**；`required=NaN`、`balance < NaN` 恒 false（`:1417-1432`）
    → **第 ② 层最坏费用闸门静默失效**；`balanceNearEligibilityThreshold` 阈值 NaN → 连 DB 复核也不再触发；
    第 ③ 层回退判据 `balance-reservedAfter >= reserve` 对 NaN 恒 false → 有预留能力时**全量 403**；
    结算 SQL `WHERE balance > $3` 在 PG 中 NaN 视为最大 → 不匹配任何行 → 所有结算返回
    `ErrInsufficientBalance`。三种后果（静默放开 / 全量 403 / 结算全失败）都无日志、无计数。
  - `budget=+Inf`：`spendable * +Inf = +Inf` → `reservedAfter <= +Inf` 恒 true（`:1583-1588`）
    → 在途预留闸门**永久放开**（等价于把倍数设成 ∞，绕过文档承诺的「<1 回退 1.0」零值安全语义）。
- 说明：JSON 管理接口无法构造 NaN/Inf（已确认 `""`/`1e999` 都会反序列化失败），因此只影响
  config 文件 / 环境变量（`BILLING_MINIMUM_BALANCE_RESERVE=NaN` 这类模板事故）。
- 最小修复：校验里显式拒绝非有限值（`math.IsNaN/IsInf`），或在 `minimumBalanceReserve()` 等取值处加同一守卫。

### F7【低】文档/注释漂移

- `docs/BILLING_ZERO_OVERSHOOT.md:39-52` 的配置清单**完全没有** `billing.inflight_reservation_budget_multiplier`
  （也没有它可被 `/admin/settings` 覆盖、后台优先于静态配置），而第 ③ 层与 `reject_inflight_reservation` 指标
  都依赖它——这是「用坏账换并发」的唯一开关，却不在运维手册里。
- 同文档 §5 升级注意事项未提 `minimum_balance_reserve` 默认值 `0.000001 → 0.1` 的行为变更与配置迁移
  （迁移代码 `config.go:1906-1909`，仅当文件里显式写了 `0.000001` 才改写）。
- `:167` 的「输入 token 按**三段口径**估计 —— 普通文本 `字节数/2`」未随 PR11 更新（现在是四段，CJK 走 1 token/字）。
- `backend/internal/service/billing_guard_metrics.go:20` 注释里的端点路径 `/admin/ops/billing-guard/health`
  不存在，实际是 `GET /api/v1/admin/ops/billing-guard`（`internal/server/routes/admin.go:266`）。

### F8【低】死代码与由此产生的覆盖空洞

- `setting_gateway_runtime.go:807-812` `InvalidateInflightReservationBudgetCache` **零调用者**（全仓 grep 仅定义处），
  与其注释「让后台设置保存后立即生效」矛盾：改倍数最多 60s 后才生效（`inflightReservationBudgetCacheTTL`）。
- `billing_cache_service.go:702-703` `QueueDeductBalance` 在改动后**无生产调用者**（仅 `billing_cache_service_test.go:106`），
  因为 `syncBalanceCacheAfterDeduction` 已改为「一律失效缓存」；同时
  `billing_cache_service_balance_test.go` 里原先断言异步扣减路径的 `…_QueuesDeductWhenBalanceStillEligible`
  被替换（新断言 `deductCalls == 0`），`cacheWriteDeductBalance` worker 分支现在没有测试覆盖。
- 最小修复：删除死代码或接上失效调用；为仍保留的分支补测。

### F9【低】`EstimateImageRequestSpendUpperBound` 的 nil 判定顺序反了

- `gateway_request_spend_estimate.go:596` 先解引用 `s.cfg`，`:600` 才判 `s == nil`；同文件的 token 版本
  （`:537-540`）是先判 nil 的。当前所有调用点都传非 nil 服务，属防御性缺陷（nil 接收者会 panic 而非返回 0）。

### F10【信息 · 既有问题，不在本区间】batch image hold 的 capture 路径缺少批次凭据校验

- `internal/repository/usage_billing_repo.go:441-471` 的 capture 只校验用户级
  `COALESCE(frozen_balance,0) >= $1`，不像 release 路径（`:479` `batchImageHoldClaimExists`）那样校验
  「该 batch 确实冻结过」；同一用户的 A 批次 capture 可以吃掉 B 批次的冻结额。
- 该函数在 `2c1982301..HEAD` 内**未改动**（已比对旧版本），属既往问题，登记备查。

## 3. 已核对成立的关键性质

- 结算封底：`deductBalanceToFloorSQL`（`usage_billing_repo.go:284-299`）在单语句内 `FOR UPDATE` 锁行 +
  `balance > floor` 条件 + `GREATEST(balance - amount, floor)` 夹紧，`settleBalanceDeduction`（`:339-357`）
  用前后余额差算 collected/shortfall 并做金额量化，浮点噪声不会造出假坏账；legacy 路径
  `user_repo.DeductBalance`（`user_repo.go:868+`）已从「允许透支成负数」改为 `balance >= amount + reserve`
  否则 `ErrInsufficientBalance`，`DeductBalanceToFloor` 与统一路径共用同一 SQL。余额不可能为负。
- 预留凭据化：reserve 用 `SET NX PX` + `INCRBYFLOAT`（幂等、十进制文本往返），release 仅在**本请求凭据存在**时
  按其金额递减（过期凭据整体 no-op 返回 `ErrBillingReservationExpired`），聚合键 TTL 只在新建/无 TTL 时设置、
  不被后续预留续期（泄漏可自愈），心跳（TTL/3）续期两把键、过期即停；`BillingReservationSlot` 状态机
  （idle→held→settling→released）幂等、归还走脱离取消的独立 ctx（`billing_cache_service.go:339-388`）。
- 预留生命周期在 handler 侧闭合：7 个挂槽位的入口都在资格检查成功后 `defer ReleaseOnExit`；所有计费提交都走
  `*WithReservation` 包装，`submitUsageRecordTaskTracked` 对显式 drop/sample 丢弃立即 `Release`，
  `DroppedStopped`（关停窗口）降级为内联同步执行（`gateway_handler.go:2643-2712`、`openai_gateway_handler.go:3500-3541`）。
- 耗尽标记闭环：结算 `shortfall > 0` 或 `NewBalance <= reserve + 1e-9` 即打标（`gateway_usage_billing.go:591-603`），
  预检先查标记 fail-closed 并顺手失效余额缓存（`billing_cache_service.go:1389-1396`），
  所有加钱路径清标记（`user_service.go:1177`、`redeem_service.go:579`、`promo_service.go:158`、
  `affiliate_service.go:486`、`admin_user.go:556` 及 `ClearBalanceExhaustedMarker`）。
- 运维端点 `GET /api/v1/admin/ops/billing-guard` 挂在 `admin` 组、经 `adminAuth`（`routes/admin.go:26-30,198,266`），
  只读进程内原子计数、不查库；所有计数器**无 label**、且全仓未注册 Prometheus 采集器 → 无基数放大/重复注册风险。
- 配置跨层一致：`mapstructure` tag ↔ `SettingKeyInflightReservationBudgetMultiplier` ↔ 解析/写回 ↔
  DTO JSON tag ↔ TS 类型/表单字段 ↔ en/zh i18n 全部对得上；`deploy/config.example.yaml` 可解析且与代码默认值一致
  （reserve 0.1、band 1.0、precheck false、default_max_output 8192、safety 1.0、min_output 0、pool 32/8）；
  仓库内已无遗留的 `0.000001` / `max_open_conns: 256` 引用（除迁移代码本身）。

## 4. 测试与 CI 观察

- 本区间新增/改写的测试大多带 `//go:build unit`，集成测试带 `//go:build integration`。
  CI 走 `make test-unit` / `make test-integration`（`.github/workflows/backend-ci.yml:39,42`），覆盖没问题；
  但本地惯用的 `make test`（= `go test ./...`）**两者都不跑**，容易得到「假绿」。
- 本机实跑：无 tag 与 `-tags=unit` 两轮全绿；`-tags=integration` 未运行（需 testcontainers/Docker），
  因此 Lua 脚本（INCRBYFLOAT/PTTL 语义）、并发累加、过期凭据隔离等集成断言在本环境**未复验**。
- 断言变更核查（无隐性放水）：
  - `TestDeductBalance_AllowsOverdraft` 删除、`TestDeductBalance_InsufficientFunds` 由「允许 −994 透支」
    反转为「拒绝 + 余额不变」、`TestCheckBillingEligibility_AllowsBalanceAtMinimumReserve` 反转为
    `RejectsBalanceAtMinimumReserve` —— 均为**跟随行为变更**的合理改写。
  - 唯一实质覆盖损失：`TestSyncBalanceCacheAfterDeduction_QueuesDeductWhenBalanceStillEligible` 被替换，
    异步扣减缓存路径自此无测试（见 F8）。
- 证据强度偏弱处：`gateway_request_spend_estimate_cjk_test.go` 里的「实测 6 字节/token」是测试内常量，
  且用 `len(body)/2` 重写「旧口径」而非调用旧实现，属同源自证；F1/F2 两类输入都**没有**测试覆盖。

## 5. 未验证 / 无法验证

- `-tags=integration` 的真 PG/Redis 套件（testcontainers）与前端 vitest 未执行。
- 生产价格/倍率取何值未核实，故 F1 的「误 403 余额阈值」只能给量级（百万 token × 单价 × 倍率）。
- 生产配置 `request_spend_min_output_tokens=8192`（见工作区 `billing-loadtest-retest-33f992205.md`）
  会额外放大约 1.1× 保守度，可能掩盖 F1/F2 的部分症状，但不改变结论。
- 异步图片、Grok 实时、视频等入口本报告未纳入（文档已登记为未覆盖）。

## 6. 建议修复顺序

1. **F1**（改白名单或按 data URI 判定）+ 回归测试 —— 影响面最大的功能回归。
2. **F2**（无标记 base64 稠密检测）+ 回归测试 —— 唯一仍敞开的 write-off 通道。
3. **F5**（前端守卫）与 **F6**（校验拒绝 NaN/Inf）—— 低成本、消除「一次误配打穿护栏」。
4. **F4**（Gemini/embeddings/failover 接入或登记）+ **F7**（文档补齐预算倍数开关、reserve 默认值迁移、CJK 口径）。
5. **F3**（CJK 费率可配/模型感知）、**F8/F9**（死代码与 nil 顺序）—— 技术债清理。

---

## 7. 修复记录（2026-09-14，基于最新 main `33f992205`）

`origin/main` 已核对：与本地 `HEAD` 相同（`33f992205`），无新提交，修复直接落在该基线上。

| 项 | 状态 | 改动 |
| --- | --- | --- |
| F1 | ✅ | `multimodalPayloadKeys` 增加 `image_url` |
| F2 | ✅ | 内联 base64 判定改为「扫描全部 base64 长串 + 键名分类 + 高熵闸门」，不再依赖 `;base64,`/`"data":"` 标记 |
| F3 | ✅ | 新增 `billing.request_spend_cjk_tokens_per_rune`（默认 0 = 内置 1；GPT/Llama 系建议 2） |
| F4 | ✅ | Gemini `/v1beta`、OpenAI embeddings、Anthropic 兜底分组三处接入预检/预留（兜底分组按兜底倍率重估且不重复挂槽位） |
| F5 | ✅ | `SettingsView.vue` 保存前 `Number.isFinite` 归一 + `Math.max(1, …)`，并补 Vitest 用例 |
| F6 | ✅ | `Validate()` 先拒绝 `NaN`/`±Inf`（4 个 billing 浮点项） |
| F7 | ✅ | 文档补 CJK 费率、预留预算倍数、reserve 默认值迁移、四段口径、未覆盖入口（Gemini/embeddings/failover）；示例配置同步；指标注释端点路径修正 |
| F8 | ✅ | `refreshCachedSettings` 调用 `InvalidateInflightReservationBudgetCache`（并 `Forget` singleflight 键） |
| F9 | ✅ | `EstimateImageRequestSpendUpperBound` 先判 `s == nil` |
| — | 新增测试 | `ResponsesStringImageURL`、`BareBase64InTextIsDense`、`LowEntropyRunsStayText`、`ShortBase64StaysText`、`RequestSpendCJKRunesPerTokenValue_Configurable`、`LoadRejectsNonFiniteBillingValues`、`LoadAcceptsCJKTokensPerRune`、SettingsView 空输入用例 |
| F10 | 未处理 | batch image hold capture 缺批次校验：**既有问题**（该文件不在 PR#3–#11 区间内改动），建议单独 PR |

验证：`go build ./...`、`go vet -tags=unit ./internal/...`、`go vet -tags=integration`、
`go test -tags=unit ./internal/{service,repository,config,handler}/...` 全绿；
前端 `node_modules` 未安装，Vitest 未在本机执行（新增用例依赖 CI）。

行为核对（直接调用仓库内函数，1MB 内联图）：

| 形态 | 修复前 | 修复后 |
| --- | --- | --- |
| Responses 字符串 `image_url` | 1 000 051 token（dense 误判） | 1 651（1 块 × 1600） |
| chat 对象 `image_url.url` | 1 663 | 1 663（不变） |
| Gemini `inline_data.data`（裸 base64） | 1 655 | 1 655（不变） |
| 裸 base64 粘进 text | 500 043（低估 1.84×） | 1 000 043（稠密） |
| `data:…;base64,` 粘进 text | 1 000 054 | 1 000 054（不变） |
| `abab…` 低熵长串 | 500 043 | 500 043（不变，避免高估） |
