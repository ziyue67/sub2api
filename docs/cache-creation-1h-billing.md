# 账号级缓存创建按 1h 计费

## 设置

管理员在 Anthropic API Key 账号的创建或编辑页面中，使用独立开关 **缓存创建按 1h 计费**。

- 配置：`accounts.extra.cache_creation_billing_1h_enabled`，仅布尔 `true` 启用。
- 默认关闭，不自动启用任何现有账号。
- 与 **强制真实 1h 缓存**（请求 TTL 改写）独立；两个开关可以分别保存。
- 开启计费规则但没有开启请求 TTL 改写时，管理员页面会提示。
- 编辑页面显式保存布尔值，管理员操作审计包含变更前后值、操作者、时间和账号路径。
- 账号保存沿用仓库的调度 outbox / 配置缓存传播机制；单次请求在转发入口固定配置快照，之后开关变化不影响该在途请求。

## 请求与计费

请求 TTL 开关继续只修改已有 ephemeral 断点并补齐所需 beta。新计费开关不改请求；开启后，客户端收到的模型 JSON/SSE Usage 按本站 1h 计费口径反馈。

对启用计费规则的账号，如果原始缓存创建明细有效：

```text
本站计费 5m = 0
本站计费 1h = 上游 5m + 上游 1h
转换数量 = 上游 5m
```

费用使用本次有效模型、渠道/分组价格、长上下文规则和客户倍率。只转换缓存创建，不增加总 tokens，不改变输入、输出或缓存读取。全为 1h 或没有创建量时不产生转换标记。

创建总量与拆分不一致、没有 TTL 明细、负数/溢出或不能解析适用的缓存拆分价格时，不宣称转换成功；保留原分类，管理员可见异常状态。明确配置免费缓存创建价格的记录也保留原分类，标为 `unavailable_pricing`，不凭空增加费用。

关闭后，新请求恢复原计费规则。历史账单和余额不重新计算。余额、订阅、API Key 配额和使用记录沿用既有结算及去重机制。

## 展示与权限

| 位置 | 行为 |
|---|---|
| 客户使用记录及导出 | 使用转换后的计费数量，正常显示 1h 和实际费用，不显示转换注释 |
| 管理员使用记录 | 转换行标记 R，提示“上游回报 5m → 本站按 1h 计费” |
| 管理员详情及导出 | 保留原始 5m/1h、创建总量、读取量、规则版本、转换数量、转换前后创建费和实际费用增量 |
| 普通用户及观察员 API | 不返回 `cache_creation_billing` 审计字段 |
| 模型 JSON/SSE 响应 | 开关开启时向下游反馈 1h 分类；内部账单和管理员审计仍保留上游原始 5m/1h |

客户看到的 1h 是本站向下游反馈的计费档位。管理员内部仍可看到原始上游分类；此功能不验证或延长上游真实缓存存活时间。

## 下游中转站账单查询

下游可以使用发起模型请求的同一 API Key 查询该请求的站内计费记录：

```http
GET /v1/usage?request_id=client%3Aexample-request-uuid
Authorization: Bearer <same-api-key>
```

这是已有 `/v1/usage` 的可选查询模式。不带 `request_id` 时保持原有额度统计响应；带该参数时返回单条计费记录。仅允许当前用户的当前 Key 查询，不支持管理员代查、跨 Key 查询或模糊搜索。响应带 `Cache-Control: no-store`。

对于普通 Messages / Responses / Chat Completions 请求，用模型响应头 `X-Client-Request-ID` 的值加上 `client:` 前缀作为查询 ID。网关会生成或规范化此 ID；必须保存响应头中的值，不要假定客户端自行发送的同名头会被沿用。每个独立生成请求对应独立 ID，不能用会话 ID 代替。这里不是上游响应体的 message ID，也不是上游站点的 request ID。异步视频、独立搜索等使用特殊计费事件 ID 的路径不属于此接入约定。

以下是上游回报 360 个 5m tokens、本地成功按 1h 分类的一条示例（金额仅用于展示结构）：

```json
{
  "request_id": "client:example-request-uuid",
  "record_status": "recorded",
  "billing_basis": "site_billing",
  "billed_usage": {
    "input_tokens": 4,
    "output_tokens": 2,
    "cache_creation_input_tokens": 360,
    "cache_read_input_tokens": 1000,
    "cache_creation_5m_tokens": 0,
    "cache_creation_1h_tokens": 360
  },
  "upstream_cache_usage": {
    "cache_creation_input_tokens": 360,
    "cache_read_input_tokens": 1000,
    "ephemeral_5m_input_tokens": 360,
    "ephemeral_1h_input_tokens": 0,
    "ephemeral_5m_present": true,
    "ephemeral_1h_present": true
  },
  "cache_billing_status": "converted",
  "actual_cost": 0.09664109,
  "billing_type": 0,
  "created_at": "2026-10-16T06:25:00Z"
}
```

字段解释与接入规则：

- `billed_usage` 是保存下来的本站计费数量，适合下游按照自己的价格计算收费；1h 字段表示计费档位，不代表已经验证的物理缓存 TTL。
- `actual_cost`、`billing_type` 与使用记录保持一致。订阅、免费请求和扣费异常不能仅靠一个 usage 记录认定为余额支付成功；`record_status=recorded` 只表示已找到记录，不是支付成功状态。
- `upstream_cache_usage` 来自已有缓存审计快照，明确保留原始 5m/1h，`*_present` 区分零值与缺失。历史记录或没有审计快照时为 `null`，不根据账单反推上游原始数据。
- `cache_billing_status` 是缓存分类规则的状态；无审计快照时为 `null`。明细缺失或价格不适用的记录仍返回实际保存的原分类，不会仅因开关开启就硬报 1h。
- 不返回管理员的账号信息、上游成本估算、转换费用差值、网络地址或完整审计对象。
- 仍使用账号原有 `cache_creation_billing_1h_enabled` 开关决定分类；只读查询本身不新增策略开关，不修改历史账单，也不发起模型调用。

模型响应结束后，后台计费记录可能尚未写入。`404 billing_record_not_found` 同时覆盖未写入、错误 ID 和非当前 Key 的记录；下游应有限退避查询，例如 1、2、4、8 秒。仍未查到应标记待核对，不能视为免费，也不能为查询账单重新发送生成请求。数据库不可用返回 503；重复匹配返回 409；空、重复或超长 `request_id` 返回 400。

调用示例（环境变量由调用方提供，不在日志中打印 Key）：

```bash
curl --get "$BASE_URL/v1/usage" \
  --header "Authorization: Bearer $API_KEY" \
  --data-urlencode "request_id=client:$CLIENT_REQUEST_ID"
```

此接口需要下游显式接入。仅解析模型 JSON/SSE 的旧版中转程序仍会看到上游原始 Usage，不会因为新增查询接口自动按本站 1h 档位收费。应在下游测试环境验证“请求响应头 → 记录查询 → 分类与金额”后再启用下游结算规则。

## 数据与对账

迁移 `277_usage_log_cache_creation_billing.sql` 添加可空 JSONB 列 `usage_logs.cache_creation_billing`。旧记录保持 NULL，不猜测历史原始分类。写入路径覆盖单条、批量和 best-effort；读取通过同一列清单恢复审计数据。

原始 Usage 与转换后的账单分别保留。账号成本统计及账号配额使用转换前的费用依据，不因客户收费档位改变而自动抬高。它们仍是站内估算，不是已确认的上游实际账单；实际成本应以提供商账单核对。

## 验证与发布

功能测试覆盖：默认关闭和账号类型隔离；5m、1h、混合、零创建及异常 Usage；0/0.8/0.85 倍率；余额/订阅和账号成本分离；管理员、用户、观察员权限；开关往返保存；Messages、Responses、Chat Completions 的流式/非流式配置快照和原始响应保持；数据库审计序列化与真实 PostgreSQL 读写。

部署前须执行数据库迁移并保留备份。新列可空且开关默认关闭，旧代码回滚可以保留该列，不需要删列。代码发布不等于账号开关自动开启；需由管理员在目标账号明确配置。
