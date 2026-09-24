# Sub2API 插件调度能力（openai.account.scheduling.v1）

本文说明如何在 Sub2API 宿主中新增一个**账号调度能力**，让插件参与选号。这是
[`docs/PLUGIN_DEVELOPMENT.md`](../../../docs/PLUGIN_DEVELOPMENT.md) 描述的官方插件体系之上的扩展，
遵循同一套进程协议、清单格式与安全边界。

## 为什么需要它

Sub2API 原有的插件能力只有一个：`openai.oauth.outbound_transport.v1`（出站传输）。
它在**宿主选完账号之后**才被调用，插件只能影响"怎么出网"，不能影响"用哪个账号"。

实测确认（`internal/service/` 下 `openai_account_scheduler.go`、`gateway_scheduling.go`、
`lane_sticky.go` 对 `pluginManager` 零引用）：**选号之前不存在任何插件钩子**。

调度能力补上了这一层。它让插件可以在宿主完成全部硬门槛过滤后，对**合法候选集**
表达一次偏好。

## 设计边界（最重要的一节）

这三条约束决定了这个能力可以安全上线，任何扩展都必须遵守：

### 1. 插件只提名，不决策

宿主的调用时机在 `selectByLoadBalance` 内部：

```text
listSchedulableAccounts          宿主拉取可调度账号
  → filterGrok* / 分组隐私 / 模型能力 / 传输能力 …   全部硬门槛过滤
  → GetAccountsLoadBatch         读权威并发负载
  → resolveSchedulingNomination  ▶ 插件在此被询问一次
  → trySelectByLoadBalancePool   候选池拆分与原生排序
  → tryAcquireOpenAISelectionOrderWithBudget   ▶ 抢槽仍是宿主独占
  → finalizeOpenAISelectionResult             ▶ lane 水合仍是宿主独占
```

插件拿到的候选集**已经经过全部硬门槛**：状态、平台、分组、传输能力、隐私设置、
模型能力、配额、利润门。因此插件不可能提名一个非法账号 —— 它只能从给定集合里挑。

### 2. 提名零副作用

提名结果只做一件事：把该账号移到 `selectionOrder` 首位。**不改变集合**。

抢并发槽位（Redis 在途计数）、lane 选择、粘性写入、失败重放全部仍是宿主原生逻辑。
插件提名一个"看着闲、实际瞬间被占满"的账号时，最坏结果是退化回原生排序 ——
宿主沿原序列继续尝试下一个候选，请求不会失败。

跨池不搬运：提名账号若属于订阅池而当前在选常规池，置顶自然失败。这是刻意的 ——
跨池搬运会破坏 `SubscriptionPriority` 语义。

### 3. 失败一律降级

以下情况全部 fail-open，宿主完全沿用自身排序：

| 情况 | 行为 |
| --- | --- |
| 未启用调度能力 / 灰度未命中 | 零额外开销（`openAIAccountSchedulingEnabled` 短路） |
| 插件返回 `handled=false` | 忽略插件意见 |
| 调用超时（> 200ms） | 忽略插件意见 |
| 插件进程退出 / RPC 报错 | 忽略插件意见 |
| 提名账号不在候选集内 | 丢弃提名（安全闸门） |
| 提名账号不在本候选池 | 忽略置顶，保持原生顺序 |
| 插件未实现该 RPC | 返回 `Unimplemented`，宿主静默跳过 |

`pluginSchedulingTimeout = 200ms` 是刻意的硬上限：选号在请求关键路径上，
插件卡住时宿主宁可退回自身排序，也不能让每个请求都等多秒。

## 契约

### 能力声明（manifest.json）

```json
{
  "capabilities": [
    { "id": "openai.oauth.outbound_transport.v1", "platform": "openai", "account_type": "oauth" },
    { "id": "openai.account.scheduling.v1",      "platform": "openai", "account_type": "oauth" }
  ]
}
```

两个能力可独立声明。只声明调度能力而不声明出站传输是合法的：插件只参与选号，
出站仍走宿主原生 HTTP 路径。

### RPC

```proto
rpc NominateAccount(NominateAccountRequest) returns (NominateAccountResponse);
```

`NominateAccountRequest` 携带：请求标识、平台、`session_hash`、`previous_response_id`、
模型、分组 ID、宿主粘性账号提示，以及**候选集** `repeated ScheduleCandidate`。

`ScheduleCandidate` 每个候选含：账号 ID、账号类型、优先级、在途并发、并发上限、
等待数、负载率（0-1 比率）、错误率、TTFT。**不含**账号名、凭据、代理、分组 ——
插件只需要判断"哪个账号更闲、更稳"，不需要知道它是谁。

```proto
message NominateAccountResponse {
  bool   handled = 1;               // false = 宿主保持自身排序
  int64  nominated_account_id = 2;  // 必须是候选集内的 ID；0 = 无偏好
  string reason = 3;                // 短标记，供日志观测
  bool   affinity_hit = 4;          // 是否来自亲和命中
}
```

### 版本协商

`GetInfoResponse.scheduling_api_version` 与 `pluginapi/v1.SchedulingAPIVersion` 比对。
与 `HostService` 一样，调度能力独立于 `TransportAPIVersion`：新增调度能力不会使
既有插件失效，未实现该 RPC 的旧插件返回 `Unimplemented` 并被静默跳过。

## 账号目录依赖

调度插件需要读到每个账号的 `schedulable` 与在途负载，否则会对着一个正在限流的
账号反复提名。因此调度能力在 `pluginCapabilityAccountScopeGrants` 中授予了与出站
传输**完全相同**的账号范围（OpenAI OAuth），不额外扩权。

插件通过 `InitHostServices` 拨号回宿主后调用 `ListAccounts` 获取。

## 观测

`OpenAIAccountScheduleDecision` 新增三个字段：

| 字段 | 含义 |
| --- | --- |
| `PluginNominatedAccountID` | 本次选号插件提名了谁（0 = 未参与） |
| `PluginAffinityHit` | 提名是否来自亲和命中 |
| `Layer` | 提名**确实被选中**时为 `plugin_scheduler`，否则保持 `load_balance` |

`Layer` 只在提名账号真正被选中时才改写：提名了但抢槽失败、宿主选了别人时，
把这次选号归因于插件会让指标失真。

宿主侧日志事件：

- `plugin_scheduling_unimplemented`（debug）：插件未实现该 RPC。
- `plugin_scheduling_nominate_failed`（warn）：调用失败，已降级。
- `plugin_scheduling_nomination_out_of_scope`（warn）：插件提名了候选集之外的账号。

## 部署

1. 打包插件（见 `tools/packager`），获得 `.s2plugin`。
2. 生成发布者密钥（`tools/keygen`），把公钥写入宿主配置：

```yaml
plugins:
  allow_unsigned: false
  trusted_publishers:
    ziyue67-publisher-v1: "BASE64_ED25519_PUBLIC_KEY"
```

3. 管理后台上传包 → 默认停用 → 确认兼容性、签名与诊断 → 按账号灰度启用。

调度能力与出站传输各自有独立的灰度绑定（`rollout_percent`，按账号 ID 稳定分桶）。
两个能力复用同一分桶函数，因此同一账号在两个能力上的命中结果一致，行为可预期。

## 涉及文件

宿主侧（本仓库）：

| 文件 | 作用 |
| --- | --- |
| `backend/pkg/pluginapi/proto/sub2api/plugin/v1/plugin.proto` | 契约源文件 |
| `backend/pkg/pluginapi/v1/runtime.go` | `SchedulingAPIVersion` 常量 |
| `backend/pkg/pluginapi/v1/manifest.schema.json` | 能力白名单（`enum`） |
| `backend/internal/service/plugin_manifest.go` | 能力校验白名单 `supportedPluginCapabilities` |
| `backend/internal/service/plugin_manager.go` | 账号范围授权表 |
| `backend/internal/service/plugin_scheduling.go` | 路由、灰度、RPC 调用与安全闸门 |
| `backend/internal/service/plugin_scheduling_hook.go` | 候选构造、提名置顶、归因标记 |
| `backend/internal/service/openai_account_scheduler.go` | 调度器接入点 |
| `backend/internal/service/gateway_service.go` | `AccountSelectionResult` 归因字段 |

## 测试

```bash
cd backend
go test -tags=unit -count=1 ./pkg/pluginapi/...           # 清单 schema
go test -tags=unit -count=1 -run 'TestPluginScheduling|TestBuildPluginScheduling|TestApplyNomination' ./internal/service/
```

进程级端到端（需要真实插件包）：

```bash
SUB2API_TEST_PLUGIN_PACKAGE=/path/to/plugin.s2plugin \
  go test -tags=unit -count=1 -run 'TestRealPackageInstalls|TestCPAAdvancedCoreProcessIntegration' -v ./internal/service/
```

后者会真正拉起插件进程，验证 go-plugin 握手、gRPC 帧序、配置协商、提名语义与
出站转发，以及"429 后解除亲和并改提名"的闭环。
