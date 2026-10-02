# PRISM_BRIDGE_RESEARCH_CN.md

> 关联议题：[#256 [Research] Prism 多项目取长短，整合为 Codex Responses API Bridge](https://github.com/ranxi2001/sub2api/issues/256)
>
> 本文是 **2026-10-01 基于 prism.openai.com 线上环境的实测结论**（非离线推演），
> 供议题 P0/P1 阶段实现时对照。所有结论均在真实账号 + 真实沙箱上验证。

## 中文

### 一、结论速览

| 议题 | 实测结论 |
| --- | --- |
| 认证入口 | OAuth access_token（RS256 JWT，10 天）**不能**直接替代 Prism session cookie 调 API |
| 换发 session | 将 access_token 预置为 cookie `prism_oai_access_token` 后访问站点首页，**Prism 会自动签发全套 session cookie**（含 `prism_session_token`） |
| 最大障碍 | **Cloudflare 对 prism.openai.com 启用 TLS 指纹校验**：Go/Python/curl_cffi(各版本) 客户端即使凭据/`cf_clearance` 新鲜也一律 `403 {"error":"Request verification failed"}`；真实 Chrome（playwright 驱动）全部放行 |
| 可行通道 | 常驻 playwright 浏览器作上游转发层（sidecar）：本地端口收请求 → 页面内 `fetch` → 原样返回。上游协议是 start+poll（纯请求-响应，无 SSE），无需流式透传 |
| 模型清单（2026-10-01 Statsig `prism_codex_models`） | `gpt-6.1-sol`(6.1 Sol)、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-luna`；`gpt-6-astra` 已下线（上游对其路由 `codex_v2_restore_start` 且返回 400，无创建入口） |
| start+poll | 与议题描述一致：start 单次提交；`turn_state` 每轮更新；start 可能**直接返回 completed**（快/失败场景），不总是 `started` |
| 沙箱注入 | 四步（backend/1/new → resources-token 签发 → 交付沙箱 → Y 凭证注入）**缺任何一步都不是报错而是永远卡住**；`wait-for-sync` 单次 `wait_ms=10000` 可能只回 `syncing`，**必须轮询到 `status:"synced"` 才能 start**，否则 `workspace_sync_timeout` 504 |

### 二、OAuth access_token 适配的实测路径

```
1. 预置 cookie：prism_oai_access_token = <OAuth access_token（RS256 JWT，10 天）>
2. 真实浏览器访问 https://prism.openai.com/（自动通过 Cloudflare）
   → Prism 识别 access_token 并 Set-Cookie 签发 prism_session_token 等全套会话
3. 之后所有 /api/* 请求在该浏览器会话内正常工作
```

注意：

- **不要**试图把 access_token 同时塞进 `prism_session_token` —— 无效。
- 裸 HTTP 客户端（即使带上浏览器导出的 `cf_clearance`/`__cf_bm`）在
  2026-10 版 Cloudflare 策略下仍被 TLS 指纹校验拒绝；`curl_cffi` 的
  chrome120/124/131 指纹同样 403。**浏览器级客户端是当前唯一可靠通道**。
- OAuth refresh_token（`rt.1.*`）的刷新：向 `auth.openai.com/oauth/token`
  提交 `grant_type=refresh_token` 时，无论 client_id 用 Prism 的
  `app_jqKb52JverFFcl5GP4axT8QY` 还是 Codex CLI 的
  `app_EMoamEEZ73f0CkXaXp7hrann`、无论 JSON/form 编码，均返回
  `invalid_client`（已按 Codex CLI 源码 `codex-rs/login/src/auth/manager.rs`
  完整仿真：JSON 编码 + `originator: codex_cli_rs` header）。
  **建议 P1 阶段用真实 CLI 触发一次刷新并抓包对照**，不要按标准 OAuth2 想当然。

### 三、工具闭环（桥接）实测要点

上游是 server-side tools 架构：模型在云端沙箱内消化工具，客户端永远只拿到
终态文本 —— **本地 CLI 的工具链一次也不会被触发**。可行的桥接方式是
"上游大脑 + 本地手脚"：

1. 检测 Codex 请求 `input` 里的 `additional_tools` 条目（顶层 `tools` 为 null）；
2. 注入受控提示：明确"你没有执行环境"，要求模型把操作以受控围栏输出；
3. 解析围栏内容 —— **模型经常输出裸 shell 而非 JS**，网关必须兜底把它
   包装成 `exec_command` 调用，否则进 V8 就是 `SyntaxError` 死循环；
4. 包装为 Responses 的 `custom_tool_call`（`{call_id, name:"exec", input:<JS>}`）
   返回；Codex CLI 在本地 V8 isolate 执行（`exec_command` 起真 shell），
   文件真实落盘；
5. `custom_tool_call_output` 按原 `call_id` 回灌，并把上一轮工具调用回放为
   assistant 文本，让上游维持"自己做过什么"的记忆。

实测：Windows 11 + Codex CLI 0.154，通过该桥完成本地文件的
**写入（Set-Content）/读回验证（Get-Content）/删除（Remove-Item）** 全闭环，
模型能在 PowerShell 报错后自我纠正 bash 语法。

### 四、运维要点

- **沙箱生命周期**：无心跳约 20-30 分钟回收；活跃会话期间前端每 5 秒
  `GET /s/sandboxes/proxy/heartbeat`。长对话网关必须自带心跳。
- **重试窗口**：Codex CLI 流断后自动重连约 12 分钟（5 次 × 2m23s）；
  网关的"沙箱未就绪"重试窗口必须覆盖它（建议 ≥5 分钟、线性/指数退避），
  否则 CLI 在等、网关已放弃。
- **模型清单动态化**：Statsig `prism_codex_models` 是唯一事实来源，
  模型会被下线（astra 案例）与新增（6.1 Sol / 6 Luna 案例），
  网关不能硬编码模型列表。

### 五、参考实现

上述桥接（含 Responses API 事件转换、`custom_tool_call` 翻译、
`additional_tools` 处理、结果回灌、按页分包的管理控制台）已在
[alanbulan/oai-prism](https://github.com/alanbulan/oai-prism) 实现并通过
上述实测，可作对照（Go 实现 + Node 浏览器通道 sidecar +
React/antd 管理台）。

---

## English (summary)

Prism web backend uses start+poll; Cloudflare now enforces **TLS fingerprint
validation** on prism.openai.com — plain HTTP clients (Go/Python/curl_cffi
with any Chrome profile) get `403 {"error":"Request verification failed"}`
even with fresh `cf_clearance`; only a real browser (playwright-driven
Chrome) passes. Viable gateway design: a resident browser sidecar that
forwards upstream requests via in-page `fetch` (upstream protocol is
request/response, no SSE passthrough needed).

OAuth `access_token` (RS256, 10-day) cannot replace the session cookie, but
seeding it as cookie `prism_oai_access_token` and visiting the site makes
Prism mint a full session — that is the correct login-adapter path.
`refresh_token` refresh via `auth.openai.com/oauth/token` returns
`invalid_client` for both known client_ids; verify against a real CLI
capture before implementing (P1).

Model manifest (2026-10-01, Statsig `prism_codex_models`):
`gpt-6.1-sol`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-6-luna`;
`gpt-6-astra` is retired (routed to `codex_v2_restore_start` → 400).

A working reference bridge (tool loop with `custom_tool_call`,
local file write/read/delete verified end-to-end with Codex CLI 0.154 on
Windows) lives at <https://github.com/alanbulan/oai-prism>.
