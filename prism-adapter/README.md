# Prism OAuth 文本适配器（P1 试验）

账号编辑 → Prism → 勾选专属模型。`extra.openai_prism_browser_models` 按账号映射后的模型名匹配，只支持 `gpt-6.1-sol`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-luna`。未选模型继续使用原来的 Codex / Excel 路由；显式空数组表示不走 Prism。已开启 Prism 的旧账号缺少该字段时仅默认这四个模型，不再接管所有 OpenAI 模型。所选模型的 Prism 失败仍不自动切换协议或模型。

Prism 请求由适配器独立排队与限流，不参与原生账号的自动并发升降档；账号页显示的并发档位不代表适配器容量。本变更不会提高线上适配器限额。

## 从 pr269 适配器升级（Issue #280）

`v2.9.7` / `bc83ff9c3` 的 Go 网关已包含 #278 工具请求头与终态验证，但只替换 Go 二进制不会更新独立运行的 `pr269-49b0f52` 适配器。该旧组合不能验收为支持 6.1 Sol 客户端工具。普通文本请求带 `include=["reasoning.encrypted_content"]` 或 `reasoning.summary=auto` 也需要本次适配器修复。

推荐使用 #281 同一提交中的 Go 网关与完整 `prism-adapter/` 目录。上线顺序：

1. 保留现有 Go 二进制、适配器目录及受限环境文件的回滚副本。全部构建在本机或 CI 完成。
2. 准备完整适配器目录和 `requirements.txt` 固定 wheel；保留已有 OAuth 会话、pending journal、工具状态目录和服务账号权限，不复制或清空状态来绕过失败。
3. 先替换适配器并重启。启动检查会验证 `jsonschema` / `lark`；旧配置若显式设有 `PRISM_ADAPTER_CLIENT_TOOLS_ENABLED=false`，需改为 true 才能验收工具路径。未设置时新默认值为 true。
4. 替换本 PR 的 Go 网关二进制，在账号编辑页保存 Prism 模型范围。新版适配器与 v2.9.7 网关的过渡组合可以用于验证工具，但专属模型路由、错误响应和并发统计必须有本 PR 的 Go 修复。
5. 使用受控测试 key 验证纯文本、带可选推理参数的文本、函数与自定义工具回传闭环、未选模型的原生路由及错误响应。真实账号权益与线上结果以本次验收为准；本仓库的离线测试不能替代此步骤。

网关日志会保留已知适配器错误码（例如 `tools_disabled`、`unsupported_request`、`model_unavailable`），不记录适配器任意错误文本。`model_unavailable` 表示账号页面缺少所选模型，不应作为参数兼容问题重放，也不会静默改成其他模型。

关联 [Issue #256](https://github.com/ranxi2001/sub2api/issues/256)。账号编辑页的 Prism 开关复用现有 OpenAI OAuth 凭据，通过回环适配服务访问 Prism 网页。管理员账号测试与 HTTP `/v1/responses` 共用后端凭据获取及适配器请求函数。

文本请求接受 `gpt-6.1-sol`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-luna` 四个精确模型 ID，以及 `low`、`medium`、`high`、`xhigh` 思考强度（省略时为 `medium`）。能否调用仍取决于该 OAuth 账号在 Prism 页面中实际可选的模型和强度；不把静态支持列表当作账号权益证明。默认保持文本模式。`gpt-6.1-sol` 可通过下述服务端试用开关启用客户端工具桥；图片、`previous_response_id`、background、structured output、compact 和原生 WebSocket 仍不支持。

每个请求先通过官方页面选择模型和思考强度，再核对 start 元数据中的实际值。缓存命中也重新检查，响应与终态回执保留本次模型和强度；并发请求不修改全局默认值，也不将新模型静默替换为 `gpt-5.6-sol`。账号页面没有对应选项时，发送前返回 `model_unavailable` 或 `reasoning_unavailable`（HTTP 422）；未知模型 ID 返回 `unsupported_model`。Beta 开关属于 Prism 账号设置，适配器不会自动修改它；开启 Beta 或在配置接口看到模型名都不能替代真实调用验收。

## 协议边界

页面初始化时可能出现官方配置 SDK 已 Ready，但 React 仍停留在 loading、只展示默认 `5.6 Sol` 的状态。适配器等待 SDK 就绪后，用 `getContext().user` 原样调用官方 `updateUserAsync` 刷新该页配置，再操作模型菜单；不会改写用户属性、套餐、Beta 标记或模型配置。准备阶段等待有上限，目录未就绪时返回 `model_catalog_unavailable`（503），不会提交模型请求。

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

## 6.1 Sol 客户端工具桥（试用）

同时升级 Go 网关和本目录适配器，并安装 `requirements.txt` 固定版本的预构建依赖。客户端工具桥接默认启用；显式设置 `PRISM_ADAPTER_CLIENT_TOOLS_ENABLED=false` 可关闭。启动时检查工具验证依赖，缺失则停止启动，避免接收请求后才发现升级不完整。其他三个模型暂只保留文本路径。无需更改用户的 Codex 工具定义、provider 或请求头。

- 支持 Responses `function`、`custom`、嵌套 namespace，顶层 `tools` / `additional_tools` 及 input 内的 `additional_tools`。
- 支持 `tool_choice=auto/none/required`、指定 function/custom，以及 `parallel_tool_calls`。最多 96 个工具、每次最多 8 个调用、历史最多 64 个调用；`parallel_tool_calls=false` 时最多一个。
- 网关不执行 shell、JavaScript、补丁、MCP 或文件操作。Prism 通过受控文本协议请求客户端工具，适配器只接受带本轮标记的完整 JSON；未知工具、裸 shell、代码围栏、重复调用或不合法参数不会被猜测、包装或发送给客户端。这不是 Prism 原生工具通道。
- Function arguments 校验 JSON Schema Draft 2020-12 / Draft 7；拒绝外部 schema 引用。Custom input 保留原始字符串，支持 text、regex 和 Lark 格式；Lark 仅允许 bundled common imports。校验在独立子进程中运行，限制时间、CPU、输入大小，Linux 限制地址空间 192 MiB；不运行工具代码。
- 回传 `function_call` / `custom_tool_call`、原工具名/namespace 和唯一 `call_id`。客户端执行后，用完整 Responses 历史提交对应的 `*_call_output`；当前不支持只有结果、没有原 call 的增量历史，也不把 `previous_response_id` 当作已恢复的会话。
- 客户端工具回合每次创建独立空白 Prism 项目，避免复用原生聊天后第三轮编辑器无法就绪；续接依据是完整客户端历史和调用记录。原会话的准入锁与 pending 作用域仍保留，不把未知结局改成匿名新请求来绕过保护。普通文本继续使用原项目缓存策略。
- `X-Prism-Caller-ID` 由 Go 网关按已认证 API Key 和账号生成，外部同名头不参与取值。调用记录绑定 caller、账号和标准会话摘要；换账号/Key/会话、修改已发出的参数、未知 ID、缺失或重复结果均拒绝。
- SQLite `tools/v1.sqlite3`（0600，父目录 0700）只存调用摘要、作用域摘要、响应/调用 ID、结果摘要和占用状态，不存工具参数、结果正文或凭据。每个结果先占用再提交，明确未发送时可释放；结果未知时保留占用，重启后也不重放。已知上游终态但格式不合法时消耗该结果并报错，不发出工具调用。记录上限 50000，达到上限需运维处理，不自动清除未知状态。
- Hosted web/search、MCP、computer、image 等工具不由本桥执行。有可用客户端工具时，未支持的 hosted 类型在提示和响应 metadata `prism_unavailable_tools` 中明确列出；只有 hosted 工具或强制选择它们时拒绝请求。
- SSE 在真实终态验证后输出 item added / arguments 或 input done / item done / completed；不伪造逐 token delta。Go 网关核对工具目录及事件与终态的一致性。当前响应仍按终态缓冲，主动终止远端生成、自动恢复未知回合和长连接实时心跳不属于本次实现。
- `usage` 仍为 null，保持原来的未计费试用边界。可接受 `include=["reasoning.encrypted_content"]` 和 summary=auto 的可选请求，但不会编造 Prism 未提供的 encrypted reasoning；已包含密文的输入历史明确拒绝。

离线验证（没有 OAuth、没有真实模型请求）：

```sh
python -m unittest discover -s prism-adapter -p 'test_*.py'
python prism-adapter/smoke_client_tools.py --chrome /absolute/path/to/chrome-headless-shell
```

浏览器 smoke 使用合成工具完成 function → 客户端结果 → custom → 客户端结果 → 最终文本的三次上游提交。真实上游验收需另外记录模型原名、effort、唯一 start、调用和回灌关联及最终结果，不把 mock 当作真实 Codex 客户端测试。

## 运行环境

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

## 并发执行器（服务端试用开关）

默认 `PRISM_ADAPTER_MODE=browser` 保持原来的单回合 UI 执行器。要试用单账号并发，在服务器环境文件中设置：

```dotenv
PRISM_ADAPTER_MODE=multiplex
PRISM_ADAPTER_MAX_INFLIGHT=20
PRISM_ADAPTER_ACCOUNT_MAX_INFLIGHT=20
PRISM_ADAPTER_MAX_QUEUED=30
PRISM_ADAPTER_BOOTSTRAP_CONCURRENCY=1
```

用户的 Codex、模型名和请求不需要修改。并发总数和单账号上限均不超过 30，单账号上限不能大于总上限；队列允许 0-60 个请求，等待超过 15 秒返回 429，尚未提交模型请求。三路管理员糖果测试使用独立请求，不需要客户端会话头。同一有标识的对话仍顺序执行，其他对话可以并行。

新执行器使用一个浏览器、一个账号上下文，默认只保留一个短期项目准备页面（`PRISM_ADAPTER_BOOTSTRAP_CONCURRENCY` 可设 1-2）。项目先通过官方页面的 fetch 封装调用 `/api/projects` 创建，客户端生成的 UUID 必须由上游原样确认；然后直接进入该项目页面，由官方编辑器创建聊天并发出唯一一次 start。避免依赖首页 New 菜单及额外整页跳转；取得可信 request ID 后，在发送前捕获该页面的首个 status 请求体，关闭准备页面，由常驻官方页面的 `window.fetch` 验证封装接管轮询。start/status 都保留官方验证流程，不复制 start 的验证头到 status；原生 fetch 的同源轻量页在真实上游会被 403 拒绝，不能替代官方页面。只有登记过的精确 status 请求体会被放行，不合成 start、复用验证头或重新提交未知结果。项目缓存只保留会话对应的项目 ID，不为每个并发请求保留浏览器页面。网络门控只用 Chromium Fetch 拦截模型 API，保留静态资源的正常 HTTP 缓存；不使用会关闭整页缓存的 Playwright route()。

当前最多同时驻留一个账号上下文；另一个账号在它繁忙时会被拒绝，账号池多上下文调度不属于本轮范围。凭据更新必须等旧上下文在飞请求结束才能替换，期间返回 429，不强行关闭旧请求。空闲回收沿用 `PRISM_ADAPTER_SESSION_TTL_SECONDS`，存活满 900 秒且无活动请求也会回收；到期不会中断在飞任务。

待决文件改为 `pending/<account_id>/<scope_hash>.json`。每个请求有自己的 request ID 和 turn_state；不确定结果只阻塞相同会话。匿名管理员测试每份结果使用独立作用域，不自动重试。原版本留下的 `pending/<account_id>` 文件仍会阻塞该账号，不能绕过。回滚到旧执行器时，旧程序看到该目录也会拒绝账号，必须先核实并发版本的未完成记录；不能直接删除目录解锁。

systemd 的 `MemoryMax=900M`、禁 swap 和单核限制保持不变。新执行器在 Linux cgroup 使用量达到 `PRISM_ADAPTER_MEMORY_LIMIT_MIB`（默认 750 MiB，必须为正整数）时拒绝新的项目准备，并回收已空闲上下文；已提交请求继续尝试取得终态。这个阈值是保护措施，不是达到生产容量的证明。推荐使用与固定 Playwright 版本匹配、预构建的 Chromium headless shell，仍启用浏览器 sandbox。

本地完整路径验证（真实 Chromium，模拟上游，无 OAuth/真实推理）：

```sh
python3 -m unittest discover -s prism-adapter -p 'test_*.py' -v
python3 prism-adapter/smoke_multiplex.py --chrome /absolute/path/to/chrome-headless-shell --concurrency 20
python3 prism-adapter/smoke_multiplex.py --chrome /absolute/path/to/chrome-headless-shell --concurrency 30
```

此脚本通过真实 HTTP 入口、项目准备、start/status 移交和 journal，验证并发任务各自只提交一次、项目和状态不串线，默认页面峰值不超过 2（准备并发设 2 时不超过 3）。模型结果由本地模拟服务生成，不可用来声称真实 Prism 20/30 并发或“不降智”已验收。真实试用应先验证 1/3 并发，再逐步升到 20/30，同时记录上游终态、正确答案、耗时和整个 systemd cgroup 的内存峰值；不得在生产服务器构建。

## 验收

1. 在账号编辑中打开 Prism 开关并保存。API Key 账号和 shadow 账号不显示开关。
2. 对该 OAuth 账号通过管理员测试入口请求 `gpt-5.6-sol`。必须观察 `test_start → content → test_complete(success=true)`，不能只看 HTTP 200。
3. 检查回执的 `start_count=1`、实际模型和终态；使用数学题时核对最终答案。项目必须是空白项目，不能用已有答案的项目评估推理能力。
4. 保持 Codex 原有请求不变，用同一会话连续提交两个不同文本请求，检查第二次回执的 `session_cache_hit=true`。换线程、换 API Key、换账号时不能命中旧会话。无标识请求和管理员测试始终使用新项目。
5. 分别验证四个模型及所需强度，核对响应、回执和实际发出的 start 一致。Astra 等未适配模型返回 422；账号缺少选项时也明确拒绝，不降级模型。适配器不可用时不能退回原生 Codex 上游；未开启工具桥时工具请求明确拒绝。

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

该脚本验证三次不同模型/强度的 start、同会话一次缓存命中、两个独立项目以及新聊天不重复提交历史。`smoke_multiplex.py` 按四模型与四档强度混合发送请求，同时核对实际 start、响应和回执参数。这些脚本验证浏览器机制，不能替代真实账号糖果测试或证明模型能力。

项目会保留在账号的 Prism 工作区内，本版不自动批量删除项目。大规模使用前仍需项目回收、账号代理、动态模型目录、计费策略、真实 Codex 客户端和长时间工具会话的独立验收。默认保持总开关关闭；真实用户流量应等待这些边界完善。

## Multiplex 内存与项目启动压力

`PRISM_ADAPTER_ACCOUNT_MAX_INFLIGHT` 和 `PRISM_ADAPTER_MAX_INFLIGHT` 是准入上限，不保证部署内存或上游项目运行环境可以承载相同并发。准备页面关闭后会请求 Chromium 回收已分离的编辑器上下文；下一次准备若仍达到配置的内存准入门槛，则最多等待 30 秒恢复，仍不足时返回 `resource_pressure`，不会绕过保护提交。systemd 的硬内存上限仍由部署方保留。

multiplex 日志记录 `prism_prepare_start/end`、`prism_poll_start/end`、完成和失败事件，包含本地请求标识、模型/强度、阶段、在途/排队/轮询数量和 cgroup 内存；不包含提示词、账号凭据、Cookie 或 turn-state。只有上游任务的执行区间确实重叠，才算实际并发。

页面显示“项目运行环境的启动请求受到限流”或项目创建接口返回 429 时，会以 `project_runtime_rate_limited` 拒绝后续准备，并对该账号暂停新的启动至少 60 秒（当前进程内）。这是最短保护窗口，不代表上游冷却已经结束；页面给出的更晚时间应优先遵守。已有上游请求继续收尾，未知结局保留，不自动重放。仅调高并发配置不能解除上游限流。

客户端应保留自己的稳定会话/线程标识。同一会话可以复用原项目并新建 chat tab，减少项目反复创建；不同 API Key、账号或会话仍独立。无会话标识的请求不能安全地共用项目，保持新建。工具回传继续遵守原有独立项目规则。

本地 5 并发三轮 smoke（真实浏览器、模拟上游，不能代替生产验收）：

```sh
python prism-adapter/smoke_multiplex.py --chrome /path/to/chrome \
  --concurrency 5 --model gpt-6.1-sol --effort xhigh --rounds 3
```

内存准入门槛与 systemd `MemoryMax` 分别配置；提高硬上限不会自动提高准入门槛。准入门槛应低于硬上限，给页面启动及在途请求留出余量。例如 1.5 GiB 硬上限配合 `PRISM_ADAPTER_MEMORY_LIMIT_MIB=1280`，4 GiB 硬上限配合 `PRISM_ADAPTER_MEMORY_LIMIT_MIB=3584`。该设置只影响 multiplex 执行器，同时用于新页面准入与空闲上下文回收，不中断已提交请求。

systemd 部署还需注意环境变量优先级：`EnvironmentFile` 中的值会覆盖 `Environment=`。若已有环境文件配置了并发，应更新对应文件，或在 drop-in 中追加最后读取的专用 `EnvironmentFile`；重启后必须核对进程实际环境，不能只看 drop-in 文本。Docker Compose 则在适配器服务的 `environment:` 下设置变量。

### 项目环境重连与真实失败

multiplex 识别官方 start 返回的明确 `completed / response.status=error / payload.reason=sandbox_reconnecting`。此时保持准备页面，让官方页面等待自己的 `ensureSandboxConnection` 后继续提交，而不是立刻关闭页面。仅允许同一输入、previousResponseId、conversationId、项目、模型和强度；sandbox 元数据由官方页面刷新。每轮最多 3 次 start 尝试，仍受请求总时限限制。未知结果、一般 HTTP/网络错误、其他终态失败都不能重新放行 start。

日志记录重连次数；回执 `start_count` 如实包含这类明确环境重连尝试。`conversation_too_large`、`project_edit_access_required` 与 `sandbox_reconnecting` 分别报告，不再全部掩盖为 `prism_failed`；其他未知失败保持通用错误，且不输出上游任意报错文本。
