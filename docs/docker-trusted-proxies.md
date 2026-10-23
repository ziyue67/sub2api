# Docker + 宿主机 Nginx：可信代理与注册 IP 配额

适用路径：客户端 → 宿主机 Nginx → Docker 发布端口 → Sub2API。注册配额和认证防滥用始终使用 `GetTrustedClientIP`，只接受可信代理链。兼容模式下访问/审计日志可能采用原始转发头，因此日志里的真实 IP 不能证明注册配额归组正确。

未显式配置时只信任 `127.0.0.1/32` 和 `::1/128`。如果容器实际 TCP 对端是 Docker 网关且未受信任，不同用户会共享网关配额；累计两次成功注册后，可能返回 `this IP address has already registered two accounts; contact support`。修复应配置实际入口代理，保留每个真实 IP 的配额限制。

## 1. 确认 TCP 对端和入口转发头

使用管理员浏览器经过与用户相同的 Nginx 入口，打开「系统设置 → 安全与认证 → IP 风控与访问记录」，在开发者工具 Network 中查看 `GET /api/v1/admin/settings/ip-risk` 的响应 `data`：

- `current_ip`：当前请求按可信链解析的地址，与注册配额使用同一解析函数。
- `current_peer_ip`：容器收到该请求时的 TCP 对端地址，不受转发头覆盖。
- `trusted_proxies_configured`：是否显式配置过列表；`true` 不代表配置正确，也可能是显式空列表。

读取这些字段无需开启 IP 风控，不创建账号、不消耗注册配额。它们只说明当前管理请求；注册入口若走不同域名、CDN 或 Nginx location，需分别检查。不要公开完整响应、会话令牌或真实访客地址。

旧版若尚无 `current_peer_ip`，可查看已开启的 IP 风控认证访问记录中的 `peer` / `ip` / `ip_source`；或在宿主机检查容器网络命名空间里的入站连接，在浏览器访问时观察 `Peer Address:Port`：

```bash
# 在部署目录执行；按实际模板替换 -f。需要宿主机有 nsenter 和 ss。
container_id=$(docker compose -f docker-compose.local.yml ps -q sub2api)
container_pid=$(docker inspect --format '{{.State.Pid}}' "$container_id")
sudo nsenter -t "$container_pid" -n ss -nt '( sport = :8080 )'
```

`docker inspect` 中的网络网关只能作为候选，不能代替实际 TCP 对端。Docker Desktop、rootless、多网卡或不同代理拓扑可能使用其他地址。下文 `172.18.0.1` 仅是示例。

宿主机 Nginx 至少设置：

```nginx
location / {
    proxy_pass http://127.0.0.1:8000;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    # 保留站点已有的流式、超时及其他配置。
}
```

这会覆盖 `X-Real-IP`，并把真实对端追加到 XFF 右侧；应用从右向左验证可信链，客户端伪造的左侧前缀不能越过不受信任的真实来源。如果前面还有 CDN/其他代理，需逐跳核实入口的真实 IP 处理及可信范围。

仅宿主机 Nginx 回源时，`.env` 可设置 `BIND_HOST=127.0.0.1`、`SERVER_PORT=8000`，限制源站发布端口。只信任经核实的精确代理 IP（IPv4 `/32`、IPv6 `/128`）或确有必要的最小 CIDR；不要直接信任整个 Docker 私网、`0.0.0.0/0` 或 `::/0`。同一可信对端背后的本机进程/其他容器也属于信任边界，应限制其访问源站。

## 2. 选择一种持久化配置方式

### 方式 A：Compose `.env`

配套的四种 `docker-compose*.yml` 模板均通过以下单键写法传入变量：

```yaml
services:
  sub2api:
    environment:
      - SERVER_TRUSTED_PROXIES
```

在部署目录的 `.env` 添加（先替换为实际代理地址）：

```dotenv
SERVER_TRUSTED_PROXIES=127.0.0.1/32,::1/128,172.18.0.1/32
```

Compose 从当前 shell 或 `.env` / `--env-file` 取值，shell 同名变量优先。老模板中若没有透传条目，仅在 `.env` 添加变量不会自动进入容器；需采用新模板、补上该条目，或使用方式 B。模板采用 Docker Compose 的 [单键环境变量语义](https://docs.docker.com/reference/compose-file/services/#environment)，不能替换成 `${SERVER_TRUSTED_PROXIES:-}` 或 `${SERVER_TRUSTED_PROXIES}`。

| 配置状态 | 应用行为 |
| --- | --- |
| shell 与 `.env` 都未设置该变量 | 保留 YAML 列表；YAML 也未设置时仅信任回环地址 |
| 变量为非空逗号列表 | 替换整个 YAML 列表，不与默认值合并 |
| `SERVER_TRUSTED_PROXIES=` 显式空字符串 | 覆盖 YAML，禁用全部代理信任 |
| YAML `trusted_proxies: []` 且环境变量未设置 | 禁用全部代理信任 |

`.env.example` 中该变量默认被注释。**不要用空值表示“使用默认值”**，也不要把 `[]` 当作环境变量列表格式。

### 方式 B：数据卷中的 YAML

对于已完成初始化的实例，备份并编辑 `/app/data/config.yaml` 中现有的 `server` 节点，保留数据库、JWT 等其他配置，不要创建重复的 `server` 节点或用片段覆盖整个文件：

```yaml
server:
  trusted_proxies:
    - 127.0.0.1/32
    - ::1/128
    - 172.18.0.1/32
```

- `docker-compose.local.yml` / `docker-compose.dev.yml`：默认对应宿主机 `./data/config.yaml`。
- `docker-compose.yml` / `docker-compose.standalone.yml`：默认位于命名数据卷，可通过 `docker compose ... cp sub2api:/app/data/config.yaml ./config.yaml` 取出，保留权限受限的备份，修改后复制回原位置。文件含凭据，不要提交 Git。
- 使用自定义 bind mount / `CONFIG_FILE` / `DATA_DIR` 时，应编辑实际生效的配置文件。

采用 YAML 时，从 `.env` 删除或注释 `SERVER_TRUSTED_PROXIES`，并执行 `unset SERVER_TRUSTED_PROXIES`，同时移除自定义 Compose override / `env_file` 中对该变量的赋值。**留空仍会覆盖 YAML**。新装实例先完成初始化，再修改生成的配置，避免只创建上述片段导致自动初始化被跳过。

## 3. 仅重建应用并验收

配置需要重新加载；环境变量修改必须重建容器，单纯 `restart` 不会更新容器环境。在实际部署目录、保持原项目名及同一组 `-f` / `--env-file` 参数执行：

```bash
docker compose -f docker-compose.local.yml up -d --no-deps --force-recreate sub2api
docker compose -f docker-compose.local.yml ps sub2api
```

该操作仅重建应用，保留数据卷；不需要重建数据库或 Redis。检查服务健康后再次从管理员页面查看上述诊断响应：配置正确时，`current_ip` 应为访问来源，`current_peer_ip` 仍是代理地址。可以从两个受控客户端出口分别读取，确认来源不同、对端相同。

需要核对实际注册请求时，可在隔离环境启用 IP 风控观察模式，对 `/api/v1/auth/register` 发送缺少必填字段的 `{}`，查看对应认证访问事件的 `ip` 和 `peer`；不同来源应记录不同的可信 IP。该请求不创建账号，可能受到已有风控/容量限制；400 只表示字段校验失败，**不是成功注册或配额 403 的复现证据**。不要为诊断消耗真实注册配额或保存完整 Redis MONITOR 数据。

在隔离环境通过已知宿主机代理路径对照两个合成地址（例如 `198.51.100.25`、`203.0.113.26`），再在 XFF 左侧加上伪造地址，确认右侧真实来源仍生效；通过公网入口提交伪造转发头也不应替换真实来源。这类探针只适合有权控制的代理/测试环境，不能在公网随意接受客户端提供的测试地址。

## 4. 更新与回滚

普通镜像更新保留 `.env` 和应用数据卷即可保留配置。更新 Compose 模板前备份原文件、`.env` 及应用配置；把 `.env.example` 作为差异参考，不覆盖站点 `.env`。保留原 Compose 项目名、volume 标识/挂载路径、bind 地址、端口及自定义 override；更新后再次检查变量是否透传，以及 YAML 模式是否仍未注入环境覆盖。

使用 `-f docker-compose.local.yml` 时，自定义 override 也应显式列为额外的 `-f`，不要假定会自动加载。不要通过 `down -v` 更新，否则会删除命名数据卷。回退旧模板可能丢失环境变量透传，此时应保留透传条目或恢复等价的持久化 YAML，并重新验收。

本修复不迁移、删除或重置 `registration_ip_quotas`。旧网关配额和历史事件保持原样；正确解析后，新请求按真实来源取配额。恢复旧配置可能重新出现聚合，不能靠清空网关计数长期解决。
