# Sub2API 计费护栏审计报告（PR #3 → #14）

- 仓库：`ziyue67/sub2api-ui`，`main` @ `00ec75715`（= PR #14 merge，与本地 `HEAD` 一致）
- 审计区间：`2c1982301..00ec75715`，14 个 PR / 18 个文件（PR13 纯文档，PR14 +593 −150）
- 本轮性质：**增量审计** —— 前序两轮已覆盖 PR3→#11（v1，`billing-audit-pr3-pr11.md`）与 PR3→#12（v2，`billing-audit-pr3-pr12.md`，= PR13 提交内容）。
  本轮的独立工作量集中在**新增的 PR13（文档）与 PR14（修复）**，并对全区间做一次收敛复核。
- 实测手段（本机 Windows，Go 工具链，全部实跑）：
  - `go build ./...` ✅
  - `go vet -tags=unit ./internal/...` ✅（无输出）
  - `go test -tags=unit -count=1 ./internal/{service,config,repository,handler}/...` ✅ 全绿
    （service 182.6s / config 3.6s / repository 4.9s / handler 39.4s）
  - 定向探针（临时 `zz_audit_pr314_probe_test.go`，**已删除**，工作区干净）：直接调用仓库内 `EstimateRequestInputTokensUpperBound` / `inlineBinaryPayloadStats` 实测折算口径
  - **未运行**：`-tags=integration`（testcontainers 真 PG/Redis）、前端 vitest/vue-tsc、生产压测、golangci-lint

## 0. 结论摘要

| PR | 内容 | 结论 |
| --- | --- | --- |
| #3–#12 | 见 v1/v2 报告 | 结论继承前序报告（F1–F9 / H1–H12） |
| **#13** | `docs(billing)`：提交 v2 复审报告 `billing-audit-pr3-pr12.md` | **纯文档，零代码风险** ✅ |
| **#14** | `fix(billing)`：修复 H1–H12 + F10，5 个提交 | **H1–H9、H11、H12、F10 实证修复；H10 判定为既有设计并干净还原**；修复本身引入 4 项新观察（N1–N4，见 §2） |

**一句话**：PR14 是一次高质量的自纠偏修复——它把 v2 自己引入的 Θ(n²) DoS（H1）和遗留的 write-off 通道（H2/H3/H4）全部堵住，并顺手把最严重的 H5（客户端可控计费幂等键）改成服务端决定。本机实测印证了每一条声称的数字。剩余问题不是"修复失败"，而是**修复的边界条件**（N1）与**未改变的既有设计**（N4）。

## 1. PR #14 对 H1–H12 / F10 的逐项核验

| 编号 | 状态 | 实测 / 证据 |
| --- | --- | --- |
| **H1** Θ(n²) 回扫 DoS | ✅ 实证 | `jsonKeyBefore` 加 1024 字节预算（`gateway_request_spend_estimate.go:523-576`）。`"A×512;"` 构造体：400→0.98ms、1600→6.8ms、3200→10.7ms、6400→19.5ms（3.28MB）。**1.64MB 由 6.19s → 10.7ms（≈578×）**，明确线性 |
| **H2** 熵闸门漏判 | ✅ 实证 | 阈值 40→24（`:411`）。英文自然文本的 base64（**实测 37 种字符**）→ `dense=720000`、tokens=720037 ✅；低熵仍留文本口径：hex(16)→0、`abab`(2)→0、`AAAA…`(1)→0（低熵保护未被破坏） |
| **H3** 带参数 data URI | ✅ 实证 | 键名回溯改为"反向找值的起始引号"（`:523-569`），不再依赖 URI 字符白名单（`isDataURIChar` 已删除）。1.2MB blob 四种形态：`data:image/png;base64,`→1638、`;charset=utf-8;base64,`→1647、`;name=a.png;base64,`→1640、`data:text/plain;…`→1642（**原 1 200 072**） |
| **H4** 分段 base64 | ✅ 实证 | `findBase64Runs` 把 `\n`/`\r`/`-`/`_`/转义 `\/` 按同一条串合并（`:449-487`）。1.2MB 高熵样本每 42 字符插 `\n`：`dense=1257144`（合并成功，未回落文本）；url-safe 文本位 `dense=1200000`；url-safe 多模态位 `binary`、1 块 |
| **H5** 客户端可控幂等键 | ✅ 主要面修复 | `resolveUsageBillingRequestID`（`:303`）只取上游 id / `generated:`，不再读 ctx；`resolveUsageBillingPayloadFingerprint`（`:350`）缺失负载哈希时返回空串，交由 `Normalize()` 用服务端 `buildUsageBillingFingerprint` 兜底；冲突时换服务端新 id **重新落账**（`:433-440`），不再丢单。**边界见 N1** |
| **H6** 减余额也清标记 | ✅ | `admin_user.go:558-567` 仅 `balanceDiff > 0` 清标记；`user_service.go:1174-1178` 仅 `amount > 0` 清标记。已确认 `UpdateBalance` 的 `amount` 是 **delta**（`usage_service.go:129` 传负值扣款），判断方向正确 |
| **H7** 加钱不清标记 | ✅ | OAuth 首绑（`auth_oauth_first_bind.go:110-134`）与退款回滚（`payment_refund.go:722-737`）均走 `InvalidateUserBalanceAfterCredit`。注入链路已逐级核对：`AuthService.promoService.billingCacheService`、`PaymentService.redeemService.billingCacheService` 均存在 |
| **H8** CJK 费率无上界 | ✅ 实证 | `config.go:3257` 拒绝 `> 8`；`requestSpendCJKRunesPerTokenValue` 运行期钳到 8；`saturatingMul`（`:238-246`）溢出返回 `MaxInt32`。实测 `cfg=1e6` → 钳到 8（56023 token）；直接调内部函数传 1e6 → 饱和为 2147483670（**非负、不 fail-open**） |
| **H9** 注释与实现矛盾 | ✅ | 注释已改写为"标记命中即稠密，熵闸门只用于无标记长串"（`:351-357`），与 `inlineBinaryPayloadStats` 的 `!marker && !looksLike` 短路逻辑一致 |
| **H10** 配额按全额累加 | ✅ 判定合理 | 还原 `cmd.APIKeyQuotaCost` 全额（`usage_billing_repo.go:200-205`、`gateway_usage_billing.go:237-250`），删除了与既有设计冲突的新增单测 `TestApplyUsageBillingEffects_QuotaUsesCollectedAmountOnFloorDrain`，两处补注释记录结论。还原**干净**（无残留死代码，`collectedBalanceCost` 仍有 3 处生产调用） |
| **H11** 扫描无快路径 | ✅ | `len(body) >= requestSpendMinInlineBinaryRun`(512) 才扫描（`:190-195`）；`tiny-body`(16B) 实测不扫描 |
| **H12** legacy 路径差异 | ✅ | `deps.userRepo == nil` 返回错误而非 panic（`:181-184`）；`DeductBalance` 成功后 `GetByID` 回填 `result.NewBalance`（`:204-209`） |
| **F10** batch capture 缺校验 | ✅ | `captureUsageBillingBatchImageBalance` 增加 `batchImageHoldClaimExists` 校验，未冻结即 no-op 返回（`usage_billing_repo.go:454-464`），与 release 路径对称；新增单测 `TestCaptureUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved` |

**测试改动核查（无放水）**：PR14 改写 5 个**锁定旧（不安全）行为**的用例，方向正确：
`PrefersClientRequestIDOverUpstreamRequestID` → `NeverUsesClientRequestIDAsBillingKey`、
`BillingFingerprintFallsBackToContextRequestID` → `...NeverFallsBackToClientContextID`、
`UsesFallbackRequestIDForUsageLog` → `FallbackRequestIDIsServerGenerated` 等。
另修一个与计费无关的随机红用例（`107c007b9`，`TestValidateAffiliateAttributionToken` 的 base64url 末字符有效位不足导致 ~5.9% 假通过）。

## 2. 本轮新发现

### N1【中 · H5 修复的边界】Anthropic 兼容路径仍向**非 OAuth** 上游透传 `x-client-request-id`

- 证据：`allowedHeaders` 白名单（`backend/internal/service/gateway_service.go:447-469`）第 468 行含 `"x-client-request-id": true`，
  被 `gateway_upstream_request.go:139-152` 的透传循环原样发往上游（仅在 OAuth mimicry 路径跳过）。
- 链路：客户端发 `X-Client-Request-ID: K` → 网关注入上游 → 若上游把它**回显**为响应 `x-request-id`
  （部分第三方/中转兼容上游会这么做）→ `ForwardResult.RequestID = K` → `resolveUsageBillingRequestID` 原样返回 K
  → **幂等键重新变成客户端可控**，H5 的"恒定 header = 免费调用"通道在**这类上游上复现**。
- 对照：OpenAI 透传白名单（`openai_gateway_service.go:91-106`）**不含** `x-request-id`/`x-client-request-id`，
  所以 OpenAI 侧修复是完整的；风险仅限 Anthropic 兼容 + 回显行为的上游。
- 标准 Anthropic 不回显该 header，因此严重度取决于部署的实际上游，定性为**中**。
- 建议（择一）：
  1. 从 `allowedHeaders` 移除 `x-client-request-id`（若上游不依赖其幂等语义）；
  2. 或对最终幂等键加服务端前缀/派生（如 `up:<sha256(accountID‖upstreamID)>`），使客户端无法预知键值；
  3. 至少在文档中写明"H5 的修复前提是上游 request id 不由客户端 header 回显"。

### N2【低】冲突重试后，`usage_log.request_id` 与计费去重表的 `request_id` 不一致

- `applyUsageBilling` 命中 `ErrUsageBillingRequestConflict` 时改用 `requestID + ":conflict:" + generateRequestID()` 重新落账
  （`gateway_usage_billing.go:433-440`），但 `usage_log` 已按**原** request_id 写入。
- 后果：运维按 `request_id` 对账时，钱包侧的扣费记录挂在一个"影子 id"上，两边对不上；`usage_billing_dedup` 里也会多出一行。
- 方向是安全的（钱收回来了），仅影响可观测性/对账。建议重试时同步回写 `usage_log.request_id`，或把冲突映射显式记入日志字段。

### N3【低 · 信息】幂等性以"上游返回 id"为前提

- 上游 id 为空时，每笔生成新的 `generated:<随机>`。当前提交路径（`usage_record_worker_pool.Submit`）**不会**对同一笔任务双执行
  （`TrySubmit` 成功即仅入队一次；失败按 overflow policy 单次降级），因此现网无重复扣费风险。
- 但这意味着幂等保证依赖"单次提交"。若未来引入记账重试/人工重放，同一笔的两次记录会拿到不同 `generated:` id → 重复扣费。
  建议在 `resolveUsageBillingRequestID` 注释或运维文档中显式声明该前提。

### N4【中低 · 既有设计，PR14 未改变】多模态键名一律按固定 allowance 折算，不看上下文

- `inlineBinaryPayloadStats` 对命中 `multimodalPayloadKeys`（`url`/`image_url`/`data`/`file_data`）的串**直接**按 1 块 × 1600 token 计
  （`gateway_request_spend_estimate.go:366-372`），binary 分支**不设长度/熵闸门**。
- 实测（同样的 1.2MB 高熵 base64）：

  | 所在键 | binaryBytes | blobs | 估算 token |
  | --- | --- | --- | --- |
  | `text` | 0 | 0 | **1 200 027**（稠密，正确） |
  | `data` | 1 200 000 | 1 | **1 627** |
  | `url` | 1 200 000 | 1 | **1 627** |

  同一份内容，仅因键名叫 `data`/`url`，估算差 **737×**。
- 影响：若客户端把大段文本/文档 base64 后放进名为 `data`/`url`/`file_data` 的自定义字段，**且上游/桥接确实把它计入输入 token**，
  则预检低估约 737×，贴底用户可重新打开 write-off 通道。
- **可达性未确证**：严格实现 OpenAI/Anthropic 协议的上游会忽略未知字段（不计费 → 无害）；风险集中在"会把未知字段并入 prompt"的桥接/兼容上游。
- 这是 PR9 起的既有设计（v1 只记录了"键名不在白名单"的**高估**方向，未覆盖"键名在白名单但非多模态用途"的**低估**方向）。
- 建议：多模态判定加入父键上下文（如 `inline_data.data` / `source.data` / `image_url`），
  或对"无多模态父键 + 超长 + 高熵"的白名单键串降级为稠密。

## 3. 前序结论（F1–F9 / H1–H12）收敛复核

- v1（F1–F9）与 v2（H1–H12）的全部结论**未被 PR14 推翻**。PR14 恰恰是把 v2 的 H 系列与遗留的 F10 落地。
- 唯一**判定变更**：H10 由"缺陷"改判为"既有产品语义"（配额/限流衡量真实消耗，与钱包能否全额收回解耦），
  并还原为全额累加。该判定有仓库自带集成测试 `TestUsageBillingRepositoryApply_DrainsWalletToReserveFloor`
  （断言 `quota_used == 全额`，文案 "quota reflects the real consumption"）支撑，**合理**；若产品侧希望改为"按实收"，应另开 PR 并同步调整耗时/限流窗口语义。
- F2（裸 base64 低估）在 PR14 后**已从"部分修复"转为"完整修复"**：高熵裸串按稠密计，低熵裸串留文本（实测见 H2 行）。

## 4. 已核对成立的关键不变量（本轮复核）

- 估算器：`找串 → 回溯键名 → 三分类（binary/dense/text）` 全链纯位置/字面量判定；回溯成本有上界（1024）→ 整体严格线性。
- CJK 折算是**上界方向**：仅当非结构语言字节中 CJK 占比 ≥90% 才启用，混杂文本回落 2 字节/token；CJK 分支内非 CJK 字节仍按 2 字节/token。
- 结算封底、预留凭据化、耗尽标记闭环、运维端点 adminAuth 等（v1/v2 已核）在本区间无改动，保持成立。
- 配置跨层一致：`request_spend_cjk_tokens_per_rune` 的校验(0–8)/运行期钳制(8)/文档/示例配置四处一致。
- 工作区干净：探针文件已删除，`git status` 仅剩未跟踪的审计素材与既有 json。

## 5. 未验证 / 无法验证

- `-tags=integration` 的真 PG/Redis 套件、前端 vitest/vue-tsc、golangci-lint 未在本机执行（CI 报告为绿）。
- N1 的触发需要"上游把 `x-client-request-id` 回显为 `x-request-id`"，**未能枚举具体上游**；标准 Anthropic 不回显。
- N4 的可达性取决于上游是否把白名单键名的非多模态内容计入输入，未在真实上游复现。
- H1 的绝对耗时随 CPU 而异（量级与线性性已验证）。

## 6. 建议处置顺序

1. **N1**（Anthropic 白名单透传 `x-client-request-id`）—— 唯一能让"H5 修复"在特定上游上失效的边界，建议收口（移除该 header 或对幂等键加服务端派生）。
2. **N4**（多模态键名加父上下文）—— 唯一仍可能产生大额低估的既有面，需先确证目标上游是否计入未知字段。
3. **N3**（文档声明幂等前提）+ **N2**（冲突重试的对账一致性）—— 低成本、提升可运维性。
4. H10 若产品侧确认按实收，另开 PR 并同步耗时/限流口径。

---

## 附：本轮实测数字汇总

| 探针 | 修复前 | 修复后（本机实测） |
| --- | --- | --- |
| H1 构造体 1.64MB / 3.28MB | 6.19s | 10.7ms / 19.5ms（线性） |
| H2 英文文本 base64（37 种字符） | 600 043（低估 1.84×） | dense=720 000，tokens=720 037 |
| H2 低熵 hex / `abab` / 全 A | — | binary=0, dense=0（留文本，未被高估） |
| H3 `;charset=utf-8;base64,` 1.2MB | 1 200 072 | 1 647 |
| H4 MIME 换行 1.2MB | 文本口径（低估 ≈1.8×） | dense=1 257 144（合并成功） |
| H4 url-safe 多模态 | 文本口径（高估 ≈120×） | binary=280 000，1 块 |
| H8 cfg=10^6 | 60 亿 token / 溢出 fail-open | 钳到 8（56 023）；内部直调饱和为 MaxInt32 |
| N4 `data`/`url` 键 1.2MB 高熵 | — | 1 627（`text` 键为 1 200 027，差 737×） |

---

## 7. 修复记录（N1–N4，2026-09-15）

| 编号 | 状态 | 改动 |
| --- | --- | --- |
| **N1** | ✅ | `allowedHeaders`（`gateway_service.go`）移除 `x-client-request-id`，并加注释禁止回加。OpenAI 白名单本就不含 request-id 类 header，因此客户端请求 id header 不再可能到达上游 —— 切断"上游回显 → 幂等键重新可控"的路径。OAuth/mimic 路径出站的该 header 由指纹收敛链（`claude.DefaultHeaders` / `ApplyFingerprint`）单独注入，**不受影响**；Codex 路径的收敛值含账号级命名空间（`scopeCodexAccountIdentityValue` 的 `namespace`），客户端无法预知。 |
| **N2** | ✅ | `applyUsageBilling` 冲突重试成功后把 `usageLog.RequestID` 同步为**实际计费键**（`<原id>:conflict:<服务端id>`），使 `usage_log` 与 `usage_billing_dedup` 两侧 request_id 一致；ALERT 日志同时打印原 id 与重试 id。两处调用方都是"先计费、后落 usage_log"，回写在落库前生效。 |
| **N3** | ✅ | `docs/BILLING_ZERO_OVERSHOOT.md` 新增"计费幂等的安全前提"（含两条运维含义：上游 id 可信性、单次提交前提）；`resolveUsageBillingRequestID` 注释补充同一约束。 |
| **N4** | ✅ | 新增 `jsonParentKeyBefore`（外层键名回溯，1024 预算 + 转义感知的引号奇偶计数）；`data` 键需父键为 `source`/`inline_data`/`inlineData`/`input_audio` 才算多模态块；父键**明确但不在白名单**时回落稠密/文本口径，父键**无法确证**（顶层 `data`、数组元素、超预算）时保持原行为 —— 不把真实图片降级为稠密口径而误 403。文档同步。 |

新增回归测试：

- `TestGatewayService_AnthropicAPIKeyPassthrough_DropsClientRequestIDHeaders`（端到端：客户端 id header 不出现在上游请求）
- `TestPassthroughAllowlistsExcludeClientRequestIDHeaders`（白名单约束，防止将来被加回）
- `TestGatewayServiceRecordUsage_ConflictRetryAlignsUsageLogRequestID`（冲突重试后 usage_log 记录实际计费键）
- `TestJsonParentKeyBefore`、`TestMultimodalPayloadKeyIsBinary_DataRequiresParentContext`、`TestEstimateRequestInputTokensUpperBound_DataKeyNeedsParentContext`（N4 三件套）

实测口径（本机，同一份 1.2MB 高熵 base64，直接调用仓库内估算函数）：

| 场景 | 修复前 | 修复后（实测 token） |
| --- | --- | --- |
| `{"payload":{"data":"<b64>"}}`（普通 data 字段） | 1 627（误按 1 块图片折算） | **1 200 027**（稠密，正确） |
| `{"content":[{"source":{…,"data":"<b64>"}}]}`（Anthropic） | 1 654 | 1 654（不变，仍是 1 块） |
| `{"parts":[{"inline_data":{…,"data":"<b64>"}}]}`（Gemini） | 1 647 | 1 647（不变） |
| `{"input_audio":{"data":"<b64>","format":"wav"}}`（OpenAI） | 1 637 | 1 637（不变） |
| `{"payload":{"url":"data:image/png;base64,<b64>"}}` | 1 638 | 1 638（`url` 键不受本改动影响） |
| `{"input":[{"image_url":"data:image/png;base64,<b64>"}]}`（Responses 字符串形态） | 1 651 | 1 651（不变） |
| `{"content":"<裸 b64>"}`（文本位，回归保护） | 1 200 023 | 1 200 023（不变） |
| `{"data":"<裸 b64>"}`（顶层，父键不可确证） | 1 621 | 1 621（保守保持原行为，不误伤） |

非 token 类的两点直接断言：

| 断言 | 修复前 | 修复后 |
| --- | --- | --- |
| `allowedHeaders["x-client-request-id"]` | `true` | **`false`** |
| `openaiPassthroughAllowedHeaders["x-request-id"]` | `false` | `false`（本就正确） |
| Anthropic API-key 路径客户端 `X-Client-Request-ID` | 原值到达上游 | **不出现在上游请求** |
| 冲突重试时 `usage_log.request_id` | 与原 id 不一致 | **等于实际计费键** |

### 附带清理

`gofmt -l internal/service/` 原本还会报出两个**与本次修复无关、PR3（`7dc17e07b`）起就存在**的格式问题
（`admin_service_email_identity_sync_test.go`、`auth_service_email_bind_test.go` 里 `DeductBalance` 的单行函数体
不符合 gofmt 拆行规则）。零风险纯格式，已一并 `gofmt -w`，现在 `gofmt -l internal/service/` 干净。

验证：`gofmt -l internal/service/`（无输出）、`go build ./...`、`go vet -tags=unit ./internal/...`、
`go test -tags=unit -count=1 ./internal/{service,config,repository,handler}/...` 全绿
（service 192.8s / config 3.6s / repository 4.6s / handler 38.9s），新增测试全部 PASS。
未跑：`-tags=integration`、前端 vitest、golangci-lint。
