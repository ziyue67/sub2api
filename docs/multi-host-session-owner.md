# 多主机会话归属：HTTP共享owner基础

本次仅完善Responses HTTP的**下游用户授权归属**，不实现原生WS的节点owner路由。现有`ValidateOpenAIHTTPResponseOwner`在转发带`previous_response_id`的HTTP请求前校验归属；响应成功后`bindHTTPResponseAccount`分别记录上游账号粘性及下游用户owner。账号相同不等于用户相同，不能用账号粘性替代授权。

## 共享记录与兼容

生产Redis适配层新增可选`OpenAIHTTPResponseOwnerCache`能力，不扩大既有`GatewayCache`接口，不新增部署开关。新writer用同一Lua原子写入一条`v1:<user_id>:<api_key_id>`记录及旧reader需要的两个整数兼容键，使用相同毫秒TTL。键仍按group隔离，response ID只以SHA-256摘要进入键，不保存请求正文、API Key字符串、上游凭据、节点地址或连接ID。

新reader在同一Lua内读取v1及兼容键：有v1时须与完整兼容键一致，仅当v1完全不存在时才按旧键对判断。旧记录缺一键则不授予续接；v1未知版本、损坏、与旧writer改写的兼容键冲突或Redis不可用时拒绝，不用本机热缓存或旧键绕过。新writer发布失败也不在本机留下授权。shared adapter路径每次授权读Redis，不把权威TTL延长到本地一分钟；本机缓存命中不能绕过到期、全格式删除或Redis故障。

同一用户的其他API Key继续沿用既有可续接语义，跨用户或跨group仍拒绝；不修改账本、并发、模型调用、HTTP错误格式或重试策略。没有共享cache的旧适配层和单进程用法保留旧行为，因此不承诺这些自定义adapter具有新原子契约。

## 升级与失效

新writer兼写旧格式，支持旧reader；新reader兼读旧writer留下的完整键对。滚动升级中旧writer本身仍分两次写，无法仅靠新reader保证其发布原子性；应完成全部writer升级后再宣称全路径原子。response ID作为上游生成的不可变归属标识，不能用相同ID跨用户重分配来迁移会话。

显式删除owner时必须删除v1和两条旧格式键；只删除v1会按兼容规则重新读取旧键，不表示撤销。若需要用户撤销/账号禁用，仍须沿用现有鉴权和撤销流程，本次不新增撤销API。TTL到期正常清理全部键，无数据库迁移；回滚旧二进制仍可读旧键，额外v1键按TTL自行过期。老二进制的本地TTL延长和双写局限也随回滚恢复。

### 兼容适配层的失败发布顺序

未提供原子 `OpenAIHTTPResponseOwnerCache` 能力的兼容适配器，原先会先安装process-local owner，再写两条共享整数键。任一写失败返回error后，同一writer仍可能用新本地记录授权，而新reader因缺键不授权。现在先确认两次共享写均成功，才安装新本地记录；本地到期时间仍从尝试开始计算，不因共享IO延长。无cache的纯本地模式及原子shared路径保持，失败不自动重放模型/重复结算。

这是生产状态存储逻辑的发布顺序修复，不是仅增加组合测试或新透明恢复策略。回归源码分别注入第一/第二写失败，通过公开Bind/Get确认writer与新reader都没有新授权；恢复后重发绑定，两者可读取完整owner，本地-only兼容保留。新增检查未本机编译/运行，须精确新head远端CI验证。

兼容双键仍不是跨进程原子操作，旧owner重绑定/部分写残留和旧reader热TTL等历史限制没有被这一顺序变更消除。生产Redis已使用原子shared能力，不从本修复推断新增跨主机恢复或撤销保证；需要原子契约的适配器应实现现有可选接口，不能靠local缓存替代共享发布成功。

## 支持范围与剩余缺口

| 状态 | 当前位置 | 本次承诺 |
| --- | --- | --- |
| HTTP tenant-owner | 共享Redis，可跨reader查询 | 原子发布、版本识别、权威TTL；上游账号及模型是否仍可续接另行判断 |
| response→account | 现有Redis及本地缓存 | 保留原逻辑，不把它当tenant授权 |
| 原生WS response→conn、turn state、session→conn | 进程内存和socket | 不共享、不转发；重启及换节点不能保证无损续接 |
| 图片/附件owner | 另项工作 | 本次无变更 |

因此本次不能称“全部会话可跨节点续接”，也不将DNS多A当成会话粘性。下一步需要稳定node ID/boot ID、明确WS状态保留期限、owner查找及安全拒绝/重建契约，再单独实现owner路由或受控恢复；图片归属随后独立推进。

## 回归与证据边界

新增仓储回归覆盖两个独立store共享、旧reader格式、新reader读旧键、缺键/未知版本/错误类型、组隔离、共享到期、发布失败、本地热节点共享故障和全格式删除。真实Redis检查接入现有`GatewayCacheSuite.TestHTTPResponseOwnerSharedRecord`；现有service用例继续检查同用户不同Key可续接、跨用户拒绝。

本机是生产宿主，本轮不运行Go编译、测试、构建。只做源码及非编译静态检查，全部回归结果等待精确提交的远端CI；新增测试源码不是已验证结果。没有访问生产DB/Redis或真实模型，不代表真实两机HTTP续接已验收。

## 可选的原生WS node/boot安全门禁

新增默认关闭的配置（环境变量`RUNTIME_WS_NODE_OWNER_ENABLED`、`RUNTIME_WS_NODE_OWNER_NODE_ID`）：

```yaml
runtime:
  ws_node_owner:
    enabled: false
    node_id: "" # 开启时填写唯一且稳定的node-a等标识，1–64个小写字母/数字/-/_
```

开启的节点仅支持原生池化WS ingress，不接受WS HTTP bridge或passthrough，包括首帧为store=true的这些模式，以免后续切到store=false绕过门禁。HTTP Responses接口和其tenant-owner不受此开关影响。此功能约束原生ingress中实际store-disabled的`previous_response_id`续接；不承诺所有隐式session、工具状态或HTTP转WS传输都获得相同保护。

| 开关 | 原生池化 | WS HTTP bridge / passthrough |
| --- | --- | --- |
| 关闭（默认） | 保留旧续接/恢复策略，不读写node-owner marker | 保留原模式，可正常完成mock回归；不执行node-owner检查 |
| 开启 | 对store-disabled续接执行node/boot/连接归属检查 | store=true和store=false首帧均在上游发送前拒绝，不降级到其他模式 |

因此“桥接/透传保持原行为”只描述关闭状态，开启后是明确的能力拒绝，不是这些模式已获得owner保护。

服务实例首次使用时生成随机boot代次，与稳定node ID及原连接ID的SHA-256摘要组成内部v1 owner token；进程重启/重新构造服务会产生新代次。原生store-disabled响应成功后，以group、API Key、账号和response摘要为作用域发布Redis marker，TTL沿用现有response sticky TTL。声明使用原子compare-and-set：同owner可续期，另一node/boot或同boot的不同连接不能覆盖未过期owner。记录不包含客户正文、API Key字符串、明文连接ID、上游密钥或节点地址，不向客户端返回owner token。

续接必须同时满足共享marker匹配当前node/boot、API Key/账号/group作用域匹配、本地response→conn绑定存在，并实际获取/保留同一个池连接；Redis可达或账号粘性命中本身不构成准入。缺记录、旧boot、不同node/Key/账号/group、版本不匹配、Redis故障、本地绑定过期或原连接丢失均保守关闭。此门禁不替代已有鉴权、调度资格、计费和并发检查，不能把owner token当用户授权。

受保护路径在解析前核对显式previous ID；不允许handler或parser剥离ID后静默重建，连接探活失败及relay失败不自动重试/切账号。owner错误以固定WS1008返回，不输出owner/response信息，也不处罚上游账号。发布marker发生在现有AfterTurn完成之后；若模型已完成而marker发布失败，仅关闭连接，不重放、不重复调用或重记账。不要让客户端把该错误当成自动重放已完成请求的指令。

开启前先保证全节点都具备新适配层并为节点设置不同稳定ID；存量未带marker的链会被拒绝，建议单独原生WS灰度范围、先排空并由客户端显式开启新会话。缺新cache能力时首个store-disabled请求也拒绝，不用本机memory降级。关闭开关恢复旧行为；marker按TTL自动清理，不改DB schema、不需要删除生产数据。

新增config、仓储、service及原生WS假上游回归源码，覆盖相同boot继续两turn、未知owner在上游dial前拒绝、不同node/boot/Key/账号/group、marker过期及local conn丢失、失败脱敏、owner拒绝不进入failover/账号惩罚。**本轮均未运行，等待远端CI。** 仍未提供原节点发现、签名owner URL、跨节点转发/迁移、自动重建、生产网络故障或真实两机验收；此项是安全拒绝契约，不是“无损跨节点续接完成”。

补充的真实ingress回归仍只连接本机httptest WS和假上游：默认关闭的原生两turn；同node/boot存活连接成功；foreign boot、本地绑定丢失、绑定尚在但池连接失效、cache错误均零上游发送且不产生成功AfterTurn；模型成功后marker发布失败只有一次生成和一次成功AfterTurn。额外的模式矩阵覆盖两种非目标模式×开关开/关×store=true/false：关闭时实际完成假上游响应，开启时零dial/零HTTP发送/零回调。这里验证的是调用与回调计数，不是实际账本/支付集成验收；无生产DB或真实模型。连接获取前已进入turn准入的失败允许一次用于释放槽位的失败AfterTurn，不能当作成功计费。

## BPS临时图片/附件：本节点owner origin

原生BPS附件上传返回上游file ID，不由本节点提供文件下载。异步生图任务已有共享Redis的UserID/APIKeyID授权记录及S3兼容存储；本轮保持这些路径。另一个可选relay模式把输入图片临时写入本机私有目录，并由本进程索引/HMAC生成不可猜测的下载capability。两个节点共用全局图片BaseURL时，链接可能落到没有该文件的节点；共享Redis本身不能解决这个问题。

新增可信进程配置，默认空沿用既有全局图片BaseURL：

```yaml
runtime:
  image_relay_owner_origin: "" # 可填 https://node-a.example.invalid，必须直达生成节点
```

环境变量`RUNTIME_IMAGE_RELAY_OWNER_ORIGIN`（Compose须自行显式传入service）只接受HTTPS origin，可带端口或末尾单个斜杠，不接受userinfo、路径、query或fragment。它不采用请求Host/Forwarded头，不做DNS探测/修改，不自动配置TLS或开防火墙。接入方需保证该origin的DNS/TLS和现有BPS图片GET/HEAD路由直达当前节点；配置同一个轮询域名无法保证owner。

只有relay已启用且为relay模式时覆盖生成URL的origin；不改变全局设置或开启功能，不绕过现有全局settings有效性校验。全局relay BaseURL仍需是合法HTTPS origin，以兼容没有本节点覆盖的旧reader。新进程重复刷新共享配置时保留本节点origin。已有serverless的签名owner query装饰继续保留，不新建经旧节点转发正文的路径。

图片字节、HMAC、capability作用域、下载上限及过期规则不改，API Key字符串/客户正文/节点凭据不加入URL。只有已鉴权的BPS请求可以创建临时图片；下载URL本身是bearer capability，外部上游不携带用户Key即可取图，不能把它当普通公开静态目录，也不应记录或传播完整链接。别的节点和进程重启后没有对应索引/密钥，旧capability返回404，不自动回流旧机、不迁移文件。修改进程配置涉及既有重启/排空流程，本轮未执行。

新增离线源码回归覆盖两套独立relay分别生成各自origin、正确owner GET/HEAD及字节、外节点/篡改链接404、不同API Key作用域capability不同、共享设置刷新不覆盖本节点origin、重启旧链接404、默认空配置/禁用/native兼容。保留原有relay到期/存储上限回归。**新增测试未运行，待精确提交远端CI；本机只做非编译静态检查。** 此功能只提供临时relay直达owner URL，仍未统一所有图片/附件的存储、鉴权、生命周期或跨机恢复。

runtime覆盖与既有relay设置复用同一纯语法HTTPS origin校验，不做DNS解析或网络探测；两者均拒绝编码路径。另有双relay到期源码回归：通过锁内调整实际owner条目的到期时间，断言清理worker尚未运行时GET/HEAD已404且释放存储，另一relay的有效capability仍200，无sleep或生产请求。此补充同样未运行，不能计作实测通过。

owner origin的端口必须处于1–65535，显式空端口与控制字符拒绝；Go复用既有无网络URL格式校验，错误不回显输入。应用与Python离线清单读取同一组脱敏origin样例回归，防止一边接受、另一边拒绝这些已知边界。此收紧可能令此前错误端口配置在启动时失败，应先修正脱敏部署清单，不能用预检成功证明地址可达。

## 可选运行日志节点归属

普通双机可以配置`runtime.node_id`（`RUNTIME_NODE_ID`），默认空保留现有日志格式。该值是1–64个小写字母/数字/-/_的opaque标识，不要用域名、地址、账号或凭据；开启WS node-owner时两处node_id须相同，避免诊断和安全归属相互矛盾。

正常启动读取配置后，结构化日志添加`runtime_node_id`、`runtime_role`和本进程随机UUID `runtime_boot_id`，同进程重配日志保持boot不变，进程重启则改变。bootstrap尚未取得配置时不加字段；zap/slog/stdlog/legacy的共用logger链和直接Ops sink事件都沿用进程归属。默认不加，不修改HTTP/SSE响应或公开health内容，不增加网络请求/磁盘状态文件。

这些是诊断标签，boot与WS owner签名代次独立，不能用日志字段授权会话或证明真实出口/流量比例。它们只帮助区分哪个配置节点/进程产生事件；共享日志查询仍应结合现有request ID、时间和终态。没有为账本增加计费字段，也未改变日志正文或现有采样规则。额外字段开销未测。

新增配置/env、logger adapter、JSON/slog默认兼容/boot稳定与direct sink标签回归源码，运行交精确head CI。本机仅静态检查，不把回归源码当执行结果。启用该进程配置需按既有变更/排空流程执行，本轮没有修改生产配置或重启。清空node_id或回滚恢复旧日志标签；不涉及DB迁移、媒体存储或透明重放语义。

## 共享鉴权失效订阅重连

API Key鉴权的本机正/负L1缓存依赖共享Redis Pub/Sub失效通知。go-redis会在同一次订阅调用内部自动重连；只读消息的通道不会把重新订阅确认交给服务，断线间错过的通知可能在恢复后留下旧L1。

适配层现在读取消息及订阅事件，每次服务端确认该失效频道订阅时通知服务。服务推进本机L1代次，旧正/负条目不再命中，后续按既有L2/数据库路径重新获取；首次确认也清理订阅前条目的逻辑有效性。条目仍受原TTL/容量限制自然淘汰，不并发调用不安全的Ristretto.Clear，不更改共享Redis键/快照版本/权限或失效outbox协议。

此行为只改善**订阅恢复检查点之后的已有L1**，不是断线期间即时撤销、消息重放或全链路线性化授权。已在途鉴权/回源仍遵循现有并发和TTL边界；L2/outbox及数据库不可用策略不改，重连后可能出现有界缓存回源增量，性能未测。订阅health只是最近确认/调用生命周期，不承诺瞬时断连检测。没有L1或使用regional auth的部署沿用既有路径。

新增源码回归覆盖同一长订阅调用自动重连后再次确认与正常接收消息、正负L1的首次确认/重确认逻辑失效、恢复后缓存和普通单key失效。适配层回归使用临时miniredis并强制连接断开/恢复，不接生产Redis；本轮未执行任何回归，等待精确head远端CI。无新开关/数据库迁移，回滚恢复旧重连缓存行为，不撤销已有授权状态或修改账本。真实两机撤销、分区中的权限延迟与计费并发整体验收仍待完成。

## 共享并发：独立客户端竞争回归

`concurrency_shared_clients_integration_test.go` 的 `TestSharedAccountAndUserConcurrencyAcrossIndependentClients` 补充 account/user 并发槽位的真实 Redis 适配层竞争回归。两个独立 go-redis 客户端及连接池共用隔离测试命名空间，并校验连接 ID 不同；同步起跑后以不同虚构 request ID 争抢同一容量为 1 的资源，要求恰一成功、两端计数一致、败者持续被拒绝，胜者释放后败者可准入，最后两端计数归零。

此测试不改 Lua、TTL、准入策略、配置或账本，区别于已有 API Key 竞争与同客户端串行启动清理回归。依赖现有 Docker Redis 集成 fixture，适用 `integration` build tag；测试本身不读取应用配置或生产数据，取消/等待和清理均有时间边界。

本机是生产宿主，仅新增源码并做非编译静态检查，**此回归未编译/运行，等待新精确 head 远端 CI**。两个适配器在同一测试进程内，不能冒充真实两主机、进程崩溃、网络分区、跨请求计费或完整业务故障验收；不提供透明续接或媒体持久化。

## 鉴权失效：独立订阅者传播回归

`auth_invalidation_fanout_integration_test.go` 的 `TestAuthInvalidationFanoutPreservesIndependentSubscriberLifecycle` 使用隔离 Docker Redis、三个独立 go-redis 客户端和公开 `APIKeyCache` 发布/订阅方法。它检查同一虚构 hash 向两订阅者传播；显式取消并等待一端退出后，另一端继续接收；原客户端重新订阅后，两端均收到新事件。订阅数量取自真实 Redis，各订阅和客户端在测试结束时收尾；不接应用配置、数据库、模型或生产 Redis。

这与单适配器自动断线重连、service 模拟 ready 回调及共享并发 A1 是不同的验收范围。恢复步骤是显式重订阅，不是自动重连、历史重放或节点迁移；没有验证 `APIKeyService` 热 L1、数据库/outbox 和中间件授权的组合链路。Pub/Sub 通道在 Redis 内全局可见，因此该集成用例串行执行，不以更换 Redis DB 冒充通道隔离。

此回归仅 `integration` 标签，本机未编译/执行，等待新精确 head 远端 CI；非 `-v` 包结果不能证明单项未 skip，不代表真实两机撤销或故障验收。没有运行行为、共享消息格式、TTL、配置或账本变更。

## 公开鉴权与热L1：真实Pub/Sub组合回归

`internal/integration/authcache/l1_pubsub_integration_test.go` 的 `TestIndependentAPIKeyServicesInvalidateHotL1ThroughRealPubSub` 连接两个独立 `APIKeyService`、真实 `APIKeyCache` 适配层及独立客户端，只使用临时私有 Unix socket Redis 和虚构查询源。它通过公开 `GetByKey` 及查询源计数确认正/负 L1 均已热命中；变更虚构源后先确认旧缓存结果仍在，再发布两个真实 hash 失效事件，要求两端重新读取并缓存新结果，同时不相关 Key 保持热命中。

L2 显式关闭，正负 TTL 为 120 秒且大于 30 秒测试上下文，不靠 L2 回读或自然过期替代失效传播。测试不访问私有缓存字段、不增加生产测试接口；订阅在关闭客户端前停止并等待，临时 Redis 被终止并等待，有限 L1 缓存随独立测试 binary 退出释放。该目录仅在 `integration` 标签下参与 CI，使用既有 workflow 安装的 `redis-server`；缺工具在 CI 中失败。

本机只做源码及非编译静态检查，**此组合回归未编译/运行**，等待新精确 head CI。它验证公开查询与缓存传播，不包含 HTTP 中间件准入、数据库/outbox、在途请求撤销、自动重连丢消息、真实两进程/主机、计费或模型；`GetByKey` 返回未知 Key 不能冒充完整业务撤销验收。没有产品策略、运行行为或媒体/透明重放语义修改。

## DB/outbox/L2与HTTP鉴权：第一遍撤销组合回归

`internal/integration/authoutbox/http_revocation_integration_test.go` 的 `TestDatabaseOutboxFirstPassRevokesTwoHotHTTPReaders` 使用本测试独占的 Docker PostgreSQL/Redis、真实迁移、API Key/outbox 仓储、worker、共享 L2、两个独立 service/client 和公开 HTTP 鉴权中间件。真实 worker 按整表领任务，独占数据库可避免消费其他集成测试的事件；测试不新增生产过滤接口。

测试启用 L1/L2、TTL 长于测试时限，并用真实仓储及 L2 读取计数同时证明两端 HTTP 查询命中热 L1。数据库停用 Key 后、worker 启动前，HTTP 和共享 L2 仍明确保留旧状态；第一遍实际删除 L2 并广播后，两端新请求返回 `401/API_KEY_DISABLED` 且不进入固定下游 handler，共享 L2 回读为停用状态，无关 Key 仍热命中。HTTP fixture 只在本进程注册现有只需认证的 `GET /v1/sub2api/billing` 路径和固定 204 handler，不调用真实倍率、模型、账本或运营接口。

本次只验证第一遍及真实 stage 1 延迟调度，随后停止 worker；不加速默认安全第二遍，也不声称最终 ack、故障重试、在途撤销或真实两机验收。没有 schema、权限、TTL 默认值、worker 策略或生产配置变更。仅 `integration` 回归源码，本机未编译/运行，等待新精确 head 远端 CI；有限 L1 随专用测试 binary 退出，worker/订阅、客户端和独占容器按序清理。

同一独占 fixture 另有 `TestDatabaseOutboxSafetyPassClearsLateSnapshotFromTwoHotHTTPReaders`：保留第一遍全部断言，再明确将变更前保存的 active 快照回填共享 L2，模拟旧查询延迟完成。这是测试故障注入，不是真实并发 SQL 竞态或新运行行为。停止旧订阅后创建两个新 service/连接；两端必须从旧 L2 读取并真正热缓存为 HTTP 204，真实数据库查询计数保持零，且无关 Key 也保持热。

测试核对原 `available_at` 完全未变、两端在截止前已热，然后启动真实 worker，等待既有 30 秒第二遍调度；不更新 `available_at`、不改时钟/延迟或手工 claim。第二遍实际删除 L2 并广播后，两端新请求最终 `401/API_KEY_DISABLED` 且未进入下游，数据库重新读取为停用快照，无关 Key 继续热命中；真实 stage 1 行最终 ack 删除，完成时间不得早于原数据库截止。

第二遍场景复用原 fixture helper，原第一遍测试仍独立保留。新增场景至少增加约 30 秒等待及独占容器成本；45 秒有界条件等待用于观察第二遍，整体仍为 120 秒上下文，TTL 300 秒，不依赖自然过期。新回归本机未编译/运行，只做非编译静态检查，结果待新精确 head CI。新旧 service 均在同一测试进程；不证明在途授权线性化、迟于第二遍的再次旧回填、故障重试或真实双机恢复。

`TestDatabaseOutboxRealRedisDeleteFailureRecoversThroughSafetyPass` 另在独占 Redis 创建仅供该场景 worker 使用的 ACL 身份，真实服务器拒绝其 `DEL`；controller、reader 及订阅使用其他连接且继续正常。测试校验实际 worker adapter 收到 `NOPERM`，事件仍处于 stage 0、attempts 增加、claim 已释放、错误保留，L2 和双 HTTP 热缓存仍为旧 active；不是 fake cache 返回故障。随后只恢复该 fixture 身份的 DEL 许可，新真实 worker 按原退避及数据库重试时间重领，保留 attempt 历史并清理错误，再走既有第二遍场景至双 HTTP 401、control 保持热和最终 ack。

故障只作用于测试专属 Redis 的 worker 用户，不修改默认用户、不停 Redis、不访问生产，也不改 `available_at`、原退避或 30 秒安全调度。新增场景只测 DEL 权限拒绝及恢复，不代表 Publish 失败、网络超时或所有 Redis 故障。复用独占 fixture 及清理顺序，worker 停止后关闭专用客户端并删除测试 ACL 用户；整体 120 秒上下文，故障观察 5 秒、恢复第一遍 15 秒、第二遍 45 秒。此 `integration` 回归本机未编译/运行，动态证据待新精确 head CI，不变更运行策略、schema、账本或产品行为。

`TestDatabaseOutboxRealRedisPublishFailureAfterDeleteRecoversThroughSafetyPass` 另以同一隔离机制仅拒绝 worker 的真实 `PUBLISH`。它必须先证明真实 DEL 已成功、共享 L2 已不存在，而两端热 L1 仍返回旧 HTTP 204；真实发布收到 `NOPERM` 后事件不能 ack，attempt/claim/错误状态仍可重试。恢复该身份的 publish 权限后，新 worker 沿原退避、原 30 秒第二遍完成双端 401、control 热命中和最终 ack。此部分成功状态不同于 DEL 被拒绝，也不同于 fake publishErr 分支；旧正常、第二遍及 DEL 场景全部保留。

此新场景仅新增测试与文档，本机未编译/运行，动态结果交新精确 head CI；预算与独占 fixture 清理沿前述故障场景，不改数据库调度时间、运行策略或账本。它只覆盖 Redis 权限拒绝型 PUBLISH 失败，不证明网络中断/超时、claim 后进程崩溃/租约接管、透明会话恢复、媒体生命周期或真实双机验收。

`TestDatabaseOutboxReclaimsCrashedClaimAfterOriginalLease` 另以同一测试 binary 的隔离子进程调用真实 outbox 仓储 `Claim`，在事务已提交领取、尚无 Redis 副作用的检查点阻塞。父端核对数据库 event/owner/claimed_at，确认原 30 秒租约内另一真实 Claim 为空后，实际终止并等待子进程；claim 不会被优雅释放或改时间。fixture 连接信息只经匿名 stdin 管道传入，不放 argv、环境变量或普通日志，回执只含虚构事件身份和固定错误。

新的真实 worker 必须等原租约到期才处理同一事件；接管后的 attempts 保持 0、旧 owner 不能 ack，新 stage 1 继续按原 30 秒安全调度，最终双 HTTP401、control 热命中和真实 ack。场景复用原第二遍故障注入与全部断言，额外租约等待上界 45 秒、第二遍上界 45 秒，整体仍为 120 秒上下文；不缩短 lease 或修改 `claimed_at`/`available_at`。缺 Docker 在 CI 失败，Windows 因继承描述符限制明确 skip；子进程在失败路径也会终止、等待并关闭管道。

此新增 `integration` 场景本机未编译/运行，结果待新精确 head CI。子进程只负责真实领取检查点，并非完整生产 worker，也不证明未提交 Claim、发布后/ack 前崩溃、网络分区、全部租约竞态、授权线性化或真实双机。没有运行策略/schema/账本、透明会话或媒体语义变化。
