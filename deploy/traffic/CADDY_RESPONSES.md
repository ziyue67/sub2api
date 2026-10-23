# 可选多目标 HTTP Responses 入口

`Caddyfile.responses` 是独立 L7 入口样例，固定验收版本为 **Caddy v2.11.7**。它只接受 `POST /v1/responses`、`POST /v1/responses/compact`；其余路径/方法返回 404，包括管理、支付、图片 capability 和 WS。应用原有鉴权、owner、并发及账本仍在各节点执行。它不改 `deploy/Caddyfile` 的单机默认，不修改 DNS、证书、生产配置或应用开关。

默认仅监听 loopback 明文 HTTP、关闭 admin API 和自动 HTTPS，目标使用 `.invalid` 占位。这个默认不能直接公开使用。操作者应在独立入口上明确配置 TLS、可信网络及转发链，或在已管理的 TLS 前端后使用本样例；公网入口证书及真实双节点验收不在本次回归内。`Caddyfile.responses` 的 HTTP 上游只适用于受信网络；另有下面的显式 HTTPS 上游样例，不提供 TLS 透传。

可信进程环境变量（不要从请求头或客户输入生成）：

| 变量 | 默认 / 用途 |
|---|---|
| `SUB2API_LB_LISTEN` | `127.0.0.1:9080`，入口监听地址 |
| `SUB2API_LB_UPSTREAM_A` / `SUB2API_LB_UPSTREAM_B` | 虚构 `node-a.example.invalid:8080` / `node-b.example.invalid:8080`，两个明确目标 |
| `SUB2API_LB_ORIGIN_HOST` | `api.example.invalid`，两节点共同接受的 HTTP Host，健康探针也使用它 |
| `SUB2API_LB_HEALTH_INTERVAL` | `5s`，连续两次失败撤离、两次成功恢复；超时 1 秒 |

例如只在自己的隔离环境准备显式配置后执行 `caddy run --config deploy/traffic/Caddyfile.responses --adapter caddyfile`。本文不是已执行的部署步骤，不应把 `.invalid` 占位替换为真实生产地址后直接接流。站点只代理目标 API 路径；不提供聚合入口 `/readyz`，后台对每个上游的 `/readyz` 检查也不等于完整业务健康。应用节点需要按现有契约配置快照门禁和角色，入口不会自动开启它们。

## 可选 HTTPS 上游身份验证

`Caddyfile.responses-tls` 保持相同的路由、Host、readiness、drain 与零重放契约，只将 edge→两节点这一段改为 HTTPS。它不改变 HTTP 模板、`deploy/Caddyfile` 或应用默认行为，前端仍是 loopback HTTP，不能据此公开明文接收 API Key。公网客户端→edge 的 TLS、DNS、证书部署及轮换仍须单独审核。

| TLS 样例变量 | 默认 / 用途 |
|---|---|
| `SUB2API_LB_UPSTREAM_A` / `SUB2API_LB_UPSTREAM_B` | 虚构 `node-a.example.invalid:8443` / `node-b.example.invalid:8443`；只填 host:port，不带 scheme/path |
| `SUB2API_LB_TLS_SERVER_NAME` | `api.example.invalid`；两节点证书共同覆盖的 DNS 名，实际拨号地址可不同；SNI 与证书名称验证都用此值 |
| `SUB2API_LB_ORIGIN_HOST` | `api.example.invalid`；显式 HTTP Host，业务与 `/readyz` 一致。通常与 TLS 名相同，均来自可信部署配置 |
| `SUB2API_LB_TRUSTED_CA_FILE` | `/etc/sub2api-lb/origin-ca.pem`；操作者提供的 PEM CA bundle，不是叶证书私钥；路径不得包含空白 |

`tls_trust_pool file` 仅信任明确提供的 CA bundle；不使用 `tls_insecure_skip_verify`、HTTP 回退或关闭名称验证。CA 文件缺失/不可读或其中证书解析失败会使配置校验失败；空文件或不含证书的文件可能只生成空信任池，不能仅凭 validate 成功判为信任配置正确。不可信证书或错误证书名称使真实健康握手失败，采样收敛后不得接收新业务。健康采样不是模型成功保证。多个节点证书若没有共同 DNS SAN，本最小模板不适用，应另审按节点的传输配置，不能通过禁用验证解决。没有 mTLS、自动签发、证书热轮换或公网 TLS 透传新行为。

CI 使用独立临时目录内的合成 root CA、签名叶证书及另一无关 CA；不读取现有证书/凭据，不访问公网节点。`test_caddy_responses_tls.py` 继承同一套七项代理行为，再补正确 CA/SNI/健康 Host、错误 CA、错误 SNI 三项，共十项。错误身份用实际 TLS SNI 回调计数证明握手已发生，健康 HTTP 与业务请求计数仍不变、入口最终 503；恢复可信配置后双目标重新 200。不靠文本搜索或单纯连不上端口冒充证书验证。fixture 的 TLS socket 取消探测不使用不受支持的 `MSG_PEEK`，保留有界关闭和活动流归零检查。

## 时序与不重放契约

代理以 round robin 在已观测健康的目标间分配新请求。`lb_retries 0` 和 `lb_try_duration 0s` 关闭 Caddy 的后端重选；上游 HTTP keepalive 关闭，避免复用失效连接时底层传输重试。带 `Idempotency-Key` 的 POST 也不能因此跨节点自动重发。已到达上游后 EOF、429 或已输出部分 SSE 时，返回/关闭当前结果，不自动另一次生成。这不替客户端提供全链路幂等或自动恢复。

不设置负 `flush_interval`、request/response buffers 或 encode：SSE 由 Caddy 原生即时刷新，客户端关闭须能传到原上游。负 flush interval 会改变早断时的上游取消语义，不能盲目当作流式优化。建连预算 3 秒、响应头 30 秒；模型流持续时间仍由应用与客户端既有预算管理，不新增后台持续生成机制。

节点 `/readyz` 变 503 后，健康采样收敛才撤离新请求；恢复也需采样。既有 SSE 仍留在原节点，节点应先 drain、等待已有请求结束，再停止。它不迁移已建立流，健康采样与发起请求仍存在窗口，硬故障中的流仍会失败；不承诺零中断。不要将模型返回 429 或应用业务错误当成节点整体 readiness 变化。

不配置粘性、owner 转发或会话恢复。HTTP tenant-owner 是授权，不保证上游上下文仍存在；原生 WS 不属于样例支持范围。媒体仍按现有临时节点 capability 生命周期处理。本入口不替用户决定透明续接、保留/删除期限或实时撤流新策略。

入口会转发正文并承担带宽。若把它放在旧流量不足的主机上，正文仍消耗旧机流量，不能称为节流；它只适用于选择了独立 L7 入口的部署。DNS-only 直达节点仍是独立选项，本样例不代替 DNS 健康调度、真实用户分流比例或出口字节验收，也不选托管供应商或购买服务。

## 真实代理回归与证据边界

独立 workflow `multihost-caddy.yml` 下载官方 `v2.11.7` Linux amd64 tarball，并校验固定 SHA256 `727b91701a392de6ebc5027509f548bf39979e5216340d0faed8fa5e69c84f8b`，不源码构建或使用浮动 latest。

`test_caddy_responses.py` 用同一个模板启动真实 Caddy 和两个 loopback 假 HTTP/SSE 上游，仅 Python 标准库、虚构请求，无应用/SQL/Redis/密钥/模型。七项回归检查：两个健康目标与允许路径；readiness 撤离/恢复及全部不可用；drain 旧 SSE 继续/新请求转移；已接 POST 后 EOF 不重发；SSE 已输出后中断不重发；上游拒绝不重发；客户端取消到达上游且活动流归零。所有 POST 携带虚构幂等提示，靠实际上游请求计数证明只到达一次。

上游 readiness 收敛后才断言新请求分配，不以 DNS 查询数替代流量。测试对 Caddy 进程、响应、连接、服务线程及活动流做限时清理。测试加速的只有 fixture 健康采样间隔（100ms），不是应用租约、重试或生产 readiness 策略。

**本机为生产宿主，仅做源码及非执行静态检查，未运行测试或 Caddy validate。** HTTP 七项已有基线 `8b548074c` 双事件远端 CI 成功证据；本轮共享 harness 改动及新 TLS 十项必须等新精确 head 的双事件 CI，旧成功不能覆盖新增场景。workflow 使用同一固定真实 binary 分别执行两份脚本。仍未验真实两机器/公网 TLS/SDK/DNS缓存、实际用户模型、规模/SLA/费用或长期出口流量。

官方契约及固定版本源码：

- [reverse_proxy 官方说明](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)：health、零重试、SSE 和取消语义。
- [v2.11.7 reverseproxy.go](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/reverseproxy.go)：`tryAgain` 零预算立即停止、下游取消不重试。
- [v2.11.7 httptransport.go](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/httptransport.go)：关闭上游 keepalive 的映射。
- [v2.11.7 healthchecks.go](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddyhttp/reverseproxy/healthchecks.go)：健康探针复用传输与 TLS scheme。
- [固定发布产物](https://github.com/caddyserver/caddy/releases/tag/v2.11.7)。
