# Admin usage「近24小时」验证记录

基线：`2af964fd589886ab9ba2d7e93bf6ac07c17aa8e1`（工作开始时的 `origin/production`）。分支：`fix/usage-rolling-24h`。本地 Node `v24.21.0`、pnpm `9.15.9`；CI 版本以仓库 workflow 为准。

## 链路与约定

已阅读根目录 `CONTRIBUTING.md`、PR 模板、Makefile 和 `.github/workflows/backend-ci.yml`；checkout 内及父目录未找到适用的 `AGENTS.md`。

原链路：`DateRangePicker.last24Hours` 和 admin `UsageView` 默认值把时间截成 `YYYY-MM-DD` → `/admin/usage`、`/admin/usage/stats` 将结束日期推进到次日零点 → repository 的 `created_at >= start AND created_at < end` 查询两个自然日。模型、趋势、分组、排行与拆分使用独立的 dashboard `parseTimeRange`，也需要支持精确时间。GET API 拦截器自动附带浏览器 IANA 时区。

修复后：admin usage 为 picker 开启 `rolling24Hours`，生成含毫秒的 ISO UTC 时间；在应用/筛选/刷新时统一捕获 `[now-24h, now)`，列表、卡片、模型、snapshot、错误及连接诊断共用边界。分页、排序、导出沿用已应用的窗口。其它 picker 使用方维持原有接口；其它预设和手工日期依旧是本地自然日。滚动、精确或混合范围不挂载清理对话框；点击清理会提示先选自然日，并明确整日删除语义。只有起止值均为 YYYY-MM-DD 时才允许打开清理，直接传递原日期，其清理 API 没有修改。

后端共同解析 YYYY-MM-DD / 带时区 RFC3339：仅日期型结束值使用 `AddDate(0,0,1)`，时间戳结束值原样作为排除边界。显式无效/倒置 dashboard 范围返回 400，不再静默切换默认区间。缓存键使用 RFC3339Nano。趋势仅在服务器显式配置为 UTC 且边界完全对齐 UTC 小时/日桶时使用预聚合；预聚合查询参数统一编码为 UTC，避免带偏移的时间戳被直接转成错误日期。其它时区或部分桶直接查原始日志，防止漏算、多算和日期映射错误。

统计卡口径不变：总消费是 `total_actual_cost = SUM(actual_cost)`，标准费用是 `total_cost = SUM(total_cost)`，账户成本是 `SUM(COALESCE(account_stats_cost,total_cost) * COALESCE(account_rate_multiplier,1))`。没有修改账本、结算、支付或迁移。

## 实际 RED → GREEN

保存的日志仅移除了行尾空白以通过 diff 检查，测试输出、计数和退出码没有修改。

先写回归测试并实际运行以下 RED 命令，确认断言失败后才修改实现。完整原始输出见 [red.log](red.log)，退出码 **1**：**12 failed / 22 passed**（2 个文件）。关键差异：跨午夜期望 `2026-09-29T15:01:02.123Z` 至 `2026-09-30T15:01:02.123Z`，实际发送 `2026-09-30` 至 `2026-10-01`；picker 同样丢失时间，并将手工两日范围误标为 last24Hours。

```bash
TZ=Asia/Seoul pnpm --dir frontend exec vitest run \
  src/components/common/__tests__/DateRangePicker.rolling24h.spec.ts \
  src/views/admin/__tests__/UsageView.spec.ts --maxWorkers=1 --minWorkers=1
```

实现后先跑同一范围，剩余 2 个失败；修正首次渲染预设识别，并让初始页面测试推进其既有的 100ms 图表延迟。旧图表测试的时钟期望也改为断言推进计时器前捕获的已应用窗口。随后以下定向 GREEN 实际退出 **0**，**50 passed**（7 个文件），输出见 [green.log](green.log)：

```bash
TZ=Asia/Seoul pnpm --dir frontend exec vitest run \
  src/components/common/__tests__/DateRangePicker.rolling24h.spec.ts \
  src/components/common/__tests__/DateRangePicker.spec.ts \
  src/components/common/__tests__/DateRangePicker.dismiss.spec.ts \
  src/components/common/__tests__/DateRangePicker.midnight.spec.ts \
  src/views/admin/__tests__/UsageView.spec.ts \
  src/components/admin/usage/__tests__/UsageStatsCards.spec.ts \
  src/components/admin/usage/__tests__/UsageCleanupDialog.spec.ts \
  --maxWorkers=1 --minWorkers=1
```

另外实际运行纽约时区（覆盖 DST 本地语义），退出 **0**，**34 passed**（2 个文件），见 [green-new-york.log](green-new-york.log)：

```bash
TZ=America/New_York pnpm --dir frontend exec vitest run \
  src/components/common/__tests__/DateRangePicker.rolling24h.spec.ts \
  src/views/admin/__tests__/UsageView.spec.ts --maxWorkers=1 --minWorkers=1
```

覆盖午夜前后、毫秒、春/秋 DST 的严格 86,400,000ms、选择到应用期间跨午夜、初始/刷新/重置请求一致性、分页/导出窗口稳定、错误/连接精确边界、全部其它预设和手工范围的自然日语义、清理日期兼容及消费/标准费用/账户成本区分。新回归文件已加入 `FRONTEND_CRITICAL_VITEST`，供远端 CI 后续执行。

## 其它实际验证和限制

- `pnpm --dir frontend install --frozen-lockfile`：退出 0；没有改锁文件。
- `pnpm --dir frontend exec eslint src/components/common/DateRangePicker.vue src/views/admin/UsageView.vue src/components/common/__tests__/DateRangePicker.rolling24h.spec.ts src/views/admin/__tests__/UsageView.spec.ts src/components/admin/usage/__tests__/UsageStatsCards.spec.ts`：退出 0，无输出。
- `pnpm --dir frontend run typecheck`：**失败，退出 134**；Node 默认约 2 GB 堆内存耗尽，未完成类型验证。原始输出见 [typecheck.log](typecheck.log)，交远端 CI 继续验证，不计为通过。
- 对本次全部 Go 改动执行 `gofmt -w`，随后 `gofmt -l` 无输出（退出 0）；这是格式/语法检查，**不是编译或 Go 测试通过**。
- `git diff --check`：退出 0；提交前人工核对 diff 及范围。
- 本机**没有运行** Go 构建、`go test`、`go vet` 或 golangci-lint；没有连接生产、读取账务数据或凭据、部署或发布。全部测试输入为虚构数据。

Go handler 测试检查实际传给 repository 的上下界、显式时区不被用户时区覆写、日期型范围在 DST 的 23/25h 自然日兼容、错误边界、统计和 dashboard 的毫秒缓存隔离。SQL mock 测试检查部分桶直接查日志，且不把被忽略的预聚合错误误当成通过。集成测试使用现有隔离数据库事务，插入 start−1µs、start、end−1µs、end、end+1µs，断言仅中间两个记录进入列表、统计和趋势，同时区分三种费用。

以下为交给远端 CI 的命令，**不是本地已运行结果**；现有 CI 的 `make test-unit` / `make test-integration` 会包含这些测试：

```bash
cd backend
go test -tags=unit ./internal/handler/admin -run 'Test(AdminUsageTimeRange|UsageStatsCacheKeyPreservesTimePrecision|AdminDashboardRolling24HoursQueriesAndCachePrecision|AdminDashboardRejectsInvalidTimeRange)' -count=1
go test -tags=unit ./internal/repository -run '^TestUsageTrendRolling24HoursPreservesQueryBounds$' -count=1
go test -tags=integration ./internal/repository -run '^TestUsageLogRolling24HoursHalfOpenBoundaries$' -count=1
make test-unit
make test-integration
```

Agent：`codex-cli`，参与调查、测试、实现、验证和 PR。完整实际模型 ID 与推理强度均为**未知**：本次会话未暴露可核实的实际值，模型/推理强度专用环境变量也未提供；没有通过读取凭据或会话日志推测它们。


## PR579 独立复审修复（2026-10-27）

接续基线为 `cfb798c1587a60b297331303d84e89c76ab1b11c`，沿用同一分支和 PR579。以下为本轮新增证据；上面的原始测试、失败记录保留，不代表本轮重新执行。

### 阻断修复和实际语义

- **P1**：移除向清理对话框传递 `calendarDate(timestamp)` 的路径。精确/滚动/混合范围时不挂载对话框，打开入口提示“滚动或精确时间范围不可直接清理。请先选择自然日；清理将删除所选日期的整日记录。”中英文均有对应文案。明确选择自然日后保持原有确认流程及 YYYY-MM-DD 清理 payload。
- **P2**：真实 writer 在 `upsertHourlyAggregates` 中用 `date_trunc('hour', created_at AT TIME ZONE timezone.Name()) AT TIME ZONE timezone.Name()`；`upsertDailyAggregates` 再将 bucket_start 转服务器本地日期。不能把 UTC 对齐当成可读本地桶的证明。为避免扩大本次范围，除显式配置 UTC 且区间对齐的情况外，一律直接查询原始日志；UTC 预聚合参数用 `.UTC()` 编码以保证日桶参数的日期含义。未改变聚合生成器、清理后端、账本、费用定义或数据库结构。
- 服务器 Asia/Shanghai 的日桶 2026-09-30 对应 `[2026-09-29T16:00Z, 2026-09-30T16:00Z)`，不能用于请求 `[2026-09-30T00:00Z, 2026-10-01T00:00Z)`。小时桶在 +05:30/+05:45 时偏离 UTC 整点；DST 回拨时本地小时也不能直接视为 UTC 固定时长桶。
- UTC 配置下仍保留原 UTC 完整小时/日桶预聚合断言，增加负偏移时间戳对应 UTC 午夜的 SQL 参数断言和数据库回归；没有删除、skip 或减弱现有业务断言。
- **既有 CI 失败**：旧 head 的两套 CI（[push](https://github.com/ranxi2001/sub2api/actions/runs/40513488540)、[PR](https://github.com/ranxi2001/sub2api/actions/runs/40513539932)）均在 `TestParseTimeRange` 失败。测试更换 Request 却复用带 queryCache 的 Gin context，使第二次请求沿用第一次的参数；现为第二次请求建立新 context，原 require.Error 断言原样保留。

### 先 RED 后 GREEN 的顺序

1. `15cb9921763637b07bb8194156fbb449f0e77686`：仅新增交互、SQL mock、真实 PostgreSQL 桶回归及远端定向步骤，推送同一 PR；实现未改变。本地前端先实际得到 **4 failed / 20 passed，退出 1**。前 3 项证明滚动/精确/混合范围错误地显示清理提交按钮；第 4 项证明重新选择自然日后仍可提交旧的两日范围。
2. [首轮 PR RED](https://github.com/ranxi2001/sub2api/actions/runs/40516614544/job/122232013147)：真实 PostgreSQL 集成 **6 个子用例失败**（上海跨 UTC 午夜、半小时/45 分钟偏移、纽约春/秋 UTC 日、Lord Howe 半小时 DST），SQL mock **7 个子用例失败**。日期标签、每桶记录数和是否直接回源的断言均真实失败。
3. `48dc8aa49460620d544e655851ee7b31947adab4`：追加 UTC 服务器上负偏移时间戳转日期的回归，仍未改变实现；UTC 负偏移真实桶用例也实际失败，第二轮真实桶共 7 个子用例失败；远端 [push RED](https://github.com/ranxi2001/sub2api/actions/runs/40517042043)、[PR RED](https://github.com/ranxi2001/sub2api/actions/runs/40517051256)。
4. RED 后实现最小修复。本地前端 **57 passed / 8 files，退出 0**（首尔），**38 passed / 2 files，退出 0**（纽约），改动文件 ESLint 退出 0。最终 Go GREEN 与全量检查以 PR 当前 head 的两套远端 CI、PR 正文及下面证据目录中的最终回读记录为准，不将本地静态检查当成 Go 执行通过。

真实数据库回归由隔离 PostgreSQL testcontainer + 逐例回滚事务执行，调用实际小时/日 writer，确保聚合表非空，再走无筛选读取路径。逐项断言原始日志的日期标签、每桶计数、半开边界、standard/actual/account 三种费用；覆盖纽约 23/25 小时自然日、重复小时、Lord Howe 偏移变化，以及 UTC 服务器下负偏移请求日期映射。SQL mock 额外记录所有实际 query，禁止先错误读取聚合后吞错回源而伪装通过。

### 本轮命令和原始证据

全部原始输出与最终回读保存在 `/data/development/artifacts/pr579-review-fixes-20261027/`，未改写输出。该目录不在 Git 版本控制中；GitHub 可复核日志见上述 Actions 链接及 PR 最新正文。

| 文件 | 实际内容 |
| --- | --- |
| `frontend-red.log` | 实现前 `TZ=Asia/Seoul` 定向 UsageView，4 failed / 20 passed |
| `frontend-green-seoul.log` | 下列 8 个定向文件，57 passed |
| `frontend-green-new-york.log` | picker rolling24h + UsageView，38 passed |
| `eslint.log` | 以下 4 个改动文件 lint 输出，退出 0，无输出 |
| `ci-before-failed.log` / `ci-before-push-failed.log` | cfb798c 两套 CI 的真实失败原始日志 |
| `go-red-initial-job.log` / `go-red-offset-job.log` | 两轮真实桶和 mock 失败日志（GitHub job logs 原始响应） |
| `pr-before.json` / `runs-before.json` | 接手时远端 PR 和 CI 状态 |

```bash
# RED（实现前），exit 1
TZ=Asia/Seoul pnpm --dir frontend exec vitest run \
  src/views/admin/__tests__/UsageView.spec.ts --maxWorkers=1 --minWorkers=1

# GREEN（实现后），exit 0，57 passed
TZ=Asia/Seoul pnpm --dir frontend exec vitest run \
  src/components/common/__tests__/DateRangePicker.rolling24h.spec.ts \
  src/components/common/__tests__/DateRangePicker.spec.ts \
  src/components/common/__tests__/DateRangePicker.dismiss.spec.ts \
  src/components/common/__tests__/DateRangePicker.midnight.spec.ts \
  src/views/admin/__tests__/UsageView.spec.ts \
  src/components/admin/usage/__tests__/UsageStatsCards.spec.ts \
  src/components/admin/usage/__tests__/UsageCleanupDialog.spec.ts \
  src/i18n/__tests__/localeKeyCompleteness.spec.ts --maxWorkers=1 --minWorkers=1

# 第二时区 GREEN，exit 0，38 passed
TZ=America/New_York pnpm --dir frontend exec vitest run \
  src/components/common/__tests__/DateRangePicker.rolling24h.spec.ts \
  src/views/admin/__tests__/UsageView.spec.ts --maxWorkers=1 --minWorkers=1

# exit 0
pnpm --dir frontend exec eslint src/views/admin/UsageView.vue \
  src/views/admin/__tests__/UsageView.spec.ts \
  src/i18n/locales/en/admin/resources.ts src/i18n/locales/zh/admin/resources.ts

# 只在远端 CI 执行，未在本机执行
cd backend
go test -tags=integration ./internal/repository -run '^TestUsageTrendAggregateTimezone' -count=1 -v
make test-unit
make test-integration
```

本轮本机仅运行定向前端测试、定向 ESLint、gofmt 及 diff 检查；未再次运行全量 typecheck，未执行本机 Go 动态测试/构建/vet/lint，未读取凭据、发送生产请求、合并、发布或部署。UI 无浏览器前后截图，交互证据为真实 picker 与清理 dialog 的 DOM 操作和模拟 API payload 断言。非 UTC 趋势回源可能增加数据库读开销，未做性能提升声明。

本轮继续由 codex-cli 完成调查、回归、实现、CI 核验、文档与 PR 更新，未使用子 Agent。模型家族由运行指令标识为 GPT-6；可核实的完整实际模型 ID 和推理强度均未提供，因此均记为未知，未读取凭据或会话日志推断。
