# OpenAI 出站客户端身份

OpenAI API Key 账号默认与 OAuth 的 Codex 转发使用同一套客户端身份解析器。普通 Responses、透传、Chat Completions 及其协议回退、WebSocket 握手、图片、Embeddings、独立搜索和账号测试都会在构建出站头时设置身份。无需为现有或新增 Key 账号逐个开启。

客户端传入的 `Go-http-client/2.0`、浏览器 UA、SDK UA 或陈旧 Codex UA 不再决定 API Key 默认出站身份。`User-Agent`、`originator`、`version` 从同一解析结果生成，版本跟随后台 Codex 版本设置及自动同步。账号 `user_agent` 可以选择受支持的 Codex 客户端名和系统信息，其版本仍跟随当前规范版本。

只改写上游请求对象，入站头与用量/错误记录继续记录调用方的真实 UA。后台看到入站 Go UA 不代表它被发送给上游。

优先级从低到高：

1. 默认 Codex 身份（包括账号 `user_agent` 中有效的 Codex 客户端信息）。
2. 供应商专用身份，例如官方 OpenCode 上游所需的 OpenCode UA。
3. 管理员明确启用的账号 `header_overrides`。

显式覆写身份头时，应一起维护 `user-agent`、`originator`、`version`，避免自行制造不一致。自动同步不会重写管理员保存的静态覆写值。`ForceCodexCLI` 仍优先于账号 `user_agent`，不改变 `header_overrides` 的既有最终优先级。

该默认策略限于 `platform=openai` 的 API Key 标准 OpenAI 协议路径。其他平台、Anthropic 原生协议以及 Prism/BPS 等独立适配器继续使用各自协议的身份规则。OAuth 保留既有 Codex 身份统一和门票绑定语义；独立搜索仍执行原有专用头过滤。

兼容性回滚开关仍为 `gateway.disable_codex_identity_enforcement`：默认 `false` 启用身份统一；设置为 `true` 时，API Key 恢复原有客户端头透传，OAuth 恢复原有身份配对逻辑。账号测试原本自带的 Codex 探针头不因关闭该开关而被删除。

此改动保证默认出站身份头的一致性，不构成请求一定不会被上游限流或风控的保证。
