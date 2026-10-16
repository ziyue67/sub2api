# 请求时区绑定

系统设置 → 网关转发 → Codex 设置中的“请求时区绑定”默认开启并选择“出口 IP 自动识别”，保存后热更新。缺失或空设置使用此默认值；管理员已保存的关闭状态或手动模式继续生效。关闭时不改写请求时区，已有账号配置继续保留。

开启后可选择两种模式：

- **手动指定**：使用 OpenAI 账号创建/编辑窗口中的“请求时区”，未配置时使用 `Asia/Singapore`。可选值由后端 IANA 时区目录提供并校验，支持通过账号 `extra.openai_request_timezone` API 批量配置。
- **出口 IP 自动识别**（默认）：通过请求选定的出口代理探测 IP 和 IANA 时区；直连时探测服务端出口。原生 HTTP 包含区域路由和代理故障转移，WebSocket 在连接建立时识别出口。探测失败、超时或时区无效时保留请求原文，不回退到手动时区。

自动模式复用代理探测服务及其配置。成功结果按完整代理配置缓存 60 秒，失败缓存 15 秒；单次探测最多等待 3 秒，同一出口并发探测合并。只返回 IP 的探测源无法提供时区。动态代理每次连接可能使用不同 IP，因此识别结果代表该出口最近一次探测，不保证与每一条业务连接的实际 IP 完全一致；需要严格固定时区时使用固定出口或手动模式。第三方插件自主管理的转发路径未接入自动识别。

## 请求范围

- Responses HTTP 转发（包括自动透传）以及 WebSocket ctx_pool、passthrough 的首轮和后续轮次。
- 开启时同时替换客户端已有的 `Oai-Client-Timezone`、`X-Openai-Timezone`、`X-Timezone` 请求头；不主动新增时区头。
- 只替换 user 输入中完整 `<environment_context>` 块内已有的 `<timezone>`，以及网页搜索工具中已有的 `user_location.timezone`。
- 不增加缺失的字段；不修改 `current_date`、普通对话文本、其他环境字段、请求头语言、系统或数据库时区。
- 残缺或重复 timezone 的环境块保持原样。非 OpenAI 账号不处理。
- 这是请求一致性功能，不能据此保证或推断模型质量改善。

## 来源与实现

参考 [MACOS-DO/sub4api](https://github.com/MACOS-DO/sub4api/tree/254e7ec4932e65ab8830e7d10e66f184140041bd)，固定源码 SHA `254e7ec4932e65ab8830e7d10e66f184140041bd`，相关历史提交 `8d583e5c0`、`4dbf98c97`。该版本实际代码保留日期并包含中国时区，与其 README 描述存在差异；以实际源码为准。

本功能以 owner fork production `ff9198947a70a83c59b8448f5ef13909119cfe7c`（v2.10.4）为基线，仅移植时区处理、目录和对应测试；额外接入全局开关、设置审计、热更新与批量修改校验。两分支 merge base 为 `efe9aab1e4ec89a42ba45e8dac20e882c5409a6a`，ahead/behind 为 1318/628，不进行整分支合并。

系统设置键 `openai_request_timezone_enabled` 和 `openai_request_timezone_auto` 缺失或为空时均为 true；显式 false 继续生效，设置读取失败时保持原有保守回退；关闭自动识别后使用手动模式，关闭总开关后不改写时区。账号配置保存在现有 extra 字段，不需要数据库迁移。回退到旧版源码时这些字段不生效。

## 本地验证（2026-10-10）

- `GOMAXPROCS=2 go test ./internal/service -count=1`：通过（175 秒）。
- `go test -tags=unit` 的时区、设置保存/失效、批量账号校验及账号时区目录 API 定向测试：通过。
- `go test -race ./internal/service -run 'Test(RequestTimezone|OpenAIRequestLocaleWebSocket)' -count=1`：通过。
- 前端 typecheck、i18n 完整性、变更文件 ESLint：通过。
- golangci-lint 2.14.0 对本次变更的 service、admin handler、routes 检查：0 issues。
- 前端关键 Vitest 集：最终 77 个文件、1317 项通过；新增时区组件另有 3 项测试通过，已加入关键测试列表。
- 前端 `pnpm build`、后端普通及 `CGO_ENABLED=0 go build -tags embed ./cmd/server`：通过，均在本地 PC 执行。
- 关键 Vitest 集在并行构建/静态检查期间曾出现两项表单提交异步断言失败，两次失败用例不同；单独测试、未修改基线的 1315 项和最终完整重跑均通过。
- 未进行发布、远端推送、生产部署或线上模型质量对比；未运行全仓库数据库集成测试。
