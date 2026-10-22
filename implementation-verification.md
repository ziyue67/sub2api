# 调度延迟分位数：实现与验证记录

日期：2026-10-21。工作树 `/data/development/projects/sub2api-scheduler-latency-quantile`，分支 `feat/scheduler-latency-quantile`，基线 `974a47f2ed722798566727a342df1eab3140dc94`。

**状态：实现已落地，前端回归与直接 SQL 固定样本核验通过；Go service/API/repository 测试及前端类型检查未完成。不能将本提交描述为完整验证通过或可发布。提交仅供 Hermes 独立审阅；没有 push、创建 PR、生产访问、付费请求、合并、部署或发版。**

## 实现与兼容性

- 配置 `latency_percentile` 接受 50/70/90/95，缺字段默认 90；`latency_measurement` 接受 `legacy_forward`（默认）和 `attributed_v1`。非法值拒绝保存。旧客户端省略新增字段时保留已保存值，而非重置已选择的分位数/口径/恢复比例。
- 用户指定缺省目标为 P90/15000ms。本 checkout 的原始 `DefaultPrioritySchedulingConfig()` 实为 3000ms，本提交将缺省值改为 15000ms；已持久化的显式 `target_ttft_ms`（包括 3000）继续原样读取，不修改线上配置。保留原窗口缺省 60min、原 `min_samples=5`，不自动改变已保存的 180min 窗口。
- 一次聚合并列计算 P50/P70/P90/P95，新增 `selected_ttft_ms` 用于评分和连续延迟因子。`p90_ttft_ms` 始终是真正的 P90，不将 P70 等写进 P90 字段。分位数属于明确标注的当前测量总体；legacy 的采样规则保留。
- `attributed_v1` 要求唯一 timing trace、完整非截断成功记录、`terminal=completed`、完整入站 body、无取消/客户端断开/下游错误。最后一条记录必须是最终账号的物理 HTTP attempt，且状态 200、无 attempt error、terminal completed。父 egress 不双计；最后另有未采集的 egress 也保持未知，不能回选更早成功的 HTTP attempt。
- 归因值为物理 attempt 的 `first_semantic-start_ms`，排除该 attempt 前的客户端上传/调度及前序账号尝试；不使用请求级 first_visible 或累计 downstream_write_ms。若 first_semantic 之前已经有应用 write，无法分离背压则整条归因样本保持 unknown。非数字/负起点/倒序/非正耗时同样不采样。HTTP attempt 自身的连接、请求写出和上游等待仍包含在内，不能将其称为纯模型计算耗时。
- 缺归因不回退旧 first_token_ms。`selected_ttft_ms=null`，样本数为 0，或与有效样本并列 `latency_unknown_samples`；评分保持中性 unknown/insufficient，UI 不显示成 0ms 快账号。原 `p90_ttft_ms` 无样本时的 0 占位为了兼容继续保留，必须结合 `samples` 解释。
- `experience_p90_ms` / `experience_samples` 单独展示请求级服务端 first_visible，包含入口/前序尝试的影响，且可能包含非成功终态；它是可观察体验样本，不用于账号延迟评分、不代表客户端收包或完整成功。
- 惩罚截止改用 `latest_bad_latency_at + latency_penalty_minutes`，只由最近超目标有效样本续期；新快速样本或缓存刷新不续期。缺坏样本时间不虚构恢复。保留过期后的延迟权重保护、质量通过率、最新确定质量通过、实时错误、亏损和容量门槛。
- `recovery_exploration_percent` 是实际生效的恢复 lane 概率上限（0–10，默认 0），仍受现有总 10% 探索 lane 和比较域约束，不保证全局流量份额、完成时限或全池慢时探索。原有“质量已通过但延迟/利润样本不足”的补样本 lane 保留；未知质量不能进入探索 lane。
- 缓存 key 包含配置内容摘要，分位数、口径、target 等变化隔离旧证据；快照携带 config revision、窗口、min_samples、真实 query cutoff，以及 At/EvaluatedAt/HistoryObservedAt。刷新不伪造新的负载观测。
- UI 中英双语提供分位数/口径/恢复比例控件，展示选定值、真实 P90、样本/unknown 数、低样本提示和独立体验 P90，说明权重不是全局概率、Flush/服务端输出不代表客户端收到。非 custom 权重继续显示 preset。
- 不需要 schema/迁移。保留三份接手时的新 quantile 测试；只格式化/增强，未删除、跳过或削弱断言。现存旧目标/恢复测试显式选择 3000ms 或启用 10% 恢复以继续覆盖原边界。新增 SQL 异常归因及 API 省略/枚举验证测试。

## 实际运行结果

### 前端定向回归

```bash
NODE_OPTIONS=--max-old-space-size=512 pnpm --dir frontend exec vitest run \
  src/views/admin/__tests__/PrioritySchedulingQuantile.spec.ts \
  src/views/admin/__tests__/PrioritySchedulingView.spec.ts \
  src/i18n/__tests__/localeKeyCompleteness.spec.ts
```

退出 0，最终输出（本地 `.scheduler-frontend-test.log`）：

```text
✓ src/views/admin/__tests__/PrioritySchedulingQuantile.spec.ts (3 tests) 353ms
✓ src/views/admin/__tests__/PrioritySchedulingView.spec.ts (14 tests) 431ms
✓ src/i18n/__tests__/localeKeyCompleteness.spec.ts (3 tests) 68ms

Test Files  3 passed (3)
     Tests  20 passed (20)
Start at  17:44:13
Duration  12.60s
```

仅有 Browserslist 数据版本提示，无测试 skip。

定向 ESLint：

```bash
cd frontend
NODE_OPTIONS=--max-old-space-size=512 pnpm exec eslint \
  src/views/admin/PrioritySchedulingView.vue src/api/admin/priorityScheduling.ts \
  src/i18n/locales/en/priorityScheduling.ts src/i18n/locales/zh/priorityScheduling.ts \
  src/views/admin/__tests__/PrioritySchedulingView.spec.ts \
  src/views/admin/__tests__/PrioritySchedulingQuantile.spec.ts
```

退出 0，无诊断。没有运行全套构建。

### PostgreSQL：直接执行源码 SELECT，非 Go integration

```bash
python3 backend/scripts/verify-priority-scheduling-sql.py
```

退出 0，精确输出：

```text
PASS attributed_v1 P50: selected=10000ms P90=32000ms samples=5 unknown=24
PASS attributed_v1 P70: selected=18000ms P90=32000ms samples=5 unknown=24
PASS attributed_v1 P90: selected=32000ms P90=32000ms samples=5 unknown=24
PASS attributed_v1 P95: selected=36000ms P90=32000ms samples=5 unknown=24
PASS legacy_forward P50: selected=99000ms P90=99000ms samples=29 unknown=0
PASS legacy_forward P70: selected=99000ms P90=99000ms samples=29 unknown=0
PASS legacy_forward P90: selected=99000ms P90=99000ms samples=29 unknown=0
PASS legacy_forward P95: selected=99000ms P90=99000ms samples=29 unknown=0
PASS: 8 SQL SELECT cases; 24 invalid/missing/ambiguous attribution fixtures remain unknown; experience P90=410000ms
```

脚本提取实际仓储 SQL，使用 VALUES CTE 固定用量/trace，四档各跑 attributed/legacy，共 8 个 SELECT。坏样本包括缺 trace、重复 trace、版本不符、失败/incomplete/取消/断开/下游错误/未完整上传、父子 attempt、错误账号、后续失败 attempt、后续未采集父 attempt、非法 JSON 数字和下游早写。体验样本单独断言。使用本地现成 `postgres:18.1-alpine3.23`，`--pull=never --network none --read-only --memory=256m --cpus=1 --tmpfs /tmp`，不发布端口、不读取 DSN；容器内 initdb 为一次性 tmpfs，执行的应用 SQL 只有 SELECT，没有迁移或应用数据库写入。

**此结果未覆盖 Go 扫描/仓储创建、真实表索引/生产规模计划或完整质量轮次 JOIN，不代替 `go test -tags=integration`。**

### Go：内存阻塞，不能记通过

先确认无活跃 Go test/compile writer。接手早期错误地启动过两个 Go 定向命令并行编译，进程退出且日志为空；按父助手反馈，历史 kernel journal 指向 Go compile OOM，不能解释为空日志测试通过。

收到资源限制后，核实环境继承：

```text
GOMEMLIMIT=700MiB
GOGC=20
GOMAXPROCS=1
GOFLAGS=-p=1
GOCACHE=/data/development/cache/go-build
```

只启动一个命令：

```bash
cd backend
go test -tags=unit ./internal/service -run '^TestPriority' -count=1 -v
```

`.scheduler-service-test.log` 为空。单个 compile RSS 依次观察到 717252、913056、1203964、1562028、1782124、2113344 KiB，逼近已知 OOM 区间；主动 SIGTERM compiler 和所属 Go 命令，工具最终返回 **exit 143 / `Terminated`**。此后未再启动 Go 编译，未加 `-c1` 等无依据选项。

因此当前最终代码的 service/API/repository 定向回归均未通过运行验收。新 Go 测试和 SQL Scan 的编译正确性仍待足够内存环境确认；gofmt 解析通过不是类型检查。

后续独立验证须串行，每条完成后再运行下一条，保留上述环境且先解决编译内存问题：

```bash
cd backend
go test -tags=unit ./internal/service -run '^TestPriority' -count=1 -v
go test -tags=unit ./internal/handler/admin -run '^TestPriority' -count=1 -v
go test -tags=integration ./internal/repository -run '^TestPriority' -count=1 -v
```

Go SQL integration 需专用 testcontainers；本次未达到执行测试阶段。没有将 skip、空输出或进程中断视为成功。

### 类型检查：两次 512MiB 堆 OOM，未通过

`NODE_OPTIONS=--max-old-space-size=512 pnpm exec vue-tsc --noEmit` 退出 1，日志：

```text
FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory
ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL Command was killed with SIGABRT (Aborted): vue-tsc --noEmit
```

随后用临时 tsconfig 缩到此页面/API/两份 locale，仍沿真实公共依赖导入，`vue-tsc --noEmit -p .scheduler-tsconfig.json` 同样退出 1 / SIGABRT / heap OOM。没有提高上限重试、未用 mock 类型替代真实依赖；临时 tsconfig 已移除。没有类型通过证据。

`git diff --check` 退出 0；修改的 Go 文件经 gofmt。未执行全仓测试/完整构建/全量 lint、race、截图或真实模型调用。

## 首 PR 以外的明确后续范围

未知质量预算探索、逐账号探索并发/成本预算、waiting-age 防饿死、新高风险紧急可用性策略、全池慢的尝试/时间预算、容量因子调整、30min 快窗口/滞回状态机、严格样本门槛、可见/semantic/WS/协议和负载分桶、新终态关联/失败计时采样、跨实例持久状态和灰度影子评估均未实现。没有相应假开关，也没有承诺新增 fail-closed；原有风险候选的末位容量回退语义仍存在。

尤其本 PR 的恢复比例只控制现有选择比较域内的恢复 lane 概率，不能等同提案中的跨实例 token/成本预算、未知质量 1% lane 或完整受限恢复状态机。旧 legacy 口径自身仍可能含 Forward 之前/重试口径混合；需要管理员显式选择 attributed_v1 才使用保守归因采样。

发布或 production 目标 PR 仍需 Hermes 独立审阅、补齐 Go/类型检查缺口。这里只交付本地提交，不执行远端操作。

## Agent 披露

Agent：Codex；参与：方案阅读、实现、测试增强、离线 SQL/前端验证和本文。模型名称：GPT-6（会话系统标识；完整后端 model ID 未提供，未知）；推理强度：未知（会话未暴露配置值）。无子 Agent。接手前其他历史会话的精确模型配置未知，未猜测。
