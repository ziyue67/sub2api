# 完整双应用模型 SSE：合法隔离 fixture 与正常结算前置

本切片基于已经双事件 CI 通过的 `e74c98dcf`。原 bootstrap、HTTP 七项/TLS 十项、全部硬内存/隔离/401/故障撤流/恢复/SIGTERM 断言保留。本轮实际新增代码与远端入口，不把已有 bootstrap 当模型验收。

## 实际链路

仍由已验证的 bootstrap 启动两个**未修改的完整 `backend/cmd/server`**：实际配置、Wire、PostgreSQL migrations、HTTP鉴权、gateway reader调度、真实Redis及真实Caddy。额外的 Go 工具 `backend/cmd/fullapp-fixture` 只有显式 `fullappfixture` build tag 才能构建，不进生产server/Release。它不是应用替身，只做隔离种子、有限状态读取和合成模型上游。

种子工具仅接受 stdin JSON，拒绝不以本轮生成 `s2-bootstrap-` 命名的 PG/Redis、非固定fixture数据库/端口，以及非internal fixture模型源；不从生产配置或宿主环境加载连接，不将密码/APIKey/DSN放argv/日志。失败只返回固定阶段。实际调用Ent/User/Group、APIKeyRepository和AccountRepository.CreateWithAccountGroups建立active用户、OpenAI标准公共组、APIKey、OpenAI api_key账号和组关系，root `parent_account_id=NULL/quota_dimension=global`；不删约束、迁移或修改账本迎合fixture。

账号只指向同一internal网络内合成模型容器，凭据虚构，模型仅 `gpt-6.1-sol`。从真实AccountRepository重新hydrate并检查资格后，经真实SchedulerCache的CaptureBucketWriteToken/SetSnapshot发布该group/openai/single快照。两个真实应用仍是gateway reader，不能临时切成双full或DB fallback绕过缺快照。

模型夹具为独立受限Go进程，接受实际应用发来的HTTP `/v1/responses`，校验合成上游认证和模型，输出合法Responses `response.created`、文本delta、`response.completed`、固定usage7in/2out及结束标记；只统计数字accept/complete/active，无请求日志。模型没有任何真实生成、互联网出口或付费服务。测试为此封闭网络显式允许HTTP/private fixture；应用生产默认、安全配置和TLS验证未改，不使用skip证书验证。

## 新回归实际要求

`test_fullapp_models.py` 复用原bootstrap仅新增无默认效果的价格/应用配置hook，原独立bootstrap CI步骤照常运行。新模型步骤从全新独占fixture开始，生成单独受限APIKey、合法种子及共享快照；本地定价改为合成**非零**成本，避免空账本路径也被当通过。

仅精确发送四次真实鉴权POST，**不重试模型请求来预热**。每次必须：

1. 经真实Caddy、两个完整应用中的一个，HTTP200/event-stream且含实际 `response.completed` 和合成输出；四次实际目标集合须覆盖两个应用。
2. 合成上游累计accepted/completed各恰为请求序号、active=0，不能把重放后最终200算通过。
3. 真实数据库该fixture的usage_logs和usage_billing_dedup累计各恰为请求序号，input/output token为7n/2n，真实用户balance出现非零合成扣减且范围合理。不是只调用账本repository的fake结果，也不改生产入账代码。
4. 通过真实ConcurrencyCache读取user/account/APIKey槽位均归零；下一次请求仍能走实际路径，不能只靠停止容器或TTL消失掩盖泄漏。12秒等待只读收敛，不重放POST/修改业务TTL或时钟。

报告只固定生成/用量/去重行计数及槽位数字，不包含Key、账户凭据、SQL正文、业务请求正文或内部网络地址。DB观察工具只查询本fixture实体范围，连接在stdin中，容器/匿名卷/network/临时文件沿原严格清理。模型容器额外256MiB、CPU0.5/pids128、readonly/capdrop/no-newpriv，不加入外部网络；原两应用及其他容器资源限制不变。

## 远端门禁与本机边界

`multihost-fullapp.yml` 保留原bootstrap步骤，随后在远端单编译槽构建显式tag工具：

```sh
cd backend
CGO_ENABLED=0 GOMAXPROCS=1 GOFLAGS=-p=1 go build -tags=fullappfixture -trimpath \
  -o <runner-temp>/sub2api-fullapp/fullapp-fixture ./cmd/fullapp-fixture
```

再独立执行 `python3 deploy/traffic/test_fullapp_models.py`。新入口仅加载新增正常SSE用例，不因继承重复执行原bootstrap suite；原suite仍由原步骤完整跑。普通production server没有测试tag和新增CLI能力。原Caddy binary固定版本/哈希不变，job35分钟与新模型步骤15分钟有界，所有动态编译/Go工具/PGRedis/Docker/模型fixture只在CI runner。

**本机仅gofmt、Python AST/YAML解析、bash语法和git静态检查，未执行/编译新增工具或测试。** 新精确head的双push/PR实际工具编译、合法种子阶段、`test_two_real_applications_complete_sse_once_and_settle_shared_state`真实执行/PASS/数字收尾及全部旧CI才是门禁。e74旧绿不证明新增模型链通过；新工具API签名/运行仍可能暴露缺口，失败应据固定阶段和原日志最小修，不skip/删断言/盲重跑。

## 明确后续而非已完成项

这个最小依赖前置先证明两真实应用的**正常SSE/真实结算/槽位收尾**。在途旧SSE drain、新请求切peer、accept后EOF/partial流不跨节点重放、客户端取消和失败/取消后的账本/并发收尾仍须沿此合法夹具新增受控故障模式及逐请求计数，不能拿正常SSE或旧Python上游七/十项当这些完整应用场景已经通过。当前没有自动恢复/重放、长期媒体/供应商新语义，也未修改生产账本或业务策略。

真实两主机、DNS、用户流量、出口字节及真实付费模型仍未验，不因隔离模型fixture就视为授权生产操作。

## 后续最小切片：真实在途模型 SSE drain

基于正常模型链已在 `2a13fc29f` 双事件通过的证据，新增独立新鲜 fixture 用例 `test_inflight_sse_finishes_on_draining_owner_and_new_model_reaches_peer_once`。原正常四请求用例函数体不变、仍独立执行；新模型 upstream 只新增有限 hold/release 控制，不改变生产app/gateway/账本策略。

先真实鉴权发送一条合成 `gpt-6.1-sol` SSE，读取实际文本delta后证明上游accepted1/completed0/active1、真实user/account/APIKey槽均1且未入usage/dedup。随后向**实际拥有该流的完整应用**发送SIGTERM，沿原main BeginDrain→原drain delay→Shutdown/Wait/Cleanup：直接readiness503、Caddy采样收敛后无Key401观察只到peer；这类观察不调用模型，也不重发旧POST。

再仅发一条新鉴权模型POST，必须由peer处理并完成；此时原流仍active1、总accepted2/completed1。随后通过同internal网的独立控制端点release原流，原响应继续返回completed，owner正常exit0无OOM、peer存活。最终上游两请求各只接受/完成一次，真实usage/dedup各2、tokens14/4、合成非零扣费、三类slots归零。status工具改在存活peer内执行，避免用已退出app查询掩盖收尾。报告仅固定计数/状态，不输出地址/Key/正文。

原生产graceful_shutdown_timeout=5、shutdown_drain_delay=4及Caddy健康/零重试配置不改；新夹具hold最多12秒、读流12秒、readiness撤离2秒/Caddy新流收敛3秒、只读最终结算12秒，Docker wait20秒和原硬内存/internal隔离均保持有界。若deadline触发或原流被提前取消/重发必须失败，不能加长生产停机预算或客户端自动重放来迁就测试。

本轮新切片仅源码/静态检查，尚未编译或动态执行；新精确head push/PR须显示两个独立模型用例实际执行、normal4及inflight2请求报告、所有phase/PASS/cleanup与全部原CI。acceptedEOF、partialEOF、客户端cancel后的失败结算/槽清理、并发争抢和真实两机仍未覆盖，不把此drain切片写成所有故障模式已完成。

## 后续最小故障切片：部分语义输出后 EOF

normal4 和在途 drain2 已在基线 `2da2bf9ae` 双事件 CI 成功，之后新增独立 fresh fixture 用例 `test_partial_model_sse_eof_is_not_replayed_or_billed_and_peer_can_continue`。模型工具只对固定合成 input marker 在 `response.created` 和真实文本 delta 已 flush 后返回，无 `response.completed`、usage 或 `[DONE]`；这是 HTTP 响应结束/协议缺终态 EOF，不冒充 TCP reset、上游发响应前 EOF 或所有错误类型。

真实 Caddy/两个未修改完整 app 必须把已经开始的流留在原请求：客户端得到文本 delta，但没有 completed/DONE；合成上游accepted1/completed0/active0，真实usage/dedup均0、tokens0、用户余额仍10，三类slots均0。该零结算契约来自当前passthrough已输出后缺terminal返回普通错误/nil ForwardResult，而不是新增账本策略。若真实路径行为不同，须据原日志定位，不能删断言/将错误流记成完成。

然后仅发一个**新的**显式客户端control POST（独立request ID/幂等提示），正常轮转peer完成SSE，最终accepted2/completed1/active0、usage/dedup各1、tokens7/2、非零合成扣费、三slots0；总计数仍包含失败请求，可识别中途或后台隐藏重放。新用例精确两模型POST不重试，不复用失败请求身份实现自动恢复。原normal/drain两个用例AST和所有旧断言保持，真实生产应用/账本/模板/超时/硬资源/隔离均未改。

本轮仅gofmt/AST/YAML/diff静态检查，尚无新动态结果；新精确head双事件模型步骤须执行normal、drain、partial三个独立case及全部phase/固定计数/清理和原CI才可通过。acceptedEOF（收到请求但尚无下游语义输出）、客户端取消/失败账本收尾与并发争抢仍单独未完成，不能以本partial case或旧drain替代。

## 后续最小边界：接收后、任何响应前 EOF

基线 `6bcbe7438` 的 normal/drain/partial 三用例已在双事件 CI 通过。新独立 case `test_accepted_model_request_eof_is_not_replayed_or_settled_as_success` 保留现有**单个合法上游账号池**和全部生产 failover 策略，不为断言关闭重试。先一条正常control请求真实结算一次；第二条独立鉴权POST由另一个完整应用收到，合成模型已完整读取请求体、校验认证与模型、增加accepted后，在任何HTTP响应/语义SSE输出前hijack断开连接。

真实入口必须报502/503错误，不伪造completed/DONE；总上游accepted2/completed1/active0，只有control有usage/dedup各1、tokens7/2及既有扣费，失败后余额与control基线相等，三slots0。这不同于partial语义输出后EOF；精确两条模型POST，不以重发或换请求身份预热/恢复。原normal/drain/partial三个caseAST和所有预算/硬限制/清理保持。

**边界限制**：当前应用在transport错误或尚无语义输出EOF时允许既有账户failover；本用例只有一个合法上游账号，验证两个应用入口及Caddy不复制该请求，不证明多个上游账户的全部切换/重试都禁止重复生成。不能据此宣称端到端所有ambiguous-send状态都可安全重试或已禁重放；多账号备用路径需要另一个明确检查点/契约审查，不能靠测试关闭或改生产策略隐瞒。本场景通过也不代表client cancel/失败结算或共享并发争抢完成。

新源码仅静态未本机编译运行；精确新head双事件必须bootstrap+四模型case实际PASS/计数/结算/slots/cleanup及全部原CI。若错误被既有策略重放、返回成功或扣费，必须保留失败并按实际调用链定位，不skip/弱化，也不擅定透明恢复/重放产品语义。

## 后续最小边界：客户端取消后的既有 usage 收尾

基线 `a5db0d575` 的 normal/drain/partial/acceptedEOF 四用例已在双事件 CI 通过。新增独立 fresh fixture 用例 `test_client_cancel_drains_existing_upstream_usage_once_and_peer_can_continue`，仍是完整两个真实应用、真实 Caddy 和隔离合成模型，不修改生产取消、账本或并发策略。

当前普通 OpenAI passthrough 在上游请求发出后使用分离的 context 读取终态/usage；客户端断开停止下游写入，但不会据此把已经生成的模型用量记成免费或取消上游。依据 `openai_gateway_passthrough.go` 的 upstream read 生命周期与 Responses handler 的 usage 提交路径，新用例先收到真实 delta、确认一条活跃生成/三类槽位占用/尚未结算，再对真实客户端 socket 执行双向 shutdown 和 close。原合成上游必须仍只有一条 active 请求、没有提前 usage/dedup 或扣费。释放现有 hold 后，原请求完成一次，usage/dedup各1、tokens7/2、非零合成扣费、三类 slots 最终归零。中间槽位是否在取消时立即释放由既有 admission owner 模式决定，本用例不改变或另定此策略。

再发唯一一个新的显式客户端 control POST，由轮转 peer 正常完成；最终 accepted/completed各2、usage/dedup各2、tokens14/4，余额相对第一次结算继续减少，三 slots 为0。计数、取消 socket 状态、合成余额及清理 phase 导出固定报告；总共两条模型 POST，不自动重试、不迁移旧流、不承诺向客户端返回取消后的终态。复用既有 hold最多12秒、只读结算12秒、各容器硬内存/internal网络和全部 cleanup 限制；原四个 case AST/断言保持。

本切片本机只做 AST/YAML/diff 静态检查，尚未动态执行；需新精确 head 双事件真实 bootstrap + 五模型 case 及全部原 CI。失败账本并发争抢、多账号 ambiguous failover、真实两机流量、透明续接与媒体生命周期仍不由本取消用例覆盖。

## 后续最小边界：跨应用重叠请求的失败结算与租约隔离

基线 `772b0568a` 的 bootstrap6、normal/drain/partial/acceptedEOF/cancel五模型用例及全部适用 CI 已在双事件通过。新增独立 fresh fixture 用例 `test_failed_concurrent_stream_releases_only_its_lease_and_peer_settles_once`，复用同一合法用户、API Key、账号（各并发上限2）、真实共享 PostgreSQL/Redis 与两个完整应用，不修改生产 handler/账本/schema/并发策略。

精确两条客户端模型 POST 经真实 Caddy 到不同应用，各收到真实 delta 后同时保持活跃：accepted2/completed0/active2、三类 slots均2、usage/dedup/tokens均0、合成余额10。tagged 合成模型只新增独立 `release-failure` 控制通道与固定 held-partial-EOF marker；先只释放失败流，缺 completed/DONE/terminal usage，不重放、不生成成功结算。真实数据库仍无 usage/dedup/扣费，而另一个应用原成功流仍 active1，三类 slots必须均为1；这能检出失败路径误清 peer 租约或漏清自己的租约。

然后释放既有正常 hold，原成功请求完成一次：总 accepted2/completed1/active0、usage/dedup各1、tokens7/2、非零合成扣费、三类 slots均0。原上游计数涵盖失败请求，能识别后台重放；最终固定报告导出 2→1→0 槽位、tokens、合成余额、计数与清理 phase。当前 partial EOF 的 nil ForwardResult 不提交成功 usage，租约按 requestID/leaseID独立删除；本用例验证已有路径，不另定失败计费或调度政策。

原五个 case AST及所有断言保留，原模型/正常hold行为与全套硬内存/internal隔离/cleanup/CI预算保持。新失败hold复用12秒上限；失败租约状态只读收敛最多6秒，最终结算12秒，不为了测试延长生产停机或槽位TTL。无第三请求/客户端模型重试，本机仅静态，动态待新精确head双事件 bootstrap6+模型6项和全部原CI。

边界：这是重叠成功/失败请求的账本隔离与各自租约收尾，未证明多个成功终态同时写账本、同幂等身份的重复提交争抢、第三请求限流或所有多账号 ambiguous failover；真实两机、透明续接与长期媒体生命周期仍未交付。若实际新 CI失败，必须读精确原始日志定向修复，不能弱化槽位/计费/无重放断言或改变生产策略迁就夹具。

## 后续最小边界：双成功流共同释放后的累计结算

#547 的真实双事件 bootstrap6+模型6（head `c7b519b40`）已成功且合并；其失败并发场景只有一条成功结算，normal4逐条结算、drain则peer和owner先后完成。新增独立 fresh fixture `test_two_held_successes_release_together_and_preserve_both_settlements`，不重复以上顺序或单成功场景：同一合法用户、Key和单账号的两条成功模型流由两个完整app分别持有，均输出真实delta后hold，accepted2/completed0/active2、三slots2、usage/dedup/tokens0、余额10。

只调用一次既有release控制，使两条原上游同时具备完成条件；不重发模型POST、不新增控制端点/Go夹具。两个客户端原响应均须有 `response.completed`；当前passthrough会在成功终态的空行停止读取，故不要求额外 `[DONE]` 才算完成。最终真实PG usage/dedup均2、tokens14/4、accepted/completed2/active0、三slots0。按现有定价输入7×0.000001+输出2×0.000002、倍率1，Decimal核对累计精确余额 **9.999978**，避免仅判断“余额非零减少”漏掉少扣或重扣。

这验证两条已重叠在途请求共同释放后两个app的完整结算保留，不声称强制两个SQL事务物理同时，也不证明同billing身份去重竞争、第三请求上限或所有race。原六个case AST/全部断言、tagged Go工具/生产代码/账本、硬资源/internal网/超时/清理均不改；suite显式7项。每条hold仍最多12秒，最终只读结算12秒，恰好两模型POST。

本轮固定production `5e9bbe994`，保留已合并其他改动；本机仅源码静态，新动态必须后续精确head双push/PR真实bootstrap6+模型7和全部适用CI/security/Caddy通过。多账号、同billing身份争抢、真实双机、透明续接/长期媒体语义仍未验，不借本测试新增生产策略。

## 后续最小边界：现有多账号策略在无响应 EOF 后重新接受请求

#547 head `c7b519b40` 的双事件 bootstrap6+模型6及全部适用 CI 已通过并合并。本轮固定production `5e9bbe994`，新增独立 fresh fixture `test_existing_multi_account_failover_can_accept_again_after_ambiguous_eof`，只记录当前普通 apikey 的策略，不将单账号无重放结论扩大到多账号，也不新增更保守或更积极的重试行为。

tagged工具的显式 `seed-multi` 沿真实 repository 能力接口原子创建两个合法、可调度的 root/global账号，各自使用不同合成凭据、绑定同一group，并写入同一真实调度快照。两者同优先级，不假定随机/负载选择顺序。工具 `status-multi` 只读观察两个账号各自的usage/tokens和槽位、共享Key dedup和用户余额；旧单账号seed/status与原六case保持兼容。

客户端通过真实 Caddy/完整应用只发**一次**合法模型 POST。合成上游第一次已读完body/认证并增加accepted，在任何响应前hijack EOF；之后接受的另一个账号按现有策略正常完成。原应用没有获得“首次已被接受”的额外信号，既有普通EOF→failover路径不改。用例要求总upstream accepted=2、completed=1、两个不同账号各accepted=1；有终态的账号usage=1/tokens7+2，没终态的账号本地usage=0，Key dedup=1、合成余额9.999989，user/key/两account槽位均0、应用存活且cleanup成功。固定报告明确标记自动改选账号后重新接受已观察到，**不能标为零重放/最多一次保证**。

这是同一个入口请求在所选完整app内部的账号failover；不能冒充客户端跨节点自动重放或两个入口app都收到该POST。计数证明首次接受，不声称首次已完成生成，更不能因本地没有usage就断言真实供应商不会执行或收费。真实供应商的第一次费用不在本fixture模型内，报告标为未建模/未知。

原有客户端取消/插件RequestSent/Copilot特殊保护、普通EOF的failover、切换预算和账本策略均保留。是否在普通HTTP已发且结果不明时停止重放，仍需独立选择可用性/重复风险和兼容范围；供应商幂等也不是通用能力，本用例不作选择。恰好1条客户端POST、2次上游接受，读流8秒/只读结算12秒沿旧预算，不扩大硬资源/internal网/CI/cleanup，原六个case AST不变、显式suite7。

新增源码本机仅静态；新精确head必须远端双push/PR实际bootstrap6+模型7和全部CI/security/Caddy通过。多账号本场景仅为当前策略的有界现状证据，其他pool重试/全部故障类型/同billing身份并发、真实两机、透明续接和长期媒体仍未覆盖。不将旧547或其他未推切片的绿色套到这里。

## PR549整合范围

本次在已验99bf双成功切片上仅整合243f多账号无响应EOF，双成功用例及原七类AST完整保留，suite明确8项。上述两个单片各自suite7/待验描述为历史开发检查点；最终整合head必须新双事件bootstrap6+模型8及全部CI/security/Caddy重新验证，不能沿99bf旧绿。未包含后续partial多账号对照或HTTP attempt增量，生产策略、原资源/隔离/预算不改。

### 同双eligible账号的语义输出后 partial EOF 对照

后续追加 `test_existing_multi_account_policy_stops_after_partial_semantic_output_eof`，沿用完全相同的双账号合法种子和默认策略，但固定marker在真实文本delta flush后EOF，不执行前响应hijack。一个客户端POST必须得到delta、没有completed/DONE；总上游accepted1/completed0、两账号各自计数之和1，第二账号没有被接受；两账号本地usage/tokens、dedup均0，余额10、共享及两account槽位全0。当前语义输出后的缺终态返回普通错误而非换账号，测试不为得到此结果调整配置或新禁failover。

无响应EOF与partial EOF两项分别用fresh fixture，仅改变故障检查点：前者现有策略可造成双账号重新接受，后者在已输出语义时停止。报告区分入口客户端POST与所选app内的账号attempt；两项均不根据本地usage推断首次供应商没有执行或收费。首次外部费用仍未建模，其他错误/pool/供应商幂等与通用最多一次保证仍未覆盖。

原六case及先前accepted多账号case AST不变，显式suite8；新base仍固定5e9，原硬资源/internal网络/8秒读流和12秒只读收尾/cleanup/CI预算不变。新增对照同样只有1客户端模型POST，未混入独立99bf双成功切片。最终组合源码本机仅静态，须新精确head远端双事件bootstrap6+模型8及全部CI/security/Caddy实际验收，失败据原joblog最小定位，不改生产policy或弱化断言。

本次PR549最终整合同时保留双成功，模型suite为9项（原七项加两项多账号），本章节单片suite8为历史开发检查点；最终精确head须双事件bootstrap6+models9重新验证。

随后补齐按账号的 HTTP-observed attempt 计数：在合成模型 `/v1/responses` 到达、按固定合成凭据识别角色后、body解析前增加；body读完且认证/模型合法后才计accepted，终态写出后计completed。它不代表未到达上游的DNS/TCP/TLS失败或全部应用网络尝试。新多账号观察helper要求本fixture每账号observed attempts=accepted，保留两个场景原有所有断言；固定日志与报告同时列出每账号attempt/accepted/completed。无响应EOF最终各账号attempt1/accepted1、合计completion1；partial场景两账号attempt之和1/accepted之和1、completion0。原六类及两个新case测试方法AST不变，仅新多账号helper增加观察/断言，不改变生产策略或资源预算。此增补也尚未动态验证。

## 后续最小边界：两应用容量已满时，第三个排队客户端取消

#549 head `b2774d546` 的双事件 bootstrap6+模型9及全部适用CI已成功并合并。本轮固定合并后的production `a4276268e`，只新增独立fresh case `test_third_queued_client_cancels_before_generation_without_releasing_two_active_peers`，验证真实请求链的共享Key等待取消，区别于此前已生成模型后的cancel、只占两槽的成功/失败场景。

同一合法用户/Key/单账号并发上限2，真实Caddy让两完整app各持一条已输出delta的原成功流，三类slots和Key queue active均2、waiting0。第三条合法客户端POST在独立reader线程等待响应，沿当前默认Key队列（max_waiting5、timeout30秒，不改配置）进入真实Redis waiting1；其不得额外占user/account槽或到达合成上游，accepted仍2/active2、usage/dedup0/余额10。

只在真实queue waiting=1被观察到后关闭第三客户端TCP并有界join reader，要求没有返回业务响应，waiting收敛0；原两流active2、三slots2和Keyactive2全部保持、没有新增模型生成或扣费。再只调用一次既有release，两个原响应各完成一次，最终accepted2/completed2、usage2/dedup2、tokens14/4、精确合成余额9.999978，三slots、Keyactive和waiting均0。总计恰好3客户端POST、2上游接受/生成，无客户端重试或收费模型。

tagged status仅复用实际 `APIKeySlotQueueCache.GetAPIKeyQueueStats` 原子观察接口输出固定active/waiting计数，不修改生产Redis/队列状态或业务路径。reader的cleanup注册在容器清理之前，失败也shutdown/close/join，不保留后台连接；原九个case AST不变，suite明确10项。原hold12秒/读流与硬资源/internal隔离/CI预算不变，新增等待观察3秒、取消清队只读3秒、reader join2秒、最终只读结算12秒；若原hold先到期或队列没有真实进入，应失败，不延长生产预算或skip。

此项验证既有队列取消前置准入与peer租约隔离，不覆盖队列满/超时/撤销或所有队列策略、同billing身份去重、多账号全部故障、真实双机/透明续接媒体。新源码本机仅AST/gofmt/diff静态，没有Go/Docker/动态；后续精确head必须远端双事件真实编译/bootstrap6+模型10及全部CI/security/Caddy，原始waiting1→0/active与账本/slots/cleanup及checkout分开归档。失败据原joblog最小定位，不改队列/计费/取消策略迁就断言。
