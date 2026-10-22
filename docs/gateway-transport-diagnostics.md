# 网关传输诊断与路由矩阵

关联 #472 的第一阶段。本页描述源码基线 `003901c3087a0b2d8f44a518f9bc5235b95d76ea` 及本 PR 增加的诊断字段，不代表生产配置、真实网络性能或客户端协议兼容性已验收。

## 从哪里读取

- **错误请求或错误 WS 轮次**：沿用[请求采集](request-capture.md)的开关、任务和权限。管理员读取详情 API 或停止任务后导出的 `record.json`，检查 `attempts[].route`。本 PR 没有增加界面展示。采集默认关闭，成功请求仍按原规则删除；中间失败、最终恢复成功的请求仍属于保留范围。
- **HTTP Responses / compact 的耗时记录**：沿用 usage 的 request timing API；`attempts[].route` 标记 HTTP 出站，新增 `routes[]` 记录上游 WS 握手与写入边界。它们随原有计时记录持久化，仍受 usage 成功落库、诊断队列和保留期约束；缺失记录不能证明未发送。
- **原生 WS 成功轮次**：现有 HTTP 计时中间件不覆盖 WS 生命周期，采集也不保留纯成功轮次。本 PR 不增加全量成功 WS 留存；这一观测缺口留给后续 turn 级诊断。Claude Messages 只获得共用 HTTP 采集层能确认的传输和端点信息，专属语义审计待后续 PR。

客户端、客户端版本、服务端实际 SHA、模型、认证类型及相关开关需要随调查记录另存。没有这些条件，不能把不同请求视为性能对照。

## 字段及含义

| 字段 | 含义 |
| --- | --- |
| `route.downstream` | `http` / `websocket`；客户端到网关的传输，不表示流式响应格式 |
| `route.upstream` | 宿主实际调用的 `http` / `websocket` 边界；`plugin` 仅表示请求交给插件，插件内部协议未知 |
| `route.mode` | `native`、`ctx_pool`、`passthrough`、`http_bridge`、`bps`、`prism_browser`、`plugin`；无路由标注的共用 HTTP 路径留空 |
| `route.reason` | 现有协议决策器产生的原因码；bridge 区分显式模式、尺寸阈值、平台和插件要求。原因码沿用现有粒度，不保证逐一解释所有排除条件 |
| `route.fallback_reason` | 本次 WS 加速退回 HTTP 的原因；历史 WS 尝试不被后续回退覆盖 |
| `route.endpoint` | 由实际 HTTP 请求路径或 WS 握手路径归类为 `responses`、`responses_compact`、`chat_completions`、`messages`、`other`；不包含域名、原始路径或查询参数 |
| `route.response_format` | 已知 HTTP Content-Type 的 `sse` / `json`，或 WS 帧请求的 `websocket_frames`；未知 HTTP Content-Type 留空，不由下游格式推断 |
| 采集 `attempts[].phase` | `websocket_handshake` 与 `request` 分开；成功新连接通常先有握手尝试，再有帧尝试；复用连接没有本轮握手 |
| 采集 `attempts[].handshake_ms` | 从本次 Dial 前到返回的耗时，只在握手尝试存在；不是连接池排队或首 token 耗时 |
| 采集 `attempts[].first_event_ms` | 从本次请求尝试开始，到采集观察到第一个完整有效 SSE/WS JSON 事件；包括 `response.created` 等元数据，心跳和无效 JSON 不计；不是内容首 token |
| 计时 `routes[].phase` | `websocket_handshake` / `websocket_write`；`start_ms`、`end_ms` 相对该 HTTP 请求的计时起点，写入耗时不包含模型生成时间 |

内容首 token 继续使用现有 usage 的 `first_token_ms`；HTTP 计时还包含 `first_semantic` / `first_visible` 等已有事件。它们与采集 `first_event_ms` 的起点可能不同，不能直接相减。未采集字段保持缺失，不填零。首事件变快不代表内容生成变快。

每次尝试的路由是上下文中的独立值，回退、更换账号或下一 WS 轮次不会修改已有记录。计时新增边界最多 32 条，超限标记 `truncated`；采集沿用 256 次尝试上限。采集阶段的请求号不再等于 WS 帧数，消费者应按 `phase` 区分。

## 路由矩阵

以下为源码规则。实际端点可能受账号适配和模型路由影响，以记录的端点类别为准。

| 客户端入口 / 认证 | 配置与条件 | 下游 → 宿主出站 | 说明 |
| --- | --- | --- | --- |
| HTTP Responses / 普通 OAuth | 默认不开账号 WS SSE 加速 | HTTP → HTTP | SSE / JSON 由实际响应 Content-Type 确认 |
| HTTP 流式 Responses / 普通 OAuth | WS v2 门控开启，账号开加速、上下文池、关闭自动透传，且无绑定插件 | HTTP → WS | 下游仍是 SSE；具体限制见[加速说明](openai-ws-sse-acceleration.md) |
| 同上，握手失败且允许回退 | 尚未写入请求、未写下游，错误属于允许回退集合 | HTTP → WS 握手失败 → HTTP | 保留握手状态、错误类别、耗时，以及 HTTP 回退原因 |
| 同上，本地 payload 超限 | 编码阶段拒绝，尚未发送对应请求 | HTTP → HTTP | `oauth_ws_sse_payload_too_large`；可能已建立 WS 连接，不能称为 WS 已发送 |
| 同上，写入失败或首事件前/后断流 | 发送状态未知或已发送 | HTTP → WS，当前流结束 | 不因此重放或退回 HTTP |
| HTTP 非流式、compact、Messages 兼容桥接、有非空 previous_response_id | 不符合 HTTP WS 加速条件 | HTTP → 原路径 | 不由加速开关切换；原路径仍可能由账号适配器或插件接管 |
| HTTP / API Key、setup-token、影子账号、Agent Identity、PAT、BPS | 不属于普通 OAuth 加速范围 | HTTP → 原路径 | 不由该账号加速开关切换；BPS 等专有通道不能据此推断内部协议 |
| WS Responses / 受支持账号 | 门控通过、`ctx_pool` | WS → WS | 连接池路径；握手与每次帧请求分开记录 |
| WS Responses / 受支持账号 | mode router v2 开启、`passthrough` | WS → WS | 透传路径；超阈值首帧仍可能走 HTTP bridge |
| WS Responses / 支持桥接的账号 | 显式 `http_bridge`、尺寸桥接或强制桥接 | WS → HTTP 或插件 | `http_bridge` 记录桥接边界，插件内部网络未知 |
| OAuth / 已绑定出站插件 | 原有插件路由生效 | 宿主 → plugin | 不绕过插件；只有宿主可观察的 dispatch 信息，不能声称内部采用 HTTP 或 WS |

基础默认值：全局 WS、OAuth WS、API Key WS、WS v2 开启，`force_http=false`；mode router v2 默认关闭，启用时默认入口模式为 `ctx_pool`；HTTP bridge 默认开启。**这些全局默认值不等于账号开启 WS 或 HTTP WS 加速**。账号 WS SSE 加速仅当 `accounts.extra.openai_oauth_ws_sse_acceleration` 为布尔值 `true` 时启用。账号/全局 force HTTP、账号 WS 模式等限制仍按原代码执行。

## 失败、隐私与兼容性

只增加诊断元数据，不修改请求、模型映射、重试、计费、连接亲和性、取消或工具事件。握手返回 401/403/429 仍沿用原错误处理。记录中的 HTTP 101 只表示 WS 握手或已建立连接的帧路径，不能当作本轮模型请求成功。

新增路由字段只使用内部原因码和固定端点类别，不收集会话 ID、连接 ID、认证头、代理地址或请求正文。错误仍只存类别，不存原始错误字符串。原有采集正文的权限、脱敏、配额和隐私边界继续适用。后台连接池预热没有用户采集上下文，不归到某次用户请求。

没有新配置或数据库迁移；旧 JSON 不含新字段时表示未知，旧二进制可忽略新字段。回滚仅丢失新增诊断能力。没有真实请求测量，也没有性能提升、模型质量或账号状态结论。

## 回归范围与未验证项

新增离线用例覆盖实际路径分类与脱敏、WS 握手回退的两阶段记录、发送后不转 HTTP、WS 多轮换号、有效事件与心跳区分、HTTP 耗时记录、插件边界标注和计时容量上限。测试、lint、构建交给 CI，本次没有本地执行这些检查；没有访问真实上游或启用生产采集。原生成功 WS 的 turn 级留存、压缩协商、工具/compaction 连续性、缓存计费与性能对照仍属 #472 后续工作。
