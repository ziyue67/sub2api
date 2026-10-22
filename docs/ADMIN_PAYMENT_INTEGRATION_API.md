# ADMIN_PAYMENT_INTEGRATION_API

> 单文件中英双语文档 / Single-file bilingual documentation (Chinese + English)

---

## 中文

### 目标
本文档用于对接外部支付系统（如 `sub2apipay`）与 Sub2API 的 Admin API，覆盖：
- 支付成功后充值
- 用户查询
- 人工余额修正
- 自定义嵌入页面的非认证参数与安全边界

### 基础地址
- 生产：`https://<your-domain>`
- Beta：`http://<your-server-ip>:8084`

### 认证
推荐使用：
- `x-api-key: admin-<64hex>`
- `Content-Type: application/json`
- 幂等接口额外传：`Idempotency-Key`

说明：管理员 JWT 也可访问 admin 路由，但服务间调用建议使用 Admin API Key。

### 1) 一步完成创建并兑换
`POST /api/v1/admin/redeem-codes/create-and-redeem`

用途：原子完成“创建兑换码 + 兑换到指定用户”。

请求头：
- `x-api-key`
- `Idempotency-Key`

请求体示例：
```json
{
  "code": "s2p_cm1234567890",
  "type": "balance",
  "value": 100.0,
  "user_id": 123,
  "notes": "sub2apipay order: cm1234567890"
}
```

幂等语义：
- 同 `code` 且 `used_by` 一致：`200`
- 同 `code` 但 `used_by` 不一致：`409`
- 缺少 `Idempotency-Key`：`400`（`IDEMPOTENCY_KEY_REQUIRED`）

curl 示例：
```bash
curl -X POST "${BASE}/api/v1/admin/redeem-codes/create-and-redeem" \
  -H "x-api-key: ${KEY}" \
  -H "Idempotency-Key: pay-cm1234567890-success" \
  -H "Content-Type: application/json" \
  -d '{
    "code":"s2p_cm1234567890",
    "type":"balance",
    "value":100.00,
    "user_id":123,
    "notes":"sub2apipay order: cm1234567890"
  }'
```

### 2) 查询用户（可选前置校验）
`GET /api/v1/admin/users/:id`

```bash
curl -s "${BASE}/api/v1/admin/users/123" \
  -H "x-api-key: ${KEY}"
```

### 3) 余额调整（已有接口）
`POST /api/v1/admin/users/:id/balance`

用途：人工补偿 / 扣减，支持 `set` / `add` / `subtract`。

请求体示例（扣减）：
```json
{
  "balance": 100.0,
  "operation": "subtract",
  "notes": "manual correction"
}
```

```bash
curl -X POST "${BASE}/api/v1/admin/users/123/balance" \
  -H "x-api-key: ${KEY}" \
  -H "Idempotency-Key: balance-subtract-cm1234567890" \
  -H "Content-Type: application/json" \
  -d '{
    "balance":100.00,
    "operation":"subtract",
    "notes":"manual correction"
  }'
```

### 4) 自定义页面嵌入参数与认证边界（iframe / 新窗口一致）
用户侧自定义页面通过 `buildEmbeddedUrl` 构造嵌入地址，iframe 和“在新窗口打开”使用相同 URL。参数包括：

- `user_id`（当前用户 ID，存在时添加）
- `theme`（`light` / `dark`）
- `lang`（当前界面语言，存在时添加）
- `ui_mode`（固定 `embedded`）
- `src_host`（浏览器中来源页的 origin）
- `src_url`（浏览器中来源页的地址，移除 query 和 fragment）

**不再透传主登录 JWT。**构造地址时会移除历史保留的 `token` 查询参数（包括同名重复项），其余目标业务参数和锚点保留。iframe 使用 `referrerpolicy="no-referrer"`；新窗口链接使用 `noopener noreferrer`。

示例：
```text
https://pay.example.com/pay?user_id=123&theme=light&lang=zh&ui_mode=embedded&src_host=https%3A%2F%2Fsub.example.com&src_url=https%3A%2F%2Fsub.example.com%2Fcustom%2Fpay
```

这些参数仅提供显示和来源上下文，**不能作为身份或授权凭据**；`user_id` 可以被访问者改写。外部系统必须独立验证身份并在服务端执行授权，Admin API Key 只能保存在受信任后端，不能放进 URL 或浏览器页面。

依赖旧 `token` 参数的集成需要迁移到独立认证流程。如需免登录，应另行实现限定接收方、权限和有效期的专用票据交换；当前嵌入路径未提供自动 SSO 或票据交换接口，不应继续向第三方传递主登录 JWT。

### 5) 失败处理建议
- 支付成功与充值成功分状态落库
- 回调验签成功后立即标记“支付成功”
- 支付成功但充值失败的订单允许后续重试
- 重试保持相同 `code`，并使用新的 `Idempotency-Key`

### 6) `doc_url` 配置建议
- 查看链接：`https://github.com/Wei-Shaw/sub2api/blob/main/docs/ADMIN_PAYMENT_INTEGRATION_API.md`
- 下载链接：`https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/docs/ADMIN_PAYMENT_INTEGRATION_API.md`

---

## English

### Purpose
This document describes the minimal Sub2API Admin API surface for external payment integrations (for example, `sub2apipay`), including:
- Recharge after payment success
- User lookup
- Manual balance correction
- Non-authentication parameters and security boundaries for embedded custom pages

### Base URL
- Production: `https://<your-domain>`
- Beta: `http://<your-server-ip>:8084`

### Authentication
Recommended headers:
- `x-api-key: admin-<64hex>`
- `Content-Type: application/json`
- `Idempotency-Key` for idempotent endpoints

Note: Admin JWT can also access admin routes, but Admin API Key is recommended for server-to-server integration.

### 1) Create and Redeem in one step
`POST /api/v1/admin/redeem-codes/create-and-redeem`

Use case: atomically create a redeem code and redeem it to a target user.

Headers:
- `x-api-key`
- `Idempotency-Key`

Request body:
```json
{
  "code": "s2p_cm1234567890",
  "type": "balance",
  "value": 100.0,
  "user_id": 123,
  "notes": "sub2apipay order: cm1234567890"
}
```

Idempotency behavior:
- Same `code` and same `used_by`: `200`
- Same `code` but different `used_by`: `409`
- Missing `Idempotency-Key`: `400` (`IDEMPOTENCY_KEY_REQUIRED`)

curl example:
```bash
curl -X POST "${BASE}/api/v1/admin/redeem-codes/create-and-redeem" \
  -H "x-api-key: ${KEY}" \
  -H "Idempotency-Key: pay-cm1234567890-success" \
  -H "Content-Type: application/json" \
  -d '{
    "code":"s2p_cm1234567890",
    "type":"balance",
    "value":100.00,
    "user_id":123,
    "notes":"sub2apipay order: cm1234567890"
  }'
```

### 2) Query User (optional pre-check)
`GET /api/v1/admin/users/:id`

```bash
curl -s "${BASE}/api/v1/admin/users/123" \
  -H "x-api-key: ${KEY}"
```

### 3) Balance Adjustment (existing API)
`POST /api/v1/admin/users/:id/balance`

Use case: manual correction with `set` / `add` / `subtract`.

Request body example (`subtract`):
```json
{
  "balance": 100.0,
  "operation": "subtract",
  "notes": "manual correction"
}
```

```bash
curl -X POST "${BASE}/api/v1/admin/users/123/balance" \
  -H "x-api-key: ${KEY}" \
  -H "Idempotency-Key: balance-subtract-cm1234567890" \
  -H "Content-Type: application/json" \
  -d '{
    "balance":100.00,
    "operation":"subtract",
    "notes":"manual correction"
  }'
```

### 4) Custom page parameters and authentication boundaries (iframe and new tab)
User-facing custom pages use `buildEmbeddedUrl` to construct the embedded URL. The iframe and "Open in new tab" link use the same URL, with these parameters:

- `user_id` (the current user ID, when available)
- `theme` (`light` / `dark`)
- `lang` (the current UI language, when available)
- `ui_mode` (fixed: `embedded`)
- `src_host` (the source page's origin, in the browser)
- `src_url` (the source page's URL with its query and fragment removed, in the browser)

**The main login JWT is no longer forwarded.** URL construction removes the legacy reserved `token` query parameter, including duplicate entries, while preserving other target business parameters and the target fragment. The iframe uses `referrerpolicy="no-referrer"`; the new-tab link uses `noopener noreferrer`.

Example:
```text
https://pay.example.com/pay?user_id=123&theme=light&lang=zh&ui_mode=embedded&src_host=https%3A%2F%2Fsub.example.com&src_url=https%3A%2F%2Fsub.example.com%2Fcustom%2Fpay
```

These parameters provide presentation and source context only; **they are not proof of identity or authorization**. Visitors can modify `user_id`. External systems must authenticate users independently and enforce authorization on the server. Keep Admin API Keys on trusted backends only, never in URLs or browser pages.

Integrations that depend on the old `token` parameter must migrate to an independent authentication flow. If seamless sign-in is needed, implement a separate ticket exchange restricted by recipient, permissions, and lifetime. This embedding path does not provide automatic SSO or a ticket-exchange endpoint, and must not continue forwarding the main login JWT to third parties.

### 5) Failure handling recommendations
- Persist payment success and recharge success as separate states
- Mark payment as successful immediately after verified callback
- Allow retry for orders with payment success but recharge failure
- Keep the same `code` for retry, and use a new `Idempotency-Key`

### 6) Recommended `doc_url`
- View URL: `https://github.com/Wei-Shaw/sub2api/blob/main/docs/ADMIN_PAYMENT_INTEGRATION_API.md`
- Download URL: `https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/docs/ADMIN_PAYMENT_INTEGRATION_API.md`
