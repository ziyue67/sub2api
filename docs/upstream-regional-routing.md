# 按上游域名选择地区出口

用户地区分流决定哪个应用节点处理请求；上游地区出口决定该节点如何访问已选账号的上游 API。两者可以同时使用。例如客户请求在亚洲 gateway 完成鉴权与计费，`api.vendor.example` 的连接通过美国 SOCKS5 出口建立。上游看到美国出口 IP，TLS 仍校验原上游域名，SSE/响应经入口传回客户。

入口仍承载请求和响应字节，这项功能不会减少它的总带宽，也不保证线路一定更快。先比较入口直连与美国出口到同一站点的实际延迟；仅凭域名或 CDN IP 无法确定上游服务位置。地区规则由管理员维护，不调用地理定位服务。

## 配置

在所有需要使用出口的主站/gateway 的 `config.yaml` 中配置。默认关闭，规则、环境变量和凭据更新均需重启相应进程；已有连接不会热切换出口。

```yaml
gateway:
  upstream_routing:
    enabled: true
    regions:
      - id: us
        proxy_url_env: SUB2API_EGRESS_US_PROXY_URL
      - id: eu
        proxy_url_env: SUB2API_EGRESS_EU_PROXY_URL
    rules:
      - domain: api.vendor.example
        region: us
        account_ids: [123] # 可选：仅指定上游账号 ID，非客户 API Key ID
      - domain: "*.us.vendor.example"
        region: us
      - domain: api.eu.vendor.example
        region: eu
```

通过 systemd `EnvironmentFile` 或 Kubernetes Secret 注入代理 URL，例：`SUB2API_EGRESS_US_PROXY_URL=socks5h://127.0.0.1:11080`。配置文件只包含变量名，不保存出口密码；未注入、格式错误、未知地区或重复规则会使启用配置验证失败，不会静默直连。也可通过 `GATEWAY_UPSTREAM_ROUTING_ENABLED=false` 停用，但 Compose 必须显式把该变量传入容器；仅修改宿主 `.env` 不会自动生效。

支持 `http://`、`https://`、`socks5://`、`socks5h://` 代理 origin，可在受限环境变量中附带代理认证。`socks5` 统一按 `socks5h` 使用。代理负责解析和连接目标地址，现有安全 URL 检查仍适用；已启用的公网目标检查不会被地区路由绕过。

域名精确匹配优先于 `*.domain`；多个后缀匹配时最长者优先。忽略域名大小写、尾点和目标端口；`*.example.com` 不匹配根域 `example.com` 或 `notexample.com`。支持精确 IP，国际化域名请写 ASCII/Punycode。规则最多 256 条、地区最多 64 个，每个地区固定一个出口；部署多个出口时用不同地区 ID 显式指定，不自动切换出口。

规则可设置 `account_ids` 限定已选中的上游账号，其他账号不会命中该规则。同一域名可配置一条通用规则和多条账号规则；相同域名下账号规则优先于通用规则，账号 ID 不得重复。账号规则仍须匹配目标域名，不能把 Nerd 的 Serverless HTTP endpoint 当作出口代理。配置后在需要处理该账号的所有网关节点启用同一规则和可达的代理地址；账号当前所在节点、账号专属代理和已有 WS 连接不会被此规则自动迁移。

## 在美国节点准备出口

可复用已有的受控 HTTP CONNECT / SOCKS5 服务。应用的 Serverless HTTP 地址或 `/internal/serverless/probe` 不是代理端口，不能直接填入本配置。

没有代理服务时，可在入口宿主机通过受限 SSH 身份建立动态转发，远端 SSH 服务需允许目标转发：

```sh
ssh -N -D 127.0.0.1:11080 \
  -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
  egress@us-relay.example
```

此时将 US 环境变量设为 `socks5h://127.0.0.1:11080`，美国机器建立到上游的 TCP 连接。部署者自行管理 SSH key、known_hosts、自动启动与目标访问限制。不要把无认证 SOCKS 服务暴露到公网。Kubernetes 的 `127.0.0.1` 指当前 Pod；用同 Pod sidecar，或配置受信网络内实际可达且受访问控制的代理地址，不能直接复制宿主机 loopback。

跨公网优先使用 HTTPS 代理或 SSH/受信隧道内 SOCKS5。主站已有账号住宅代理、292 采票代理和 Keeper 各有用途，不作为地区出口自动复用。

## 转发与失败语义

- 非空账号/调用方专属代理优先，即使其 URL 无效也由原路径报错，地区规则不会覆盖它。要使用地区规则，应确认该账号可以取消专属代理绑定。
- 覆盖共享 `HTTPUpstream` 的账号 HTTP/HTTPS 请求（含 SSE、通过它实现的账号测试和探测），以及 OpenAI 原生 WebSocket 的 passthrough、连接池和预热建连。HTTP 重定向每一跳重新匹配地区，并沿用 `net/http` 的认证头处理和既有 URL 校验。WebSocket 的地区握手不跟随重定向。
- Codex 采票、BPS 专用 HTTP profile 和无账号请求保持既有出口。Prism 浏览器、其他独立 SDK/自建 client 和 Gemini Live 等不使用共享客户端的路径，不在本次覆盖内；不能把其 loopback sidecar 改指地区代理。
- HTTPS 经 CONNECT / SOCKS 隧道，TLS 终端仍是上游 API；没有把上游 URL 重写为代理 URL。HTTP 明文内容可被出口代理读取。指纹路径继续沿用现有指纹 transport；HTTPS 代理本身仍按现有实现使用标准 TLS transport，不能宣称保留自定义 ClientHello。
- HTTP/SSE 响应不整包缓存，连接池按已有账号/代理策略隔离；Body 关闭释放引用并传递取消。代理 CONNECT/SOCKS 建连及 TLS 握手有界，成功后不限制长流的总时长。
- 命中规则后出口失败直接返回 transport error，不增加跨出口重放，也不降级为主站直连；已有上层账号故障转移/HTTP 重试语义保留。已开始的流不会重新发送。取消和底层错误类型可继续识别，日志文本隐藏地区代理凭据。
- 不重新鉴权、选号或生成第二份计费记录。不是完整请求跨 Pod 托管，也不是自动 Kubernetes 扩缩容。

## 验收与观察

结构化日志 `upstream.region_route` 包含 `upstream_host`、`region`、HTTP 状态或 transport error；HTTP 路径另有 `account_id` 和 `response_header_ms`。这是建立上游连接并获得响应头阶段的记录，不代表 SSE 最终业务成功；WebSocket 只在新握手时记录，复用连接的回合不会重复记一条握手。

先在本地模拟上游/代理验证域名匹配、跨域重定向、CONNECT、SSE、WebSocket、断流取消及失败不直连。生产上线另行指定代理、域名与灰度范围：逐实例开启，核对出口日志/上游观测 IP 和响应状态。代码测试不构成美国真实线路加速的测量结果。

回退时关闭 `gateway.upstream_routing.enabled` 并排空、重启入口进程，保留代理配置便于核查；无数据库迁移。上游对来源 IP 或连接会话有要求时，应先结束旧会话再切换出口。
