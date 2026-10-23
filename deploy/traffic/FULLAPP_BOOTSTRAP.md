# 两个完整应用的安全初始化前置

`test_fullapp_bootstrap.py` 是后续模型组合验收的**初始化前置**，不是已完成的模型负载均衡验收。不同于生产 lifecycle 部件 helper，它实际启动两个由本提交 `backend/cmd/server` 编译的 Sub2API 二进制，各自执行未替换的 `main`、`runMainServer`、Wire 全图、配置校验和真实数据库迁移；通过真实 Caddy 双目标入口访问它们。应用没有测试专用 handler 或启动开关。

## 现有缺口与本前置的范围

真实入口 `backend/cmd/server/main.go` 调用 `initializeApplication`，`wire_gen.go` 先 `ProvideConfig`→`ProvideEnt`/`ProvideRedis`，再初始化 settings、定价、router/serverless/IPRisk、插件等完整依赖。`InitEnt` 执行权威 SQL migrations 和系统密钥 bootstrap；现有 external e2e 使用 `BASE_URL` 及外部 Key，未提供能直接复用的双应用隔离启动设施。

生产已经有 `CONFIG_FILE` 明确配置文件路径、`SKIP_SETUP` 和 gateway 角色，因此本轮无需修改应用运行策略。测试补齐下面的安全初始化夹具，而不是调用生产地址或用假 HTTP server冒充完整应用：

- 复用现有隔离 CI 的 PostgreSQL `18.1-alpine3.23` 和 Redis `8.4-alpine` 版本。每轮创建唯一容器名和独占 Docker `--internal` 网络，两应用只连接该网络；镜像在启动前拉取，记录实际 image ID，版本标签不声称不可变 digest。
- 在临时目录生成共享测试 JWT/TOTP、独占 DB 密码及最小 JSON 格式 YAML 配置，权限 0600。不读取已有 config.yaml、环境文件、密钥、数据库或客户记录；不给容器 GitHub token、宿主环境或 Docker socket。
- 为两个真实应用提供各自全新目录、本地合成 `gpt-6.1-sol` 零定价文件、关闭远程定价 URL/hash。空数据库没有模型账号、用户或 API Key，不存在真实模型出口；网络层还阻断应用的外部网络。不会借本地代理连接生产。
- 两个实际二进制容器使用宿主测试 UID/GID、只读根文件系统、移除全部 capability、禁止提权、有限 tmpfs 和进程数；每应用 memory/swap 同为 512MiB。PG 384MiB、Redis 96MiB、Caddy 256MiB，也禁止额外 swap。这些是容器硬限制，不能与 Go 的软内存提示混淆。编译只在远端 runner 单槽执行，未声称编译进程峰值低于 1GiB。
- 应用和 Caddy 不请求或发布宿主端口。专用 Linux Docker runner 从宿主直接访问本轮唯一 internal bridge 的私有 IPv4，断言进程存活、仅附着本轮网络、有地址、无 HostConfig.PortBindings 或实际映射；不接入外部/默认/host 网络、不修改防火墙、不输出地址。取得端点不表示 readiness 成功，后续仍须实际 `/readyz` 返回 200。
- 验证编译 commit、实际容器 entrypoint 为 `/fixture/bin/sub2api`、Args 为空、两个完整 OS PID 不同、实际网络和内存限制一致。临时 fixture 只持有合成配置，不保存 app 日志或 SQL 正文。

## 实际新门禁

独立 `multihost-fullapp.yml` 在 push/PR 从 `backend/go.mod` 固定 Go 版本，`CGO_ENABLED=0 GOMAXPROCS=1 GOFLAGS=-p=1 go build ... ./cmd/server`，固定 commit 写入二进制，再下载并校验已固定 SHA256 的 Caddy v2.11.7。无 embed 前端构建；业务 API 和完整服务初始化仍是实际应用。

标准库 unittest 实际要求：

1. 两个完整应用先后真实迁移/启动，`/readyz` 都返回 200；编译身份、两个 PID、运行入口检查正确。
2. 用原 `Caddyfile.responses` 的路由/health/零重放配置连接两应用。测试副本只增响应目标标记，不改请求重试或健康语义。多次无 Key POST 必须被两个真实应用鉴权中间件分别拒绝为 401，证明路由到两目标；**401 不等于模型请求成功**。
3. 仅暂停本测试的 Redis 容器，两应用实际依赖探测均变 503，Caddy 观察到两目标撤离后入口为 503；恢复该 Redis 后两应用 readiness 和 Caddy 双目标路由恢复。
4. 对应用 A 发送真实 SIGTERM，经过原 `main` 的 `BeginDrain`、配置 drain delay、`Shutdown`、`Lifecycle.Wait` 及 Cleanup。新无 Key 请求收敛到应用 B，A 正常 exit 0、无 OOM，B 仍运行。此时没有已有模型 SSE，因此不声称验证了流保持。
5. 失败路径也取消 pause，按唯一名称逆序清理容器及匿名卷、network，临时配置删除。只保存固定阶段、commit、二进制 SHA、image ID、数值退出/OOM 状态及少量固定诊断布尔值；对本轮合成应用最多读取日志末尾 60 行并只匹配固定错误分类，不导出原始内容，不读其他容器。不导出容器 env/挂载配置、DB 内容、请求体或 app 日志。

仅隔离配置的日志级别设为 info，原 1MiB/单文件上限和不导出原文规则不变。原因是生产 stdlog bridge 的固定级别推断可能把开头为 `Failed ...`、且不含后续 error/fatal 等关键词的 `log.Fatalf` 消息归为 info；error 过滤会使启动错误分类缺失。本修改不触及生产 logger 代码或配置。早退时立即输出 ExitCode/OOM 和固定应用分类；HTTP观察只累计固定 app-a/app-b/edge、ready/responses、状态码及已知目标类别，不存任意响应头、地址、URL或正文。新增直接访问两真实 app 的401前置，保留经 Caddy 双目标401原断言，以区分鉴权路由本身和入口观察失败。

Docker CLI 非零退出和本轮容器的 `State.Error` 只通过固定分类布尔值输出（logging/cgroup/CPU/memory/pids/network/mount/permission/runtime/security/disk/image/options/unknown），不回显原始错误、命令参数或输入。分类是下一步定位证据，不等于已确定根因；不以分类为理由移除内存、swap、network 或安全限制。脚本还包含合成敏感字符串不回显的离线回归，由同一远端 CI 执行。

历史 `e52e957cb` 双事件中，真实应用二进制编译成功；首个 PostgreSQL `docker run` 返回 125，容器 ExitCode 128、非 OOM，应用尚未初始化。旧脚本没有记录 Docker stderr/State.Error 分类，无法从原日志确定具体启动根因。保留该失败而不盲重跑；本次只补安全分类，不削弱任何原业务/资源/隔离断言。

后续 `6b93f2eea` 双事件仍在首个 PG 启动失败，真实固定分类一致为 `logging_options=true`、`runtime_start=true`，其余分类 false。复核 Moby `daemon/logger/local/config.go` 与 `local.go`：local driver 默认启用压缩，而 `max-file=1` 与压缩同时启用会被拒绝。因此夹具显式增加 `--log-opt compress=false`，保留原 1MiB/单文件上限、所有内存/swap/CPU/pids、网络和安全限制。这是与实际分类吻合且源码可确认的日志选项冲突修复，不将布尔分类冒充原始 daemon 完整错误；应用是否能初始化仍须新精确 head CI。原 bootstrap 和脱敏回归均保留，不 skip、不改成假应用。

`8060db08e` 双事件编译成功并越过旧首 PG 启动失败，但在读取首个应用的宿主端口时 `KeyError: 8080/tcp`。当时报告无 phase，push app ExitCode 为 0、PR 为 1，均非 OOM，未记录端口时点 Running/网络/映射，因此没有猜测根因。随后 `cf2fce23f` 只补单次固定诊断，真实双事件均显示进程当时 `running=true`、地址已分配、仅附着 fixture 网络、`requested_mapping=true` 且 `realized_mapping=false`，明确失败为 `host_mapping_absent`，三项脱敏诊断回归通过。由此确认当时阻塞层是宿主映射未建立；应用稍后 Exit1 仍未定位，不能宣布应用已启动。

现在将夹具的端点契约明确设为唯一 internal 网络私有地址，移除 host publication 请求；这是基于新事实的单一探测路径，不是在宿主映射失败时悄悄 fallback。Docker 官方说明 Linux host 可访问 internal bridge 网络；不通过加入外部网络放开出口。仍保留固定 Running/映射/network/app 分类、早退失败、地址/网络不合要求失败、禁止 host publication、实际 readiness 和全部业务/资源/清理断言。新端点回归只 mock诊断层，不替代真实应用 bootstrap。此修复未动态运行，新精确 CI 仍须证明应用 readiness 或揭示下一层安全诊断。

`7dd383a8b` 新双事件已经越过旧宿主映射失败：push真实app在ready前Exit1且非OOM，旧固定应用分类全unknown；PR事件到达`two-full-applications-ready`，但Caddy双401目标观察超时。两者不能当同一根因，PR成功初始化也不能抵push失败。现在仅修补上述日志级别/HTTP固定观察证据缺口，不猜改模型/鉴权/入口配置，不将新诊断写成应用启动或路由已修。

`18d8e8a86` 新事实进一步区分问题：PR早退分类命中 application_init_failed/database_unavailable、非OOM；push两 app ready且直接 POST 均401，但 edge 50次均200/目标missing。夹具修复两处明确契约错误：PG `pg_isready` 指定 `-h 127.0.0.1 -p 5432`，避免命中官方 image 在 initdb 阶段的 socket-only 临时服务；Caddy fixture站点从 `http://0.0.0.0:9080` 改为 `http://:9080`，避免 IP 站点隐含 Host matcher 拒绝 `api.fixture.invalid`。Docker internal 网络和无宿主端口限制保持，无增加生产回退/重试。CI 还以实际固定 Caddy adapt检查唯一listen为 `:9080`、路由没有host matcher，只保存固定通过phase，不导出适配JSON。直接401/代理双401及原全部阶段、时间预算、资源/清理断言不变；此修复未在本机运行，下一精确head双事件CI仍须证明实际启动/路由通过。

对应官方契约：[PostgreSQL image初始化脚本](https://github.com/docker-library/postgres/blob/master/18/alpine3.23/docker-entrypoint.sh)的临时server明确 `listen_addresses=''`；[Caddy site地址](https://caddyserver.com/docs/caddyfile/concepts#addresses)中带host/IP的地址限制Host，省略host才是catch-all。网络仍只在本轮internal容器内，不公开入口。

job 35 分钟上限含单槽 Go 编译、镜像下载；动态步骤 15 分钟；单应用启动 120 秒，Docker 操作、健康等待及退出都有有界超时。工具/镜像/启动失败直接失败，不 skip、不回退成假应用。所有动态操作仅 CI。本机只 AST/YAML/diff 静态检查，未编译或运行；下一精确提交双事件 CI 才能证明本前置实际通过，旧 26c 七/十项成功不能覆盖。

## 明确未覆盖及下一前置

已接收模型请求不重放、部分 SSE 中断不重放、旧 SSE drain、新请求切换及客户端取消清理，仍需**在这两个真实应用内**有合法共享数据库种子：用户/组/API Key/模型账号资格、指向隔离合成上游的账户凭据和请求通道，以及可观测的生成/账本/活动占用。还需要使用既有路径构造可控 EOF/SSE/取消，不得把本轮未鉴权 401、Python fixture 或部件 handler 当这些断言已通过。原 HTTP 七项、TLS 十项和其他回归完全保留。

这一缺口是安全初始化/fixture 前置，不是需要用户替我们选择透明重放或媒体保留。后续应先扩展独占数据库合法种子及合成上游，逐项连接已有真实业务路径；本轮不改生产 schema、约束、账本或新运行策略。真实双机、DNS、SDK、模型付费、生产流量与媒体持久化仍未验，未授权的生产操作不因 CI 前置完成而恢复。

Docker 隔离与资源语义来源：[internal network](https://docs.docker.com/reference/cli/docker/network/create/)、[internal bridge 宿主可达](https://docs.docker.com/engine/network/port-publishing/)、[runtime limits](https://docs.docker.com/engine/containers/resource_constraints/)、[run options](https://docs.docker.com/reference/cli/docker/container/run)。
