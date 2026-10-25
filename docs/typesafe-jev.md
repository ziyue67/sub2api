# TypeSafe / Jev System One

> 整理自上游 [Wei-Shaw/sub2api v0.2.13](https://github.com/Wei-Shaw/sub2api/tree/v0.2.13) README 中的 TypeSafe / Jev 小节（上游 PR [#7425](https://github.com/Wei-Shaw/sub2api/pull/7425)）。本 fork 的 README 结构不同，因此单独成文。

内容审计的新增分类、默认关闭策略和验证边界见 [TypeSafe 内容审计分类](typesafe-content-audit.md)。

## 使用说明

Sub2API 支持使用 TypeSafe API Key 账户，通过 Jev 原生、非流式的 System One 协议调用模型。

- 平台：`typesafe`；账号类型：API Key
- 默认上游：`https://api.typesafe.ai`
- 对外端点：`POST /v1/systemone`
- 模型：`jev-latest`，TypeSafe 分组的 `/v1/models` 也会返回该模型
- 问题类型：`noul`、`choice`、`score`

### 编辑上游 URL

- **风控内容审计**：管理后台 → 风控 → 设置 → 基础，选择 **TypeSafe AI**，在“Key 可用状态”的对应 Key 行查看 URL，点击“编辑此 Key URL”后保存或取消。行内保存独立生效并读回持久化值，只影响该 Key；不会回填真实 Key。顶部“公共默认 URL”仅供未设置逐 Key 覆盖的旧配置回退使用，留空恢复 `https://api.typesafe.ai`。逐 Key URL 不接受空值，空值不表示删除或恢复默认；并发修改冲突时需刷新后重试。
- **模型渠道账号**：在账号管理中新建或编辑 TypeSafe / Jev API Key 账号，使用已有的 **Base URL** 字段；该配置与风控审计引擎的 URL 相互独立。
- 支持服务根地址、以 `/v1` 结尾的 API 地址或完整 `/v1/systemone` 端点；尾斜杠会被规范化，代理路径前缀会被保留。例如 `https://relay.example/typesafe/v1/` 和 `https://relay.example/typesafe/v1/systemone` 都会请求 `https://relay.example/typesafe/v1/systemone`，不会重复追加 `/v1`。
- URL 不接受内嵌用户名/密码、查询参数或片段；API Key 仍通过 Authorization 请求头发送。该编辑能力不改变既有出站 URL 白名单、私网访问或 TLS 校验策略，也不会因输入地址而自动开启 HTTP 或私网访问。

**401 与 URL 编辑是两件事**：401 表示请求收到鉴权拒绝，单凭该状态无法确定是凭据、上游权限、地址还是中间代理配置所致。审计 Key 的 401/403 继续按既有规则冻结 10 分钟；仅保存 URL 不会解冻、删除 Key 或把失败状态改成成功。这里的编辑说明不代表已诊断任何线上 401。

请求和成功响应保持 System One 原生 JSON 结构。该端点不兼容 Chat Completions、Responses、Anthropic Messages 或流式客户端。

问题校验遵循 TypeSafe OpenAPI 的线上协议 schema（SDK v0.5.7 也使用该 schema）。所有问题的 `instructions` 都可以省略或为 `null`。Noul 的 `criteria` 可以省略或为 `null`，其中 `true`/`false` 的描述和 Choice 描述支持字符串、对象、数组或 `null`。Score 的 `criteria` 必须是至少包含一档描述的数组，每档支持字符串、对象或数组；单档也合法。SDK 的整数键 Score 映射会由 SDK 在发送前转换为数组。

```bash
curl https://your-sub2api.example.com/v1/systemone \
  -H 'Authorization: Bearer sk-your-sub2api-key' \
  -H 'Content-Type: application/json' \
  --data '{"model":"jev-latest","state":"待评估文本","questions":{"safety":{"type":"noul","instructions":"评估文本是否不安全"}}}'
```

`jev-latest` 内置价格为输入 `$0.042/百万 tokens`、输出 `$0`，渠道定价可以覆盖。凭据、欠费、权限、限流、过载、服务端和网络错误（`401`、`402`、`403`、`429`、`529`、`5xx`、传输错误）沿用现有账号错误策略（含自定义错误码与临时不可调度规则）并切换账号；请求错误（`400`、`413`、`422`）不会切换账号重试，也不会改变账号状态。TypeSafe 分组（以及路由到 TypeSafe 的 Composite 请求）调用 Messages、Chat Completions、Responses、count_tokens 时返回 `404`。

## English

Sub2API supports TypeSafe API-key accounts through Jev's native, non-streaming System One protocol.

- Platform: `typesafe`; account type: API Key
- Default upstream: `https://api.typesafe.ai`
- Public endpoint: `POST /v1/systemone`
- Model: `jev-latest`, also returned by `/v1/models` for TypeSafe groups
- Questions: `noul`, `choice`, and `score`

### Editing the upstream URL

- **Content auditing**: In Risk Control → Settings → Basic, select **TypeSafe AI**. Each stored key in Key Availability shows its URL and an **Edit this key URL** action with Save and Cancel. Saving reads back the persisted URL and affects only that key, without returning its secret. The **Shared default URL** applies to legacy keys without overrides; leaving that default blank restores `https://api.typesafe.ai`. Per-key URLs cannot be empty; an empty value never deletes an override or resets it to the default. Refresh and retry after a concurrent-edit conflict.
- **Model channel accounts**: Create or edit a TypeSafe / Jev API-key account in account management using its existing **Base URL** field. Account URLs and content-audit engine URLs are separate settings.
- A server root, API base ending in `/v1`, or complete `/v1/systemone` endpoint is accepted. Trailing slashes are normalized and proxy prefixes are preserved. Both `https://relay.example/typesafe/v1/` and `https://relay.example/typesafe/v1/systemone` target `https://relay.example/typesafe/v1/systemone`, with no duplicated API path.
- Embedded credentials, query parameters and fragments are rejected; API keys still use the Authorization header. Editing does not change existing outbound URL allowlists, private-network access or TLS verification policies, or automatically enable HTTP or private-network access.

**A 401 does not diagnose the URL**: it means authentication was rejected, but does not establish whether credentials, upstream permissions, the endpoint or an intermediary caused the rejection. Audit keys still freeze for 10 minutes on 401/403. Saving a URL does not unfreeze or delete keys, or turn a failed status into success. These instructions do not diagnose any production 401.

Requests and successful responses retain the native System One JSON structure. This endpoint is not compatible with Chat Completions, Responses, Anthropic Messages, or streaming clients.

Question validation follows the TypeSafe OpenAPI wire schema (also used by SDK v0.5.7). `instructions` may be omitted or `null` for all question types. Noul `criteria` may be omitted or `null`; its `true`/`false` descriptions and Choice descriptions accept strings, objects, arrays, or `null`. Score `criteria` must be a non-empty array of string, object, or array descriptions; a single level is valid. SDK integer-keyed Score maps are normalized to arrays by the SDK before sending.

```bash
curl https://your-sub2api.example.com/v1/systemone \
  -H 'Authorization: Bearer sk-your-sub2api-key' \
  -H 'Content-Type: application/json' \
  --data '{"model":"jev-latest","state":"Text to evaluate","questions":{"safety":{"type":"noul","instructions":"Evaluate whether the text is unsafe"}}}'
```

The built-in `jev-latest` price is `$0.042` per million input tokens and `$0` for output tokens. Channel pricing can override both values. Credential, billing, permission, rate-limit, overload, server, and network failures (`401`, `402`, `403`, `429`, `529`, `5xx`, transport errors) use the existing account error policy (including custom error codes and temporary-unschedulable rules) and fail over to another account; request errors (`400`, `413`, and `422`) are returned without retrying another account and never change account state. TypeSafe groups (and Composite requests routed to TypeSafe) reject Messages, Chat Completions, Responses, and count_tokens requests with `404`.
