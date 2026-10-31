# Issue #603：延迟观测与调度反馈覆盖矩阵

初始基线：`77455cf755fa0f119367b911704f11df88e7917e`（包含 #601）。已按授权普通 merge 集成最新 `origin/production` / `ec007920de042797299fc7cf2c034ad24c136462`（包含 #602、#607），保留双方共享 requesttiming 改动；未 rebase 或 force push。单一 Draft [PR #604](https://github.com/ranxi2001/sub2api/pull/604)，Refs #603，保持 Draft，禁止 merge / ready / release / deploy。唯一 writer；未修改 #602 分支、工作树或账户连接测试功能。

Issue 研究是作者单实例的观察，不是普遍因果，也不用于自动校准。上传完成后的等待包含网络、上游排队和工作，不是 prefill 或纯模型耗时。

| 项 | 当前真实链路 | 原缺口 | 设计 | 实现文件 | 回归 | 状态 | 行为决策 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1. attempt 指标与分组 | #599 `requesttiming` → `request_timing_details` → usage timing service/handler/API/dialog | 只有事件明细，没有可聚合上传/首内容等待 | 每物理 HTTP attempt 派生上传、上传后 semantic 和 visible 等待；真实 SQL 按已读 body、最终 usage cache cohort、effective effort 分组；失败无用量请求在 ops 的 unknown cohort 中保留 | `pkg/requesttiming/latency.go`、`collector.go`、`http.go`；`repository/request_timing.go`、`latency_observability.go`；`service/latency_observability.go`；admin handler、routes、adminaccess、`LatencyObservabilityPanel.vue`，真实 `UsageView.vue` / `OpsDashboard.vue` | `TestIssue603CollectorPublishesAttemptMetrics`、`TestIssue603AttemptBoundaryAndFailoverSemantics`、`TestIssue603RealHTTPTransportBoundaries`、真实 PG SQL、handler 与页面测试 | 35a4eea29 的 collector RED、SQL、handler/UI GREEN 通过；实现完成；当前 head 的最终验收见 PR | 仅同一物理 attempt 相减；缺失、反序、写失败不是 0。成功零值保留，样本数独立；不按最终成功账号推断前面失败 attempt 的性能 |
| 2. LoadRate 容量 | 通用/OpenAI scheduler → `AccountWithConcurrency` → Redis LoadRate → 实际 slot/admission；admin account / ops 读同投影 | `EffectiveLoadFactor` 作为容量导致假空闲，与实际槽位和 priority 不一致 | 所有普通容量输入原样使用 Concurrency；load_factor 保留独立 0.5–2 倍软排序偏好 | `account_repo.go`、`account.go`、`gateway_scheduling.go`、`openai_gateway_scheduling.go`、`openai_account_scheduler.go`、`ops_concurrency.go`、collector、admin account handler | `TestBuildOpenAIAccountLoadRequestUsesRealConcurrency`、projection unit/integration、ops projection、既有 scheduler/priority 回归 | a939d1470 的命名 RED → GREEN、生产包/server build 已通过；35a4eea29 专用容量和当前 head 的最终验收见 PR | 保留 Concurrency=0 的既有无限语义。真实 slot/admission、sticky/wait 不改。原字段/API/运营意图保留，但历史大权重不能伪造额外容量 |
| 3. 默认调度反馈 | 默认 legacy OpenAI 选号跳过高级 EWMA；真实发送已有公开模型/端点/分组/账号的被动 attempt 窗口 | 默认路径未用该窗口，未知和冷启动边界未定义 | 复用 bounded attempt ring；增加发送至首 semantic 的成功样本；默认批量、非批量和读取失败兜底在同优先级合格候选间软排序 | `priority_error_rate.go`、`default_scheduler_feedback.go`、`openai_gateway_scheduling.go` | `TestIssue603DefaultSchedulerUsesScopedFeedback`、`TestIssue603TransportSemanticAnd403Feedback`；真实 transport/handler 与既有权限/额度/priority 回归 | 35a4eea29 默认 selector 旧源码命名 RED + race GREEN 通过；当前 head 的最终验收见 PR | 10 分钟窗口、最少 5 样本，错误先验 5、错误最多 25 点、延迟最多 10 点，合计最多 35。参考 15s 沿用既有配置默认，未从 issue 数字校准。未知/冷启动无惩罚，过期恢复。安全/权限/额度拒绝不进软健康证据；任何软偏好都不能绕 guard。已有高级与 priority 模式保留独立配置；未观测协议维持未知 |
| 4. 大正文切换成本 | failover/retry loop 的真实 HTTP sends → 同一 request 的 attempt JSON | 没有 >1 MiB 的同账号重试/实际账号切换和已测组件成本 | 物理 attempt 的 initial / same_account_retry / account_switch / unknown；后续 send 的上传和等待组件进入 JSON、SQL、usage/ops handler 与 UI | `requesttiming/latency.go`、共享 SQL/API/panel、`UsageTimingDialog.vue` | attempt 边界/重叠/截断/失败写入；SQL 样本数、unbilled trace 去重；UI unknown 与零值回归 | 35a4eea29 第一轮通过，已补无物理 send/失败未计费用量覆盖；当前 head 证据见 PR | 不改 retry budgets，不引入 limit。额外值是第一个 send 之后的组件，不是 wall time 或反事实节省量。响应失败只可单独测 response-header wait。身份未知、重叠、未采集 physical send、截断时 trace 为 partial，不能进入完整切换样本或次数总计；未结束时也无完整成本总值。旧 JSON 无派生成本时样本为 0 |
| 5. 服务端和 E2E | shared UsageTable → admin/user usage；admin/observer timing API | 服务端转发 TTFT/总耗时标签易被误读为用户全链路 | 中英明确服务端首字、服务端总耗时和应用内完整耗时；shared table 与 report 显示客户端 E2E 未采集；API 返回 null/status/source | `UsageTable.vue`、`UsageTimingDialog.vue`、中英 dashboard/requestTiming/latencyObservability；admin/observer timing handler、report service | 真实 `UsageView.spec.ts`、双语 `UsageTable.spec.ts` RED → GREEN、observer ownership、panel stale-response/unknown 测试、远端浏览器前后截图 | 35a4eea29 的页面/文案 RED 和 85 项 UI/i18n 检查及 production build 通过；670f2b7bb 的收口 UI 复验和双语前后截图通过；当前 head 证据见 PR | `end_to_end_ms=null`、`end_to_end_status=not_collected`、`source=none`；client upload 同样未采集。不用 duration、nginx 差值、应用 flush 伪造客户端收到时间 |
| 6. 网络与 body 分钟曲线 | 现有 ops collector/storage/handler/dashboard | 没有出口、重传、qdisc 能力状态、body bytes 曲线 | collector 在集群 leader 锁之前每实例读 proc 聚合计数；存 ops_network_minutes；真实分钟 SQL + ops API/chart。body bytes 由持久化 application traces 聚合，含失败/部分读取 | `service/ops_network.go`、collector、cleanup；迁移 `320_ops_network_minutes.sql`、`repository/ops_network.go`、handler/routes/permissions、`OpsNetworkChart.vue` | `TestIssue603RegisteredOpsNetworkRouteAndGlobalPermission` 的旧生产路由 404 → 新注册 handler 200 与无权限 403 反证；注入 proc fixture 的真实 collector、真实 PG 存储/分钟 SQL、权限/handler、chart 的 measured zero / unknown / qdisc 无数据测试 | 35a4eea29 的 collector/SQL/handler/chart GREEN 通过；已补权限/不支持状态与真实时间轴；当前 head 证据见 PR | 不运行 shell/tc，不做 probe、不改主机。只存随机实例 UUID、聚合值和状态，不存接口名/IP/域名/凭据。qdisc 已补只读无特权 netlink root egress dump，正常采集 delta；权限/能力不足时值 null，不虚构 drop 曲线 |

## 指标与兼容约定

- `body_upload_ms` 等价于成功 `headers_written → request_written`；支持 `wrote_request` 别名。它是 HTTP transport 观察的写入区间，不保证远端收到字节。semantic 可能含推理/结构内容，visible 是独立内容边界。
- SQL 直接读取历史 JSON 的原始边界，不需要 timing schema 扩张；新 JSON 另外存可审阅的派生状态。计费逻辑和去重不改。usage 按 usage row 的创建时间筛选，cache/effort 来自最终用量；account_id 也筛选计费用量所属账号，包含整个请求的先前失败 attempt，不是各账号独立性能排名。
- Ops 另纳入未生成 usage row 的 application trace，时间按应用请求开始，cache/effort 为 unknown。按 trace ID 和计费身份去重。不同 cohort 的 distinct request 数不可直接当计费总数相加；多 trace 可属于同一计费 row。
- `load_factor / concurrency` 仅影响有界候选偏好，不改容量。nil/无效 load_factor 为中性；Concurrency<=0 时不构造权重。大于实际容量的历史权重将明显改变旧流量分配，保留字段便于运营审阅，无自动迁移配置。
- 默认反馈仅对已有可信 OpenAI HTTP transport 证据生效；scope 是公开模型+端点+分组+账号，不把本地存储、客户端断开、权限、安全或计费拒绝当上游性能。403 仅在排除明确权限/凭据等拒绝后形成临时软证据，不造永久禁用。
- 网络计数只反映进程所在网络命名空间，不能宣称物理宿主机或集群带宽；非 loopback 虚拟接口可能重复计量。实例重启换随机 UUID。曲线显示实际分钟时间，缺口断开；45–90 秒外的采样窗口为 uneven_window。计数 reset、接口集合变动、首样本、权限不足、不支持、坏格式分别显式，不能补 0。
- TCP 指标为 `ΔRetransSegs / ΔOutSegs`，不是公网丢包率；无发出分段时比例未知。qdisc 使用 Linux UAPI 的只读 RTM_GETQDISC netlink dump；500ms 总期限、1MiB/64 个包上限、只统计 root egress，解析 TCA_STATS2/TCA_STATS_QUEUE（legacy TCA_STATS 兜底），不相加嵌套队列。权限/系统能力不足、截断、无统计、reset 与拓扑变化均显式无数据，不运行 tc。参考 [Linux UAPI gen_stats](https://github.com/torvalds/linux/blob/master/include/uapi/linux/gen_stats.h) 与 [kernel netlink statistics](https://docs.kernel.org/networking/gen_stats.html)。
- body 曲线是全站 application trace 已读正文，不重复叠加各主机实例，不是客户端上行带宽或全部声明 Content-Length。无记录的分钟为缺数据，失败或半截读取保留已读字节。
- 网络表是唯一新增迁移，按现有 minute retention 清理；旧 binary 可忽略新表回滚。latency 查询最长 31 天、8 秒 timeout；network 最长 24 小时，图展示最近一小时。所有观测都是诊断，不驱动计费或自动基础设施配置。

## 远端验收证据

- [容量 run 41088425102](https://github.com/ranxi2001/sub2api/actions/runs/41088425102)：a939d1470 命名行为 RED（10000 vs 2）→ 新 adapter/repository GREEN → 普通 service/repository/handler/server 编译。
- [完整验收 run 41090840621](https://github.com/ranxi2001/sub2api/actions/runs/41090840621)：35a4eea29 的 collector、默认 selector、真实 usage 页面和双语表格命名 RED；race GREEN、PostgreSQL 15 / Redis 合成隔离 SQL、普通生产包/server、UI production typecheck/build 均通过。UI 82 个页面/组件用例 + 3 个 i18n 检查通过。
- raw 日志与 PR/check-runs/statuses 逐页对账在 `/data/development/artifacts/issue603-latency/`，记录每次 head。不能用上述历史候选替代最终 head 验收。
- `issue603-observability.yml` 的 RED 只改一次性 worktree 的旧生产文件并校验 blob，拒绝编译/fixture/无测试失败；GREEN 保持独立干净候选。远端合成 HTTP/SSE/PG/Redis，浏览器只允许 loopback 和合成 API adapter。
- 本机仅静态源码、Git、gofmt 和 Python 静态检查；无 Go compile/build/test/run/generate、Docker、前端动态，无生产连接/付费 probe/配置修改。

最终 head 的全部 check-runs（filter=all，分页计数）、传统 statuses、raw 日志、截图与逐项验收结果记录于 PR 正文和 artifacts/progress.md；本矩阵列明实现和验证入口，不将历史 SHA 的通过当作当前 head 已通过，也不宣称 parent 已独立 review。

- [670f2b7bb 的完整验收与浏览器证据](https://github.com/ranxi2001/sub2api/actions/runs/41093395359)：UI 命名 RED → GREEN、production build、基线/当前中英桌面移动 usage 页和 network 图截图通过；验证 JSON 的 browserErrors/externalRequests 为空。该候选 lint 定位到测试中未检查的 Flusher 类型断言，后续改为检查 ResponseController.Flush 的返回错误；未将 lint 失败当作行为 RED。

## 追加 #605 的真实链路矩阵（实现与远端验收入口）

最新 live #605 已完整读取，包括原交接之后新增的 4 条评论。评论提出区域/协议、排队和智能路由研究；本次遵循追加 brief 的五项验收，不把研究建议当成授权修改生产、启动 probes 或自动协议路由。typed reasoning 仅表示观察到推理协议事件，不证明排队/计算被拆分。

| 项 | 等价覆盖 / 缺口 | 设计 / 实现路径 | 验收与状态 |
| --- | --- | --- | --- |
| 605-1/5 typed 三边界 | 已有 first_semantic / first_visible / first_byte，但 semantic 不是 reasoning，visible 含工具和推理摘要 | 在真实 SSE 解析边界区分 item.type=reasoning、reasoning delta 与 output_text；新增 first_reasoning、first_visible_text，不重定义原事件。按物理 attempt JSON → usage/admin/ops SQL/API/真实表格和双语 UI，历史无字段显示 — | `TestIssue605MixedSSERecordsTypedBoundaries`、native loopback mixed SSE、typed negative/summary delta/first-byte、batch repo/handler/UI RED → GREEN；实现已推送，当前 head CI 见 PR |
| 605-2 effort/model | 报表已有 effective effort 但默认反馈 key 只有 account/model/endpoint/group；高级 legacy EWMA 跨模型/effort | 增加精确 observation effort 维度，捕获策略后的实际 effort；默认和高级普通反馈只用相同 model/effort 证据，未知保持中性；报告 account×model×effort 边界 | `TestIssue605DefaultAndAdvancedFeedbackEffortIsolation` 与 priority SQL/cache key、xhigh/medium/max/unknown 隔离；实现已推送，当前 head CI 见 PR |
| 605-3 capability | 缺少 reasoning streaming 能力来源 | 默认 unknown + source none，不由样本推断，不改变 WS/HTTP、previous_response_id、工具/缓存路由；可仅提供明确未知的只读能力状态 | unknown/source none 由 ContentObservation/report/API/真实双语 UI 返回；无推断/路由变更，实现已推送，当前 head CI 见 PR |
| 605-4 socket | 当前只有 host 聚合，不能推断逐请求 socket | client↔server 与 server↔upstream 分开返回 null/source=none；反代/代理的实际 hop 不明时显式 obscured/not_collected。不执行 ss/MTR，不拿 host重传代替 socket证据 | 真实 usage list/DTO/report、历史/代理 socket evidence 和双语 UI 明确 null/source/scope；实现已推送，当前 head CI 见 PR |
| 603-6 qdisc 补强 | 原先固定 unsupported，父审要求研究安全读取，现已实现 best effort | 依据 Linux UAPI 的只读 RTM_GETQDISC dump，限定无特权、超时和payload上限，解析 root egress qdisc queue drops；无权限/不支持/截断/reset 显式状态，不调用 tc 或修改配置 | `TestIssue605QdiscRootFixturesAndCollectorDelta` 与 collector→storage→handler→UI；实现已推送，当前 head CI 见 PR |

追加生产增量：typed Content 识别已接真实 SSE scanner/native/passthrough，usage page 批量 repository 读取与 DTO 三边界；分组 SQL 增加 account/public model 与 typed 样本/可见文本分位数；默认/高级普通/priority history 的真实观测链增加 effort（公开模型与策略后请求 effort，max/xhigh 保留独立）key。默认未知 capability/source 与分 hop socket 未采集 envelope 已实现。qdisc root dump 与 fixture/collector delta 测试已实现。上述 #605 与 qdisc 增量已在生产集成后的 checkpoint `270f7e904` 完成远端专用 RED/GREEN；最终交付 exact head 的全部检查仍以 PR 正文和外部 acceptance 记录为准，不以 checkpoint 代替。

#605 typed 分组与列表仅选择最终物理 HTTP attempt，且分组要求其账号与用量/trace 账号匹配；前面切换的失败账号不被归入最终账号的 reasoning/text 分位数。旧首字上传组件保留全 attempt 诊断语义。已补真实 loopback HTTP/SSE native handler 的 first_byte→typed reasoning→output_text 顺序和 mixed-account PG 分位数隔离回归。

真实 native loopback SSE 发现 transport EOF 可早于缓冲区事件解析，导致把有效 typed 内容错判为缺失；已保持上传/first-byte 的 transport 边界，同时把 semantic/visible/typed parser 上界绑定到真实完成请求（要求已知 transport end），不把 EOF 当解析结束。新增旧 content.go blob 的命名行为 RED、buffered EOF native/PG 回归；该失败不是生产探测或 prefill 推断。


## production 集成后的验收 checkpoint

候选 `270f7e904bbf5575345d22c3270258314280830b` 包含 production `ec007920de042797299fc7cf2c034ad24c136462`。以下是该 checkpoint 的真实远端结果；本文件后续文档提交产生的新 head 必须独立重新验收，最终 SHA 和全部 check-runs/statuses 在 PR #604 正文及外部 acceptance 记录中冻结。

| 验收路径 | 结果与证据 |
| --- | --- |
| collector、默认选号、注册路由、typed SSE、effort 隔离、EOF/parser 边界 | [完整验收 41120612019](https://github.com/ranxi2001/sub2api/actions/runs/41120612019) 成功：六个旧生产行为命名断言 RED，拒绝编译/fixture/无测试失败；独立干净候选 race GREEN |
| 真实 SQL / handler / repository / scheduler | 同一 run 中 PostgreSQL 15 / Redis 合成隔离数据库、usage list/DTO、account×model×effort 分位数与 priority history、失败 trace、qdisc collector/store/UI 都通过 |
| 普通生产包与 server | 同一 run 成功编译；server SHA256 `874bd3630b2fff8fc507d93c284dd0ebf63f9d667a72909f5d7a619520215731`，不作为生产发布物 |
| 容量 adapter / projection | [容量验收 41120612094](https://github.com/ranxi2001/sub2api/actions/runs/41120612094) 成功：原 10000 vs 实际 2 命名 RED → GREEN、repository 和普通包编译 |
| 真实页面与中英文浏览器 | 完整验收同一 run：86 个页面/组件用例、3 个 locale 检查、typecheck/Vite build、基线/当前中英桌面移动截图；proof 标记 synthetic=true、browserErrors=[]、externalRequests=[] |
| #602/#607 共享兼容性 | 专用验收执行整个 requesttiming 包（含 #602 account clock/headers/range 测试）；仓库 `make test-unit` 的真实 `internal/integration/accountconnectiontiming` 合成 handler/transport 包纳入最终全 CI；#607 既有 preserved-contracts 与 network RED/GREEN 同 head 验收 |

边界：受限 admin API key 的 usage 响应保持原 #601 allowlist，不自动获得新增诊断字段；完整 admin 和用户本人已授权的 usage 列表才走相应 DTO enrichment。E2E/client upload、逐 hop socket 仍为未采集/source none，reasoning capability unknown；不从 host 数据或协议事件推断排队/计算原因。parent 独立 review 不由 writer 自称完成。
