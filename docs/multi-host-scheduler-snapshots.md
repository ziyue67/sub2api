# 多主机调度快照职责

`runtime.scheduler_snapshot_mode` 支持 `auto`（默认）、`writer`、`reader`，也可通过 `RUNTIME_SCHEDULER_SNAPSHOT_MODE` 设置。默认 `full` 节点保持原有行为；默认 `gateway` 节点只消费共享 Redis 或已配置的 regional 调度快照，不再执行启动重建、outbox 轮询、全量重建和 regional 调度快照发布。`gateway + writer` 配置会被拒绝。

多副本部署应明确指定一个 `full + writer` 作为生产者，其余节点使用 reader；此配置不提供 writer 选主，也不会阻止两个 full writer 同时启动。现有缓存桶 epoch 和租约继续提供写入 fencing。若要切换生产者，先停用原生产者任务，再启用新生产者；本修改不包含线上切换。

reader 的候选列表、单个账号和批量完整账号读取允许缓存命中。缓存缺失、损坏或不可用时返回不可用，不捕获发布 token、不重拉账号库、不回写完整列表，即使旧配置仍启用了 `gateway.scheduling.db_fallback_enabled`。regional reader 的不可用、过期及缺失继续沿用其既有拒绝服务行为。空命中的合法候选列表仍可返回空列表。

reader 并非整个应用的数据库只读副本：鉴权、账本、并发、请求引起的账号冷却/限流反馈仍按既有路径执行。`UpdateAccountInCache` 保留即时账号状态反馈，避免禁用/限流反馈延迟。共享 Redis reader 的分组详情仍显式查询数据库；只有配置 regional 详情 reader 时才从区域快照获取分组。本修改不会把所有跨机查询消除，也不改变请求最终出口。

生产者必须先完成首轮快照，再让 reader 接流；reader 不会自动用数据库补齐。默认 `/readyz` 的 DB/Redis 连通检查不等于快照已存在或足够新鲜；可显式启用下文的有界桶发布门禁。接流前还需验证目标分组/平台候选桶和账号完整缓存均命中；撤掉候选桶或完整账号缓存时请求应失败且不产生账号数据库重拉。下文门禁仅覆盖指定桶的发布年龄与metadata/账号详情缓存存在性，不等于完整业务健康或可自动切流的多主机部署。

回滚源码或显式恢复 full writer 可恢复原有行为；恢复 writer 前应确认旧生产者已停用。不要为了消除不可用错误把 gateway 改成 writer 或放开数据库兜底。完整验证由 CI 执行；就绪门禁另有获授权的离线定向 RED/GREEN 和隔离真实 Redis 适配层检查，不能视为线上双节点验收。

## 可选的 gateway 快照就绪门禁

本次新增 `runtime.scheduler_readiness`，默认关闭；只有开启后 `/readyz` 才在DB/Redis检查之外验证显式调度桶。此功能用于共享Redis reader，配置 regional scheduler 的预览节点不能启用，既有区域业务准入503不变。

```yaml
runtime:
  role: gateway
  scheduler_snapshot_mode: reader
  scheduler_readiness:
    enabled: true
    buckets: ["17:openai:single", "17:openai:forced"] # 虚构分组，按实际承接范围填写
    max_age_seconds: 900
```

对应环境变量是 `RUNTIME_SCHEDULER_READINESS_ENABLED`、`RUNTIME_SCHEDULER_READINESS_BUCKETS`（逗号分隔）、`RUNTIME_SCHEDULER_READINESS_MAX_AGE_SECONDS`。Compose需要显式将变量传入service；此变更没有自动开启任何部署。最大年龄0取900秒，最大86400秒；开启需要1至16个不重复的桶。必须覆盖该节点接流的范围；只检查一个桶不能声称所有用户/模型都就绪。

发布者在快照active版本切换的同一Lua中记录generation与Redis时间。拒绝的旧写者和同generation重复激活不刷新时间；无需依赖两台服务器的时钟。只有配置桶的ready/active/发布generation匹配、发布时间在窗口内、未退休、成员集合非空且每个成员的metadata及完整账号详情缓存均为非空Redis string时才通过。检查至多4096候选/桶，超限保守不就绪；不读取完整账号JSON、不拉SQL、不修改水位或触发重建。检查在Redis中原子执行，返回固定状态，不输出账号/凭据或Redis错误。公共not-ready响应仍是通用503，`/health`不受影响，既有drain立即not-ready不变。

这里的“完整”仅指候选metadata/账号详情缓存存在与类型；它不校验账号JSON内容、资格或代理可用性。空快照在当前reader中本来被视为miss，门禁也拒绝，不表示空分组本身数据损坏。

兼容顺序：先升级full writer，再等待至少一次完整目标桶发布，最后给gateway开启门禁。旧writer没有generation时间戳；旧writer覆盖active后也会与旧stamp不匹配，此时门禁失败而非假就绪。升级期间不要在旧writer上启用reader门禁，也不要为了恢复200关掉鉴权/并发检查。full默认readiness契约不变，缓存主体格式和数据库schema不变；reader回滚前先按入口排空流程撤流。关掉新门禁会恢复旧readiness语义，不会修复缺失或陈旧快照。

最大年龄应超过正常完整重建周期及其时长。默认完整重建300秒，门禁默认900秒；关闭完整重建且桶长期无事件时会按设计过期，需要部署者明确策略。这是**桶发布年龄**，不是outbox消费进度/账号即时撤销新鲜度；仍缺少outbox滞后、失效订阅状态、完整账号可解码性、本机上游依赖等健康契约。

就绪门禁及下文隔离故障回归不等于生产多主机验收。HTTP健康探针撤流不能撤回客户端缓存的DNS结果，已有业务请求也不会仅因readiness503被强制取消；DNS多A不自动解决WS/Responses续接或BPS图片归属。真实双节点故障/恢复仍需单独演练，会话与资源owner契约也尚未实现。

## 离线双进程故障/恢复回归

`backend/internal/server/scheduler_multinode_recovery_test.go` 使用两个独立的测试子进程模拟publisher与gateway reader，共享一个关闭TCP/持久化的临时真实Redis。publisher经实际仓储的epoch/token API发布虚构账号，reader经实际`SchedulerSnapshotService`和`ProvideLifecycle`提供HTTP。reader的数据库仅mock Ping，账号仓储为nil，不接SQL或上游；虚构业务仅检查候选与账号读取并返回固定标记。

测试入口是仅用于验收的单目标HTTP代理：显式采样reader的`/readyz`，503时拒绝转发，200时再纳入；断言摘除后转发计数不增加，恢复后的请求到达原reader。它不交付入口产品、不提供第二健康目标的兜底，也不代表实际DNS健康摘除策略。探针采样与转发之间仍有真实系统普遍存在的竞态，测试按阶段串行故障注入，不宣称原子路由。

覆盖链为：冷启动不纳入→发布后纳入→实际终止publisher进程并等待两秒发布窗口过期→摘除→新publisher进程重新发布→恢复；再逐项删除候选集合、metadata与完整账号缓存，验证摘除且reader不自行发布，publisher补齐后恢复；退休后旧token发布被拒绝，reopen未发布时仍不纳入，重新发布后恢复；最后reader drain立即摘除且health继续成功。退出会清理全部隔离子进程。

单项执行（Linux/Unix，需要`redis-server`，CI已准备；无工具或Windows会明确skip）：

```sh
cd backend
GOMAXPROCS=1 GOFLAGS=-p=1 GOMEMLIMIT=700MiB GOGC=20 \
  go test -tags=unit ./internal/server \
  -run '^TestGatewaySnapshot(MultiProcessFaultRecovery|NodeProcess)$' -count=1 -v
```

这是隔离进程间的契约回归，不是两台真实主机、生产PostgreSQL、真实模型、自动writer选主或生产流量分配验收。尚未覆盖跨主机网络隔离/抖动、DNS缓存、会话续接、图片owner、真实LB供应商策略、生产规模探针开销。真实环境演练仍须单独授权、限定流量并保留撤流回滚路径。
