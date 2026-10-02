# Prism OAuth 文本适配器（P1 试验）

关联 [Issue #256](https://github.com/ranxi2001/sub2api/issues/256)。账号编辑页的 Prism 开关复用现有 OpenAI OAuth 凭据，通过回环适配服务访问 Prism 网页。管理员账号测试与 HTTP `/v1/responses` 共用后端凭据获取及适配器请求函数。

当前只支持 `gpt-5.6-sol`、`medium` 和普通文本输入。`tools`、`additional_tools`、图片、工具结果、`previous_response_id`、background、structured output、compact 和原生 WebSocket 不支持。此版本不能替代带工具的 Codex 会话；P2 工具闭环、Codex CLI 端到端验收仍待实现。

## 协议边界

- 用户无需修改 Codex 或添加配置。网关复用已有的会话解析：读取标准会话/线程请求头及 `client_metadata`，线程标识优先，绑定已认证 API Key 和上游账号后生成摘要。`X-Prism-Session-ID` 仅用于网关到适配器的回环通信，外部同名头不参与缓存选取。
- 同一会话命中缓存后保留私有浏览器上下文和项目，先打开新的 chat tab，再提交调用方的完整输入；项目文件仍属于该会话。缺少可靠会话标识的请求、管理员账号测试始终新建空白项目，避免同一 API Key 下的无关对话共享文件。客户端不需要提供 `project_id`，项目创建及 sandbox 管理仍由官方页面完成。
- 缓存仅保存在进程内存，默认最多 1 个上下文（可配置 1-2 个）。空闲默认 300 秒（可配置 30-900 秒）、创建满 900 秒后在空闲检查时回收，不中断正在执行的回合；凭据变化在下一次请求时使该账号旧上下文失效，失败也会丢弃本次上下文。重启后重新创建项目。缓存不新增持久化 OAuth token、Cookie、sandbox token 或会话摘要；原有 pending 文件的敏感状态要求见下文。
- 网页的 start 请求在发送前校验模型和 reasoning effort，只允许一次。浏览器尝试重复 start 会被拦截。status 响应会将最新 request ID 和 `turn_state` 更新到权限为 `0600` 的待决文件。
- 结果不明确时保留待决文件，后续请求返回 409。没有自动删除待决文件或重放模型请求的逻辑。自动续接轮询、取消和结果恢复尚未实现；运营人员必须先确认原请求结局。
- start 从未离开浏览器（页面没有发出，或被门控拦下）时结局是确定的：适配器清除待决文件并返回 `start_not_sent`，账号不会因此被锁。
- 终态回执只保留 request ID、模型、请求次数、时间和答案摘要，不记录 prompt、答案正文、Cookie 或 OAuth token。待决文件含敏感 `turn_state`，不能公开或提交。
- 成功结果 `usage: null`，不会估算官方 token 数。SSE 只包含 created/completed 事件，不产生伪造的 token delta。主网关发现 usage 不可用时拒绝将它记录为零 token 或据此扣费，所以这是未计费的试验通道。
- 仅允许 `http://127.0.0.1:<port>/v1` 或 `http://[::1]:<port>/v1` 作为适配器地址；不使用环境代理、账号代理、HTTP 重定向或通用插件链传递 OAuth token。适配器默认只监听 IPv4 回环 `127.0.0.1:8319`。
- 单个浏览器回合串行执行，忙时返回 429，不排无限队列。每个账号有独立待决锁；全局服务也只允许一个浏览器回合，避免生产资源争用。重复、冲突或非法的标准会话标识在网关拒绝，不转发到适配器。
- 调度器不会把 WebSocket 会话分给开启 Prism 的账号（与 Excel BPS 模型相同），HTTP 请求照常进入适配器。适配器的鉴权或路径错误（401/403/404/405）对客户端统一返回 502，不会被误当成客户端 API Key 失效。

## 运行条件

在构建机生成带前端的 Sub2API 二进制。生产机只安装已有产物及运行时，不执行 Go、Vite 或其他源码构建。

浏览器运行时需要 Python 3.12、`requirements.txt` 固定版本的 Playwright wheel，以及匹配的 Linux Chromium 预构建产物。只安装 wheel，可用 `pip install --only-binary=:all: -r requirements.txt` 防止回退到源码构建。下载 Chromium 不属于编译；运行时目录应由 root 管理。

服务必须以非 root 用户运行并启用 Chromium sandbox。Ubuntu 限制 user namespaces 时，可以安装 Chromium 自带的 SUID sandbox helper：root 所有、`4755`，标准路径为浏览器旁的 `chrome-sandbox`；下载产物名为 `chrome_sandbox` 时建立对应链接。不要使用 `--no-sandbox`。systemd 的 `NoNewPrivileges=yes` 或清空能力边界会阻止 SUID sandbox；模板为此保留 helper 所需能力，浏览器本身仍由 `sub2api` 用户启动。

模板假设安装目录 `/opt/sub2api/prism-adapter`、虚拟环境 `venv`、状态目录 `/var/lib/sub2api-prism`。按实际安装位置设置受限文件 `/etc/sub2api-prism.env`（root 所有，`0600`）：

```dotenv
PRISM_ADAPTER_API_KEY=<random-bridge-secret-at-least-32-characters>
GATEWAY_PRISM_BROWSER_API_KEY=<same-bridge-secret>
GATEWAY_PRISM_BROWSER_ENABLED=true
GATEWAY_PRISM_BROWSER_BASE_URL=http://127.0.0.1:8319/v1
PRISM_ADAPTER_CHROME=<absolute-path-to-chromium>
CHROME_DEVEL_SANDBOX=<absolute-path-to-chrome-sandbox>
PRISM_ADAPTER_STATE_DIR=/var/lib/sub2api-prism
# 可选：内存缓存上限 1-2 个，空闲回收 300 秒；不设置即用默认值
PRISM_ADAPTER_MAX_SESSIONS=1
PRISM_ADAPTER_SESSION_TTL_SECONDS=300
```

安装 `sub2api-prism-adapter.service`，将 `sub2api-prism.conf` 放入主服务的 drop-in 目录，然后 reload/restart。主服务重启需要部署授权和二进制回滚备份。运行目录、状态目录权限与现有服务用户应对应；不要把 env 文件提交到 Git。

`/health` 只证明 HTTP 进程可用，不证明 OAuth 登录、浏览器 sandbox 或模型可调用。服务模板限制 CPU 为一个核心、内存为 900 MiB、禁止 swap；实际资源需求仍需观测。

## 验收

1. 在账号编辑中打开 Prism 开关并保存。API Key 账号和 shadow 账号不显示开关。
2. 对该 OAuth 账号通过管理员测试入口请求 `gpt-5.6-sol`。必须观察 `test_start → content → test_complete(success=true)`，不能只看 HTTP 200。
3. 检查回执的 `start_count=1`、实际模型和终态；使用数学题时核对最终答案。项目必须是空白项目，不能用已有答案的项目评估推理能力。
4. 保持 Codex 原有请求不变，用同一会话连续提交两个不同文本请求，检查第二次回执的 `session_cache_hit=true`。换线程、换 API Key、换账号时不能命中旧会话。无标识请求和管理员测试始终使用新项目。
5. Astra 等其他模型返回 422；适配器不可用时不能退回原生 Codex 上游。工具请求也应明确拒绝。

本地离线检查：

```sh
python3 -m unittest discover -s prism-adapter -p 'test_*.py' -v
cd backend
go test ./internal/service -run 'TestPrismBrowser|TestAccountUsesPrism' -count=1
```

在本机构建机使用已安装的 Chromium 运行浏览器回归（只连接本地模拟页面，不使用 OAuth 或真实 Prism）：

```sh
python3 prism-adapter/smoke_browser.py --chrome /absolute/path/to/chromium
```

该脚本验证三次 start、同会话一次缓存命中、两个独立项目以及新聊天不重复提交历史。它验证浏览器机制，不能替代真实账号糖果测试或证明模型能力。

项目会保留在账号的 Prism 工作区内，本版不自动批量删除项目。大规模使用前需要项目回收、账号代理、动态模型目录、计费策略和 P2 工具闭环的独立实现及验收。默认保持总开关关闭；真实用户流量应等待这些边界完善。
