# Kubernetes / K3s 请求副本

## 正式版升级脚本

从包含此脚本的正式版本开始，Release 页面会自动提供对应 tag 的升级命令。在已有单节点 k3s 主机执行：

```bash
release_tag=vX.Y.Z # 替换为实际发布版本
curl -fsSL "https://raw.githubusercontent.com/ranxi2001/sub2api/$release_tag/deploy/upgrade-k3s.sh" | sudo bash -s -- --version "$release_tag"
```

要求 root、Python 3.9+、curl 和 k3s；默认 namespace `tosky-canary`、Deployment `tosky-nerd`、容器 `sub2api`，可通过 `--namespace`、`--deployment`、`--container` 覆盖。仅支持已有单节点、单副本 Deployment，保留 Secret、PVC、资源、角色和更新策略。**该升级脚本不支持本目录的 StatefulSet；不要把其默认 namespace/Deployment 套用到请求副本模板。**`--dry-run` 只读解析版本与工作负载，不拉镜像或更新 Deployment。

脚本自动解析稳定 Release 的完整 commit、匹配架构的 GHCR digest，验证标签并预拉取。临时容器不挂载生产数据，只检查二进制；同镜像初始化容器及 `verify-runtime` 的二进制摘要一起更新。相同版本和校验值时只验收，不强制重启。

原模板备份到 `/var/backups/sub2api-k3s/` 的受限目录；更新前做 server-side dry-run，更新和回滚均校验模板与 resourceVersion。rollout、readiness、运行 imageID 或版本校验失败会恢复旧模板；若其他人已修改模板则拒绝覆盖并报告备份路径。备份可能含部署配置，不应公开。

脚本只更新执行所在的 k3s 工作负载，不修改主站、不发起模型请求、不升级独立 Worker。先阅读目标 Release 的迁移和 Worker 兼容说明；所有共享 Redis 的实例需统一更新，再跨周期重建检查候选成本。旧副机回滚可能重新引入缺字段问题。v2.9.6 及此前 tag 没有此入口，不回填历史 Release 的不可用脚本链接。

本目录适用于包含 runtime.role 与 /readyz 的构建。旧版本（包括 2.9.4）不支持这些契约，不能只添加环境变量就作为 gateway 节点部署。示例镜像标签故意不可直接使用；部署前必须替换为经过验证、包含 gateway 启动门控及 scheduler snapshot reader 改动的 owner fork 镜像摘要。旧镜像会忽略未知环境变量，固定 digest 本身不能证明版本具备这些能力。

## 部署职责

- 主实例使用 `runtime.role: full`（默认值），保留管理后台、支付、巡检、通知投递和全局维护任务。可以继续使用原来的 systemd / Compose；本示例不搬迁数据库或原主站。
- 请求副本使用 `runtime.role: gateway`，暴露模型网关与健康检查路由，不注册后台、用户面板、支付或重登 Worker 的管理接口。需要通过主站修改配置。
- 网关节点仍运行请求计费、审计、缓存同步、账号事件落库/自动配置、请求触发的 Ollama 检测以及本机连接池等必要工作。不能简单停掉所有 goroutine。BPS warm pool 仍逐进程运行，PR #531 通过窄字段查询减少其周期读取，不是全局禁止 BPS 预热。
- 定时扫描、主动监控、普通账号计划检测、备份、版本轮询、全局额度维护、批量图片消费者和 BPS 403 周期恢复只由 full 节点启动。账号运营事件由请求副本入库，通知由 full 节点投递。
- 模板显式配置 `RUNTIME_SCHEDULER_SNAPSHOT_MODE=reader`；唯一 full 节点使用 `writer`（或对应版本的 `auto`），先确保其已发布完整调度快照。reader 缺失快照应拒绝调度，不回源重建。该配置不会消除请求鉴权、计费等必要的 PostgreSQL/Redis 往返。
- 保持一个 full 实例。此开关没有将所有旧后台任务改造成带租约的多主调度器，不应横向扩容 full 实例。

多个副本必须连接同一套 PostgreSQL、Redis DB 与配置，并使用一致的 JWT、TOTP/其他加密密钥。不为每个 Pod 建独立余额数据库或并发缓存。跨地域连接使用受限加密网络；本目录不开放数据库公网端口，也不建立跨国 Kubernetes 控制面。

## 应用生命周期

```yaml
runtime:
  role: full                 # full / gateway；未知值拒绝启动
server:
  graceful_shutdown_timeout: 5  # 秒；默认保留原行为，0 也取 5 秒
  shutdown_drain_delay: 0       # 秒；默认 0
```

对应环境变量为 `RUNTIME_ROLE`、`SERVER_GRACEFUL_SHUTDOWN_TIMEOUT`、`SERVER_SHUTDOWN_DRAIN_DELAY`。timeout 范围 0–3600，drain delay 范围 0–300。需要通过 Compose 使用时显式加入 service.environment，宿主 .env 文件本身不会自动进入容器。

- `/health` 用作 liveness，只反映进程 HTTP 服务存活。
- `/readyz` 用作 readiness：正常模式检查 PostgreSQL 和 Redis。默认共享 1 秒预算；使用远程共享依赖的 gateway 可将 `server.readiness_timeout_seconds` 设置为有界值（例如 3），依赖不可用时仍返回 503，不返回内部错误或凭据。
- 未完成配置的 setup 模式对 /readyz 返回 503，避免把前端 SPA 的 200 当成准备就绪。
- SIGTERM 到达后立即撤销 readiness，新业务请求返回 503/Retry-After，已有处理器继续运行；先等待 drain delay，再在 graceful timeout 内排空 HTTP 和仍在处理中的 hijacked/WS handler。到期后进程进入关闭流程。
- 示例为 10 秒撤流等待、240 秒请求排空、320 秒 Pod 宽限期。宽限期要覆盖两个阶段和后台清理余量。更长的流需要更大的预算；本功能不保证无限长连接不中断，也不会将已经输出的请求自动重放到另一个 Pod。

`server.readiness_timeout_seconds` 支持 YAML 或 `SERVER_READINESS_TIMEOUT_SECONDS` 环境变量，重启生效；省略或设为 `0` 保持 1 秒，显式值允许 `1..60` 秒。这是 PostgreSQL 与 Redis 顺序检查的总预算。先测量真实依赖操作的耗时，再决定是否调整；仅连接 SSH 本地转发监听端口的耗时无法反映远端数据库往返。

例如依赖检查通常需要 1–2 秒时，可以为相应 gateway 显式配置 3 秒，并给 kubelet 留出余量：

```yaml
env:
  - name: SERVER_READINESS_TIMEOUT_SECONDS
    value: "3"
readinessProbe:
  httpGet: {path: /readyz, port: http}
  timeoutSeconds: 5
  periodSeconds: 5
  failureThreshold: 2
```

`readinessProbe.timeoutSeconds` 必须大于应用探测预算；只修改 kubelet 的超时不会改变应用内的预算。Compose 部署需在 `service.environment` 显式传入环境变量。调大预算会延长依赖故障时的撤流等待，不能用来掩盖持续故障；本配置不改变 scheduler 重建或 outbox 超时。内部调用者（例如 Serverless heartbeat）自身更短的 deadline 仍优先。`/health` 的存活检查、draining 时立即返回 503 和 setup 模式行为保持原样。

## 配置和存储

本目录统一使用 `sub2api` namespace（离线渲染可覆盖）、`sub2api-gateway` StatefulSet 和同名聚合 Service。模板默认 **0 副本**，防止 apply 后在配置和入口验收前直接启动后台连接；支持显式扩为两个 gateway Pod，每个 Pod 有稳定实例名和独立的 10 GiB RWO PVC。没有把两个 Pod 同时挂到一个本地数据目录。K3s 可使用默认 local-path，其他集群使用已配置的默认 StorageClass；local-path 不能使数据跟随 Pod 跨主机迁移，也不构成存储高可用。拓扑分布是软约束，单节点 k3s 同样可以调度两个副本。

先在受限运维目录准备原部署的完整 config.yaml，只调整审查过的数据库/Redis网络地址；不要把真实配置提交到 Git。

```bash
kubectl apply -f deploy/kubernetes/namespace.yaml
kubectl -n sub2api create secret generic sub2api-runtime-config \
  --from-file=config.yaml=/secure/sub2api/config.yaml
kubectl -n sub2api create secret generic sub2api-credential-key \
  --from-file=credential-operations.key=/secure/sub2api/credential-operations.key
```

credential key 必须来自原实例。初始化容器把同一密钥复制到每个 PVC，目标文件 owner=1000、mode=0600；目标已存在但内容不一致时拒绝覆盖，不能靠重新生成密钥解决启动失败。Secret 的 group-readable 投影不能直接替代程序要求的 0600 密钥文件。若原部署只使用固定 TOTP_ENCRYPTION_KEY 而没有 credential-operations.key，沿用该配置，删除示例中的 credential-key 初始化容器与 credential-source 卷，不要临时生成一个不匹配的文件。密钥轮换需单独设计协同更新，不能滚动产生不同密钥。

Secret 的 config.yaml 使用 subPath，更新后需要重建 Pod。PVC 用于本地运行资料、价格缓存等；模板不包含生产账号、代理配置、订阅或私钥。

## 固定镜像与上线

使用离线渲染器统一 namespace、所有容器的固定 digest 和副本数。需要 Python 3 与 Kustomize（或带 Kustomize 的 kubectl）；它只运行本地 `build`/`kustomize`，不读取集群配置、不访问 Kubernetes API、不拉镜像。仅接受 owner fork 的 sha256 镜像，缺省副本数为 0；输出不包含 Secret 内容。CI 的 `release-helpers` 对非法参数、namespace 显式补丁、固定 digest、默认副本数、离线命令与失败不输出半成品做 mock 回归；另用固定版本与校验摘要的真实 Kustomize 验证完整模板的默认/自定义 namespace、两类容器 digest 和副本配置；它不代替集群 schema 验证。默认 0 副本只用于新建暂存；更新已经运行的同名 StatefulSet 时必须显式指定经批准的副本数，否则 apply 会把现有副本缩为 0。

```bash
python3 deploy/kubernetes/render.py \
  --namespace sub2api \
  --image ghcr.io/ranxi2001/sub2api@sha256:替换为实际64位镜像摘要 \
  --replicas 0 > /secure/sub2api/gateway-staged.yaml
```

先固定 image/source SHA，核对二进制版本、镜像 digest 和构建证据；不要在生产机临时编译。直接 `kubectl kustomize deploy/kubernetes` 仍用于审阅基础模板，其占位镜像不可上线。自定义 namespace 时，两份 Secret 也必须创建在该 namespace；不要只修改 workload 而留下其他 namespace 的 Secret。

以下命令是人工审查后的部署示意，**渲染器不会执行**。先创建对应 namespace 和 Secret，才能对后续 namespaced 资源进行 server-side dry-run。0 副本暂存不证明挂载、镜像或依赖可用；启动前还须核对同名 StatefulSet 没有沿用不兼容的旧 PVC/selector。

```bash
kubectl apply --dry-run=server -f /secure/sub2api/gateway-staged.yaml
kubectl apply -f /secure/sub2api/gateway-staged.yaml
# 独立批准启动后，以 --replicas 1 重新渲染并审阅 diff，再应用。
# 扩到 2 副本仍须先验收第一个副本；不要直接修改生成文件里的镜像。
kubectl -n sub2api rollout status statefulset/sub2api-gateway
kubectl -n sub2api get pods,pvc,pdb
```

Service 都是 ClusterIP，不自动创建 Ingress、NodePort、TLS 证书或 DNS 记录：

| Service | 用途 | 会话边界 |
| --- | --- | --- |
| `sub2api-gateway` | 多副本普通无状态请求 | 每条新连接可能命中不同 Pod，不能提供 BPS/WS 所有者粘性 |
| `sub2api-gateway-headless` | StatefulSet 稳定网络标识 | 不是对外入口或健康/流量控制器 |
| `sub2api-gateway-0` | 专属入口固定第 0 个 Pod | 只选择 ready 的 gateway-0，失联返回不可用，不自动迁移到 gateway-1 |

`readinessProbe.timeoutSeconds=5` 高于应用共享依赖探测预算 3 秒；startup/liveness 只访问 `/health`。readiness 仍仅检查 PostgreSQL/Redis，不检查 scheduler snapshot 新鲜度；reader 无快照会 fail closed，必须由 full 先完成预热。readiness 成功不证明账号代理、SSE、图片链接或真实模型出口正常。

## 定向灰度与流量目标

最小接流单位是事先同意的隔离测试 API Key 与其客户端。为 `sub2api-gateway-0` 准备**新机自己的专属 HTTPS 入口**，先做无模型健康检查，再让该测试客户端把 base URL 改为此入口；API Key 仍由现有鉴权校验，TLS 不允许跳过。保持这一个客户端的 HTTP、SSE、WS 与可用的能力下载入口固定到同一 Pod。该方案不在代理配置里存储/匹配真实 Key，也不让旧机搬运请求/响应正文。

专属入口指向 gateway 并不限制其他持有效 Key 的客户端访问；它不是 Key 白名单或百分比路由。若必须在公共入口强制限制 Key 范围，应先实现经过鉴权的节点准入策略；不要靠可伪造的客户端路由头授权，也不要把原始 Authorization 放进 Nginx map、日志或 Git。未完成该策略前只给受控测试客户端交付入口。

不得把 DNS 多 A、随机 Service、复制两份 gateway 配置当成 1%/5% 灰度。当前 Serverless 路由是主站接收正文后转发，可能保持会话但仍消耗主站公网流量；本次扩容目标是降低原机正文流量，应优先让测试客户端直接访问新机入口。既有地区绑定功能见 [Serverless 路由](../../docs/serverless-routing.md)，不能将它描述为通用百分比接流。

共享 group 读取、账本、计费与全局并发仍实时依赖 SQL/Redis，本版本不是全离线本地 gateway。性能收益必须通过独立时间窗实测，不能从模板或窄查询推算“节省 95%”。

开始前记录专属入口→Service→Pod→模型出口的路径，核对旧机 loopback 代理没有被复制成远程回流路径。按独立时间窗对照两机网卡/隧道字节增量、已完成请求数与失败数；入口 ready、TLS 握手耗时不能证明节省公网流量，也不能冒充模型 TTFT。

首轮只测试无续接、无图片、无工具回放的单次请求，并选择不会落到 BPS 的隔离账号分组。完成计费去重/额度一致、取消请求、SSE 正常结束和出口验收后，才增加同意试用的客户端；不根据一次成功请求自动启用所有用户。后续增加实例时为每个状态所有者单独建入口与精确 Pod selector，不能将固定入口改指聚合 Service。

资源和连接池是起始预算：每 Pod requests=500m/768Mi、limits=2 CPU/2 GiB；SQL 最大连接 16，Redis pool size 32。总池容量随 Pod 数增长，需给主站和其他服务留连接余量。不要沿用每实例 256/1024 的默认池后直接大幅扩容。PDB minAvailable=1 只约束自愿驱逐，不是故障接管保证，也不阻止管理员直接删除 Pod。

## 仍需单独解决的有状态路径

- BPS 工具回放、工具目录及 429 冷却仍有进程内状态；图片下载链接使用进程密钥，必须路由到生成该链接的实例。仅使用 ClientIP 会话粘性不足以关联客户端与上游图片下载方。首轮将这些路径留在主站，或者先落实可验证的会话/图片所有者路由。不能只按 URL 判定 BPS，因为相同网关 URL 可能选到 BPS 账号。
- WS response→account 路由已有 Redis 部分，但连接 ID/turn state 仍是进程内状态；重连、续接与缩容需要独立测试。
- 127.0.0.1 的业务代理、托管 Mihomo、292/Keeper 出口、重登运行环境和插件文件都不因共享数据库自动出现在其他 Pod。必须逐项确认出口可达与本地文件的一致性；不要复制主站 loopback 地址后假定能访问主站。
- 批量图片/异步任务若依赖本地上传文件或图片存储，提交端与 full 消费者必须能读到同一任务资源，未验证前留主站。
- 共享数据库/Redis故障仍会影响所有副本；两个应用节点不等于数据库容灾。

启动清理现在只按过期时间回收普通槽位，保留其他进程的活跃槽位和等待计数。故障进程遗留槽位按已有 TTL 到期，启动时不再无条件清空；旧版本实例重启仍执行旧逻辑，因此接入第二个副本前必须先升级主实例。不要通过拆分 Redis DB 来绕过这个问题，否则会拆散全局并发约束。

## 验收与回退

先在隔离 PostgreSQL/Redis 中验证两个进程同时服务、新副本加入不改变旧副本计数、并发准入、计费去重、配置失效、正常 SSE 结束与中途终止、readiness 依赖失败、SIGTERM排空和固定密钥读取。应用健康检查成功不代替这些验证。

回退先让试用客户端停止向专属入口发送新请求，等待现有 SSE/WS 排空并结束状态会话，再将客户端 base URL 恢复主站；不要在进行中的会话里无提示跨节点重放。随后将 gateway StatefulSet 缩容为 0，保留 PVC/Secret 和备份。若回退到旧应用代码，先停完所有请求副本，再按单实例流程恢复主站；不在多个活跃副本间混用旧启动清理实现。本 PR 不涉及数据库迁移或自动部署。
