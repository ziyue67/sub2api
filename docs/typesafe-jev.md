# TypeSafe / Jev System One

> 整理自上游 [Wei-Shaw/sub2api v0.2.13](https://github.com/Wei-Shaw/sub2api/tree/v0.2.13) README 中的 TypeSafe / Jev 小节（上游 PR [#7425](https://github.com/Wei-Shaw/sub2api/pull/7425)）。本 fork 的 README 结构不同，因此单独成文。

## 使用说明

Sub2API 支持使用 TypeSafe API Key 账户，通过 Jev 原生、非流式的 System One 协议调用模型。

- 平台：`typesafe`；账号类型：API Key
- 默认上游：`https://api.typesafe.ai`
- 对外端点：`POST /v1/systemone`
- 模型：`jev-latest`，TypeSafe 分组的 `/v1/models` 也会返回该模型
- 问题类型：`noul`、`choice`、`score`

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

Requests and successful responses retain the native System One JSON structure. This endpoint is not compatible with Chat Completions, Responses, Anthropic Messages, or streaming clients.

Question validation follows the TypeSafe OpenAPI wire schema (also used by SDK v0.5.7). `instructions` may be omitted or `null` for all question types. Noul `criteria` may be omitted or `null`; its `true`/`false` descriptions and Choice descriptions accept strings, objects, arrays, or `null`. Score `criteria` must be a non-empty array of string, object, or array descriptions; a single level is valid. SDK integer-keyed Score maps are normalized to arrays by the SDK before sending.

```bash
curl https://your-sub2api.example.com/v1/systemone \
  -H 'Authorization: Bearer sk-your-sub2api-key' \
  -H 'Content-Type: application/json' \
  --data '{"model":"jev-latest","state":"Text to evaluate","questions":{"safety":{"type":"noul","instructions":"Evaluate whether the text is unsafe"}}}'
```

The built-in `jev-latest` price is `$0.042` per million input tokens and `$0` for output tokens. Channel pricing can override both values. Credential, billing, permission, rate-limit, overload, server, and network failures (`401`, `402`, `403`, `429`, `529`, `5xx`, transport errors) use the existing account error policy (including custom error codes and temporary-unschedulable rules) and fail over to another account; request errors (`400`, `413`, and `422`) are returned without retrying another account and never change account state. TypeSafe groups (and Composite requests routed to TypeSafe) reject Messages, Chat Completions, Responses, and count_tokens requests with `404`.
