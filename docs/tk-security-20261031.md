# tk 安全修复第一批：计费、会话、二次验证与计数路由

## 基线与范围

- 基线：`ziyue67/sub2api` 的 `7702524fb`，包含 #141 和 #142。
- 修复分支：`fix/tk-billing-session-security-20261031`。
- 只使用本地虚构用户、令牌、提示词及价格；没有对生产用户重放请求、发送收费推理请求或改动生产配置。
- ARTEX 任务 6 的发现是待核实线索；混合目标发现数量不能作为本站漏洞数量。本批只处理能够用当前源码和隔离测试确认的路径。

## 根因、复现与修改

| 问题 | 修复前的确定性观察 | 修复行为 | 验证入口 |
| --- | --- | --- | --- |
| 客户端 flex 少扣费，1294/310 | 请求 `flex`，上游缺省/default/未知档位仍保留 `flex`，默认半价规则可生效 | 未获上游确认的 flex 按标准档结算；保留上游明确 flex、管理员显式配置的 flex 价格；响应模型切换重新判定该模型自己的显式价格 | `TestTKSecurityUnconfirmedFlex`、`TestTKSecurityFlexSettlement`、既有 ServiceTier/ResponseModel 回归 |
| 退出后 access token 继续有效，1293 | 退出单设备或全部设备后，旧 token 访问受保护入口仍为 200 | 单会话撤销标记、用户单调版本号；普通用户、管理员 JWT、旧 token 刷新、OAuth 绑定入口检查撤销；Redis 错误拒绝认证 | `TestTKSecuritySessionRevocation` |
| 旧 refresh 重放不撤销后继会话，1293 | 旧 refresh 被拒绝，但后继 refresh 仍成功 | Redis Lua 原子消费，保留轮转墓碑；再次使用旧 refresh 撤销对应家族 | `TestTKSecurityRefreshConsumptionIsAtomic`、`TestTKSecuritySessionRevocation/refresh-reuse` |
| TOTP 重放、计数并发及故障放行，1278/891 | 同一码能再次成功；缓存关闭后正确码仍能通过 | 在验证前原子占用尝试次数，验证码时间步 SETNX 消费，缓存失败返回服务不可用；移除 secret 前缀日志 | `TestTKSecurityTotpReplayAndCacheFailure`、`TestTKSecurityTotpConcurrency` |
| step-up 会话隔离及开关故障，1278/891 | 不同无 sid token 共用用户级授权键；设置读取错误绕过门控 | 无 sid token 以 bearer 摘要隔离授权，缺乏会话身份拒绝；区分未设置和存储故障，后者返回 503 | `TestTKSecurityStepUpDoesNotShareLegacyUserGrant`、`TestTKSecurityStepUpSettingsFailureIsClosed` |
| TOTP 导出/轮转漏验证，1284/885 | 基线敏感 handler 在验证前执行，返回 400，而非门控 403 | 三条 POST：`totp-export`、`totp-rotation`、`totp-rotation/verify` 加既有 step-up；前端按需加载弹窗，验证后仅重试一次，关闭时取消 | `TestTKSecurityTotpExportRequiresStepUp`、`OpenAITOTPDialog.spec.ts` |
| token 计数误入生成，721 | Anthropic 分组的 Responses 计数路径到达生成 handler，测试夹具返回 500 | 不支持的 Responses 计数请求在调度/生成前返回 400，指向 Messages 计数端点；OpenAI-compatible 保留现有计数处理 | `TestTKSecurityAnthropicResponseCountNeverGenerates` |
| 撤销失败却返回退出成功 | 撤销缓存抛错后 logout 仍返回 200/success | 清理浏览器状态，但返回 503 和可重试原因，避免将本地清理称作服务器撤销成功 | `TestTKSecurityLogoutDoesNotReportFailedRevocationAsSuccess` |

本批 flex 修复收紧的是最终结算授权，不新增全平台出站参数过滤或新的渠道能力配置。未承诺上游实际按请求档位执行。已有强制缓存 TTL、5m/1h 计费转换规则不在本次修改范围。

## 第二批：补齐敏感数据路由的 step-up

ARTEX #1284 的首次修复只覆盖了 TOTP 导出与轮转三条 POST 路由。后续复测确认，同组已有导出路由受 step-up 保护，但下列敏感数据入口仍可在管理员完成普通认证后直接进入 handler：

- `POST /api/v1/admin/accounts/data`：导入上游账号及凭据；
- `POST /api/v1/admin/proxies/data`：导入代理地址及认证信息；
- `GET /api/v1/admin/redeem-codes/export`：导出兑换码。

第二批为这三条路由复用既有 `StepUpAuthMiddleware`。开关关闭时保持现有兼容行为；开关开启时，未完成当前会话 step-up 的请求在 handler 执行前返回 403。修改不新增数据库迁移，也不改变其他账号、代理或兑换码操作。

回归入口为 `TestTKSecuritySensitiveDataRoutesRequireStepUp`。测试用拦截型 step-up 中间件分别请求三条路由，要求全部在 handler 前返回 403，并确认中间件恰好执行三次。

## 会话存储与兼容边界

- 不新增数据库迁移。JWT 增加可选 `sv`；旧 JWT/refresh 缺字段时视为版本 0，用户级撤销同样能覆盖。
- 生产 Redis refresh adapter 实现 `SessionSecurityStore`，并有编译期接口检查；旧测试/其他适配器保留原接口。更换生产 adapter 时必须实现新的安全存储能力，不能把兼容分支当作等效安全保证。
- 用户版本计数不设 TTL，避免计数自然过期使旧 token 复活。家族撤销 TTL 至少覆盖当前配置的 access/refresh 最长寿命，并留一分钟余量；轮转墓碑沿用旧 refresh 剩余 TTL。
- 撤销依赖共享 Redis 状态的持续保留。Redis 数据丢失、清库、驱逐撤销记录，或在已有长寿命 token 尚未过期时降低相关寿命配置，均需配套使旧 JWT 失效，不能承诺仅凭这些缓存记录跨丢库恢复撤销。
- 单设备退出不撤销其他设备；同一秒撤销全部设备后重新登录可以成功。无 sid 的旧 access-only token 通过用户级撤销覆盖；不凭空推断它属于哪个 refresh 家族。
- JWT 后台/面板访问新增 Redis 撤销检查；API Key 推理鉴权不是 JWT 登录路径。
- 同一 TOTP 时间步跨登录和 step-up 仅能成功使用一次。刚登录后再做敏感动作可能需要等待下一个验证码。
- 既有 step-up 开关继续决定敏感动作是否要求验证；本批只补三条 TOTP 入口，未声称所有管理路由均已覆盖。
- 登出未提供 refresh token 时保留既有清理浏览器状态的兼容行为，不宣称能凭空撤销未识别的服务器会话。

## 验证与证据

本地证据在聊天工作区 `audit/tk-panel-20261031/`；大日志没有提交到源码库。

- `red-security.txt`：flex、退出/撤销、refresh 重放的业务断言失败。
- `red-totp-count.txt`：TOTP 重放/缓存故障、旧会话授权键、计数路由失败。
- `red-extra.txt`：通过 Go overlay 使用原始 `admin.go` 和 `step_up.go`，确认敏感路由和设置故障用例在基线上失败。
- `red-logout.txt`：撤销失败时实际 200，期望 503。
- `green-final.txt`：最终定向回归结果，覆盖实际 `RecordUsage` 的消费日志与余额扣减调用，不将内存 repository stub 称为真实数据库验收。
- `billing-final.txt`：新增完整结算与模型切换用例、既有 ResponseModel/ServiceTier 回归通过；显式 flex 权限不能从基线模型传给另一个响应模型。
- `race-security.txt`：新增仓储/中间件安全用例 race 通过，包括 32 并发 refresh 消费只有一个赢家、32 并发验证码只有一次成功、错误验证码前五次之外被限制。
- `frontend-parent-final.txt`：TOTP 管理和 TokenGuard 父页面 27 个测试通过；`useStepUp` 与 session 导出另有定向回归。
- `frontend-build-final.txt`：最终源码的完整前端构建通过；类型检查、所改前端文件 ESLint 均通过。

Windows 全量回归的首次运行不是全绿：

- 后端出现 POSIX 权限/目录 fsync/Bash 执行问题、部分并行 Redis 超时，以及原先假设“仅客户端 flex 就可半价”的测试。flex 测试已改为上游明确确认 flex 的合法场景，并另加未确认时全额扣费的用例，不降低金额断言。
- `baseline-windows.txt` 在未修改的 `7702524fb` 复现区域状态持久化、归档路径和凭据目录同步等失败；不能把所有其余失败都归为已确认的基线问题。没有完整 Linux 全绿证据。
- 前端首次全量运行有三组失败：新增弹窗引入的父页面 Pinia 初始化问题已通过按需加载修复；筛选栏布局断言在未修改的主分支也复现（`frontend-baseline.txt`）；Windows quick-config 用例在无重负载复测时通过。
- 最终前端全量复测（`frontend-tests-final.txt`）：536 个文件通过、1 个文件失败；4,847 项通过、9 项跳过、1 项失败。唯一失败是已在基线复现的 `AccountTableFilters` 布局断言，无未处理异常；不将此结果写成全绿。
- 真实 PostgreSQL/Redis integration 需要 Docker。本机没有可用 Docker，WSL 已注册发行版的 VHDX 不存在；跳过的 integration 不计为通过。

推荐复跑命令（在 `backend`，Windows 需设置 `GOOS=windows GOARCH=amd64 GOTOOLCHAIN=auto`）：

```text
go test -p 1 -tags unit ./internal/service ./internal/repository ./internal/server/middleware ./internal/server/routes ./internal/handler -run 'TestTKSecurity|TestResolveBillingServiceTier|TestApplyServiceTierBilling|TestEnforceStepUp|TestOpenAIGatewayServiceRecordUsage_ServiceTier|TestAuthHandlerRevokeAllSessions|TestLogoutClearsOAuth' -count=1 -timeout=180s
go test -race -tags unit ./internal/repository ./internal/server/middleware -run '^TestTKSecurity' -count=1 -timeout=240s
go test -tags unit ./... -count=1 -timeout=600s
go test -tags integration ./...
```

前端：`pnpm exec vitest run --maxWorkers=4 --minWorkers=1`、`pnpm run typecheck`、`pnpm run build`。

## 第三批：画布 Key 预检与排行榜内部成本隔离

基线 `c9106fe73` 上新增回归用例后确认两项缺陷：

- `CanvasService.CreateRun` 在校验请求级 Key 与节点覆盖 Key 的用户归属前先写入 `canvas_runs`。节点按顺序创建任务，后续节点的非法 Key 可能在前序任务已经创建后才失败。
- 用户排行榜对普通用户和 observer 序列化 `account_cost`，并接受 `sort_by=account_cost`，形成直接字段泄露和排序侧信道。

修复后，画布在写入运行记录前收集请求 Key 及所有可执行生图节点的有效 Key，去重后一次性调用现有 `VerifyOwnership`。任一 Key 不属于当前用户时返回既有 `API_KEY_FORBIDDEN`，不创建运行记录、不创建任何图片任务，也不进入计费路径；全部 Key 合法时保持原来的任务创建行为。该修复只证明并修正提前落库与部分执行，不把历史非法引用表述为已经发生的盗刷。

排行榜的 `account_cost` 改为仅 `RoleAdmin` 序列化。普通用户和 observer 即使手工发送 `sort_by=account_cost`，仓储查询和响应中的 `sort_by` 都回退为 `tokens`。公开前端请求类型不再提供该排序值；管理员专用用量接口保持原行为。既有 `actual_cost` 可见性开关不受影响。

回归入口：

- `TestCanvasCreateRunRejectsUnownedRequestKeyBeforePersistingRun`
- `TestCanvasCreateRunPreflightsAllNodeOverrideKeysBeforeCreatingAnyTask`
- `TestCanvasCreateRunCreatesTasksAfterAllEffectiveKeysPassOwnershipPreflight`
- `TestDashboardLeaderboardHidesAccountCostAndSortOracleForOrdinaryUsers`
- `TestDashboardLeaderboardHidesAccountCostAndSortOracleForObservers`
- `TestDashboardLeaderboardKeepsAccountCostAndSortForAdmins`

`audit/tk-panel-20261031/batch2-red-tests-c9106fe7-real.txt` 保存了修复前的确定性失败：画布两项均未报错，普通用户和 observer 的查询仍按 `account_cost` 排序。修复后上述定向 Go 测试通过，`internal/handler` 整包通过；前端排行榜 4 项测试、`vue-tsc --noEmit` 和生产构建通过。

Windows 上 `internal/service` 整包仍不是全绿，失败集中在已有的文件权限/持久化交付测试，以及 `Issue578`、quality 调度用例；这些失败不涉及本批修改文件，也不能计作本批通过或在本文中宣称已修复。最终合并门槛仍以 GitHub Linux CI 全部必需检查通过为准。

第三批已通过 PR #146 合并为 `11077b3c1`，并以镜像摘要 `sha256:ea60b46ea7e0e15fe79cdd4f38c66d315ddbc2b4db7482f0056b060df0a39dd3` 部署到 `tk.shour.bond`。发布验收包括健康、未认证边界、60 秒自然流量、容器重启数及 panic/fatal/迁移错误检查；没有使用生产用户会话执行画布或排行榜的登录态业务验收，不将 HTTP 边界探针写成业务路径已实测。

## 第四批：账号登录与改密失败桶

ARTEX #1296 和 #1307 指向两个相互独立的口令猜测入口：

- `POST /api/v1/auth/login` 原有每 IP 20 次/分钟与 IP 风险桶，但轮换出口 IP 时同一邮箱没有统一失败上限。
- `PUT /api/v1/user/password` 会校验当前口令，但原路由没有独立失败桶；全局面板限流又允许豁免管理员，不能代替口令校验保护。

修改后，登录路由在原 IP 桶之外叠加每标准化邮箱 10 次/分钟的净失败桶。邮箱在进入 Redis key 前会 trim、小写化并取 SHA-256 前 8 字节，Redis 中不保存明文标识；中间件读取后恢复请求体，不影响真实登录 handler 再次解析。成功响应使用 Lua 原子退回本次计数且不延长 TTL；失败保留计数。登录限流总开关关闭时，IP 与账号两层同时跳过，保留现有设置语义。

改密路由叠加每认证用户 5 次/10 分钟的净失败桶。该桶不受全局面板限流开关和管理员豁免影响；成功改密原子退回计数，Redis 计数不可用时返回 503，不在故障期继续校验当前口令。

回归入口：

- `TestTKSecurityAccountRateLimitCountsNetFailuresAcrossIPs`
- `TestTKSecurityAccountRateLimitSkipsUnusableIdentityAndHonorsDisable`
- `TestTKSecurityLoginAccountLimitSurvivesRotatingSourceIPs`
- `TestTKSecurityPasswordChangeGuardCountsFailuresAndDoesNotExemptAdmin`
- `TestTKSecurityPasswordChangeGuardFailsClosedWhenCounterUnavailable`

本批定向、所改三包完整 unit 及上述用例 `-race` 在 Windows/Go 1.27.3 通过；生产发布与现网验收状态必须在 PR 合并后另行记录。

## 尚未纳入与发布状态

DTO/原始错误体最小化、密钥/代理凭据掩码、其他敏感管理路由仍需独立复现和修复。

此文档描述修复分支及隔离验证，不能作为生产已部署、历史账单已修正、全部漏洞已清除或 CI 全绿的证明。生产仍需独立发布与验收。
