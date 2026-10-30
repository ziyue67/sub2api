# 客户 API 生图记录与失败请求可见性

## 基线与范围

- 来源：`https://github.com/ziyue67/sub2api`，`main` 基线 `003da3648d1d0969c3483f88603eb60e03a1708d`，实现分支 `fix/customer-image-records`。
- 客户通过站点 API 生图后，可在登录后的“我的 API 生图”查看自己已保留的结果，或由下游调用新的查询接口。
- 本次实现不改变生成响应、价格、扣费事务、退款或重试策略；不额外请求上游模型。

## 已确认的问题与未确认的原因

原有 `ImageArchiveCapture.Finish` 在没有采集到图片且返回结果没有图片数量时直接释放任务，导致这类终态请求没有留档。原有记录和文件接口仅对管理员开放，客户无法从站点取回已经留档的结果图。

`TestImageHistoryRetainsZeroImageAttempt` 对旧版 `Finish` 行为执行后失败：

```text
--- FAIL: TestImageHistoryRetainsZeroImageAttempt
zero-image terminal attempt disappeared from history
```

基线重放仅补入新增筛选字段以满足当前包编译，保留旧版完整 `Finish` 行为；随后恢复修改并用同一个断言验证通过。

这确认的是记录可见性缺口。客户报告的“失败但扣费”还可能来自结果传输中断、上游返回可计费结果但客户端未收到、或其他具体请求问题。本次没有拿生产争议请求作逐单归因，也不能据此认定全部扣费错误。

## 数据与权限

客户 DTO 只返回：记录 ID、站内客户端请求 ID、公开请求模型、生成状态、传输状态、泛化提示、已保存结果图数量及文件、本站结算金额/状态、创建时间和到期时间。

客户响应不包含：提示词、参考图、mask、修订提示词、上游 URL/请求 ID、账号/渠道/分组名称或 ID、内部成本、原始错误、凭据。文件接口再次校验归属、到期时间和 `result` 文件类型；其他用户、其他 Key、无效 ID 统一不可读取。

- 网站 JWT：只读当前用户的记录，可跨该用户自己的 Key。
- API Key：只读当前 Key 的记录；查询参数不能切换用户或 Key。
- 耗尽余额/Key 额度不妨碍查询；禁用、过期、无效凭据和原有用户/IP 等限制继续生效。
- 页面与文件返回 `Cache-Control: private, no-store`。文件仅提供归属明确、未过期的结果图。
- 管理员完整留档及原有权限保持有效。客户无法通过本接口删除记录、修改留档范围或读取参考图。

## 接口

网站使用标准 `{code,message,data}` 响应包装：

```text
GET /api/v1/user/image-history?page=1
GET /api/v1/user/image-history/:id
GET /api/v1/user/image-history/:id/files/:file?thumbnail=true
```

下游使用本站 API Key，以 JSON 数据直接响应，固定每页 20 条：

```text
GET /v1/images/history?page=1
GET /v1/images/history/:id
GET /v1/images/history/:id/files/:file?thumbnail=true
Authorization: Bearer <SITE_API_KEY>
```

列表含 `items/total/page/page_size`。记录内 `files[].url` 是同站受保护文件路径，读取仍需要相同身份认证。`image_count` 表示当前已保存的结果图数量，不是账单图片数。`preview_only=true` 表示仅有压缩预览图，下载也只能取得该预览图；不会把它标成原图。

这提供新的查询能力。现有 NewAPI/Sub2API 下游需要接入该接口才能同步历史图片，不会仅升级本站就自动增加下游页面。

## 状态与结算

生成状态 `succeeded/failed/unknown`、传输状态 `returned/interrupted/unknown` 与留档的 `saved/partial/failed` 分开。`returned` 仅表示服务端转发函数未报告错误，不证明客户端已展示或持久保存。

扣费优先通过采集时记录的**本站规范结算请求 ID**，与 `usage_billing_events` 的同一用户、同一 Key 匹配。旧记录缺规范 ID 时，必须仍保存结果图且没有明确失败的生成状态，才接受 `usage_logs` 中同一用户、Key、上游账号、图片数量大于零、请求 ID 唯一匹配的关联，再读取持久结算事件并核对账号；单条使用记录或费用估算不能证明实际扣款。

| 结算事件 | 客户状态 | 金额 |
| --- | --- | --- |
| `applied/complete`，金额大于零 | `charged` | 余额扣费或订阅额度消费金额，USD |
| `applied/complete`，金额为零 | `no_charge` | 0 |
| `pending` | `pending` | 不提前宣称已扣费 |
| 无规范 ID，且具备完整转发失败、明确零张输出、无结果图的尝试证据 | `no_charge` | 0，仅表示本站这次尝试未提交计费 |
| 无可靠关联、无事件、旧版/阻塞事件、身份不匹配、查询失败 | `unknown` | 不伪造金额，也不显示为免费 |

旧请求也会在读取时补查，不仅限更新后的请求，不需要重发、改库或重算费用：

- PR #138 已保存 `_generation_status=failed`、数值 `_image_count=0`、精确 `forward_failed` 错误且无结果图的尝试，两条 Images handler 均在提交用量前退出，可显示“本站未计费”。新捕获另外保存 `_billing_outcome=not_submitted_v1`；未知版本的契约不推断为免费。
- 部分出图但传输中断仍可能收费，不适用上述零输出规则。有规范结算关联时始终优先查真实事件，即使它与失败元数据矛盾；事件缺失或查询异常不能降级成免费。
- 旧成功记录可走上述唯一关联补查；关联不足、多个候选或仅有保存失败错误时保持 `unknown`。新旧记录都不通过“查不到账单”推断免费。
- 客户文案：`no_charge` 为“本站未计费”，`pending` 为“结算中”，`unknown` 为“暂无可核验扣费记录”。不公开内部失败原因或上游信息；本站未计费不证明上游未向站长收费。

没有结果的失败重试不绑定后来成功请求的结算事件，以免重复指认扣费。API 已返回的费用结果与本功能无关，不会由此追加扣费或自动退款。

## 留档与运行边界

继续使用管理员“生图记录 → 留档设置”的开关、账号/分组范围、容量、保留期与压缩选项。没有另建无限保存机制。只有范围命中并成功采集的请求可见；队列满、内存/磁盘限制、数据库失败、管理员删除和到期清理均可能造成记录或图片缺失。

新增失败终态留档，纯元数据记录不依赖图片磁盘可用性。无返回结果、无错误、无图片的空采集仍沿用原先的丢弃规则。旧版已经丢弃的请求和图片无法通过此次更新恢复。

覆盖当前 OpenAI Images / Grok Images 留档路径。Responses/Gemini 等其他协议的图片输出不会因此自动全量纳入。不得把该页面当成完整财务账本或承诺每个收费请求必定有可下载图片。

## 验证

在 `backend` 下，Windows 本地环境需覆盖既有 Linux 构建环境变量：

```powershell
$env:GOTOOLCHAIN='auto'
$env:GOOS='windows'
go test -tags unit ./internal/service ./internal/repository ./internal/handler ./internal/server/routes ./internal/server/middleware -run 'TestImageArchive|TestImageHistory|TestAPIKeyAuthBillingInfo|TestIsAsyncImageTaskRead|TestGatewayRoutesKeyBilling' -count=1
```

涵盖：失败留档、结果已生成但传输中断、规范账单 ID、各结算状态、无可靠账单时的未知状态、跨用户/跨 Key/过期记录/参考图访问拦截、匿名访问、API 身份验证及耗尽额度时的只读访问、真实网关路由注册和既有管理员留档回归。

在 `frontend` 下：

```text
pnpm exec vitest run src/views/user/__tests__/ImageHistoryView.spec.ts src/components/admin/__tests__/ImageArchiveAsset.spec.ts src/views/admin/__tests__/ImageArchiveView.spec.ts src/i18n/__tests__/localeKeyCompleteness.spec.ts
pnpm run build
```

客户组件测试覆盖最小展示、用户文件加载器、预览图下载、分页取消、未知账单状态及泛化错误；原管理员缩放/双指/空白关闭行为继续回归。

## 发布验收

本分支不需要新增数据库迁移。部署仍应验证当前数据库已有留档及持久结算事件表，采用候选实例验证、备份与最小服务范围发布。上线后用测试用户检查自己的历史结果、另一用户和另一 Key 的 404、预览和下载、零余额读取及管理员留档；收费生图实测单独按授权额度执行。本地测试通过不等于已部署或已完成生产争议账单核查。
