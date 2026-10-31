# Claude 原生 Messages 断流后的最终 Usage 收集

## 范围与根因

基线：`5a29ef4a6555bda8ae686ef929e2b9f12990fc9d`，对应当前上游 `main`（2026-10-31 检查）。

原生 Messages 流式转发在客户端取消、Write 失败或 Flush 失败后，立即退出读取并关闭上游 Body。HTTP 上游请求同时继承客户端取消。这会使后续 `message_delta.usage` 无法到达本站，只能按已经读到的部分 Usage 结算；例如模拟上游初始 output=1、最终 output=42，断连时原生路径只返回1。单独的 API Key 透传与协议转换路径已经有不同的 drain 行为，不能按“Claude 分组”名称认定所有请求均受影响。

这是已复现的少计用量风险，不等同于已证明真实上游亏损。真实损失需要同一请求的供应商账单及最终 Usage，不能用字符数、请求时长或另一条请求的 token 数推算。

## 修复约束

1. 非流式请求保持原取消语义。
2. 等待上游响应头、错误响应和重试期间仍传播取消；成功接受流式请求后才停止传播客户端取消。
3. 断连后立即停止下游写入、flush 和 keepalive，继续合并上游原始 Usage。
4. 收到已有协议判定的最终事件后及时关闭 Body，不必等待仍不关闭的上游 EOF。断连阶段还须确实观测到 `message_delta.usage.output_tokens`（显式0也有效）；只有结束事件而缺少最终 Usage 时保留已观测值并标记不完整。
5. 保留 `gateway.stream_data_interval_timeout`；未配置时断连阶段使用180秒空闲上限。另加断连后180秒总上限，持续 ping/文本不能无限延长。
6. EOF、读取错误、SSE 错误、超时等取不到最终 Usage 时，返回已观测数值与不完整原因，写管理员服务日志；断连后的错误不再转为账号切换/重放。
7. 继续使用现有 `RecordUsage`、持久化、计费去重与恢复流程，不新增第二次扣费或账单补扣。

不改缓存 TTL、5m/1h 策略、tier、价格、分组倍率、数据库 schema、排行榜或画布。

## 代码入口

- `backend/internal/service/gateway_claude_stream_context.go`：响应接受前后的取消生命周期。
- `backend/internal/service/gateway_forward.go`：原请求和签名/budget 重试共同使用该生命周期。
- `backend/internal/service/gateway_upstream_response.go`：断连状态、有界读取、最终 Usage 合并与资源释放。

## 可重复验证

仓库根目录下进入 `backend`，使用 go.mod 指定工具链：

```sh
go test -tags unit ./internal/service -run '^(TestClaude|TestGatewayService_.*(Streaming|Stream|PartialUsage)|TestAnthropicCompat.*)' -count=1 -timeout=180s
go test -race -tags unit ./internal/service -run '^(TestClaude|TestGatewayService_.*(Streaming|Stream|PartialUsage)|TestAnthropicCompat.*|TestUsageRecovery|TestApplyUsageBilling|TestUsageBillingCommand)' -count=1 -timeout=180s
go test -tags unit ./... -count=1 -timeout=300s
go test -tags integration ./... -count=1 -timeout=300s
```

Windows 本地须设置 `GOOS=windows`、`GOARCH=amd64`，race 还需可用的 C 编译器及 `CGO_ENABLED=1`。集成测试按项目既有 harness 使用独立 Docker PostgreSQL/Redis，不得连接生产账本。Docker 不可用时 harness 可能返回成功退出但跳过测试；该情况不能标记为集成验证通过。

### 失败证据（修复前）

`TestClaudeBillingProbeNativeFinalUsage/complete`：最终42，返回42，通过。

`TestClaudeBillingProbeNativeFinalUsage/downstream_write_failure`：期望42，实际1，断言失败。此用例保留在修复分支，未降低断言。

### 回归用例

- 正常完成、Write失败、Flush失败、请求ctx取消均收集到同一最终Usage。
- 初始输入/缓存/输出Usage保留，后续累计输出更新，不做字符/token估算。
- 最终事件到达而上游不发EOF时也会释放Body。
- 缺失最终事件、上游SSE错误保留已观测Usage，并禁止断连后切换重放。
- 空闲和持续ping上游均有界释放。
- 实际本地HTTP上游：响应头之前取消仍立即终止；成功流断连后还能获得最终Usage；拿不到最终Usage时到期关闭真实连接，不重试。
- `Forward -> RecordUsage -> billing command`：相同上游Usage在断连与正常完成时生成同额费用；取消ctx不阻止结算，同请求ID/Key仍使用同一去重标识。这里的repository为本地stub，不冒充真实数据库验证。
- 既有API Key透传、兼容转换、缓存计费和计费恢复测试继续保留。

本次不做生产切换、不额外发付费推理、不回补历史账单。源码、隔离测试、真实收费请求和生产部署应分别验收。

## 2026-10-31 本地验收结果

| 检查 | 结果 |
| --- | --- |
| 修复前原用例 | 正常42通过，断连期望42实际1失败；失败日志保留 |
| 扩展unit回归 | 176个顶层测试通过，包含模拟计费链；之后补充最终Usage缺失边界 |
| 最终源码race回归 | 84个相关顶层测试通过，0失败、0 data race，28.188秒；包含上述边界及原生API Key/OAuth实际本地HTTP、透传、兼容转换、缓存分类、结算和恢复 |
| 最终源码integration-tag断连回归 | 通过，6.107秒；包括响应头前取消、OAuth/API Key收齐最终Usage、超时关闭实际连接；上游为本地模拟HTTP，不是收费供应商 |
| 全量Windows unit | 未通过；权限、目录同步、快照及其他模块出现失败，service套件超过本次300秒总时限 |
| 全量Windows integration | 未通过；另在中间测试版本发现HTTPS fixture配置错误，修正后以上定向回归通过；没有将全量命令标为绿色 |
| 真实数据库扣费/去重 | 未验收；repository harness明确输出Docker不可用并跳过 |
| 真实收费请求、生产部署、GitHub CI | 本次未执行 |

全量失败中的五个代表性用例（requestcapture权限、mihomo权限、regional落盘、凭据目录同步、模型快照）已通过Go overlay换回未修改的两个生产源码文件进行对照，同样失败。其他全量失败未逐一归因；这些测试和断言均保留，没有为本修复屏蔽或放宽。

本地证据目录为工作区 `claude-billing-20261031/`：`fix-red.txt`、`fix-final-targeted.txt`、`fix-final-race.txt`、`fix-final-integration-stream.txt`、`fix-billing-integration.txt`、`fix-unit-all.txt`、`fix-integration-all.txt`、`fix-baseline-failures.txt`。仓库提交保留可重复测试与本说明；Linux CI及真实数据库验收仍待完成。
