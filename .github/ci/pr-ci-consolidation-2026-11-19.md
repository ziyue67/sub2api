# PR CI 精简清单（2026-11-19）

基线：PR #172 已合并的 main `44339c2ace4eed7d44f48a885a082b895676a759`。本次只调整 CI 与前端测试入口，没有修改或删除应用代码、测试源码及业务断言。

允许响应 main PR 的工作流入口：**60 → 42（减少 30%）**；18 个自动入口共对应 **29 个独立 job 定义**。这表示相关路径全部命中时的配置数量变化，各 PR 实际数量由改动路径决定。主工作流已有的条件选择和旧成功证据复用规则继续生效。

根 `Makefile` 的前端清单变化只选择主前端门禁。Go runner 在 `backend/` 运行并读取 `backend/Makefile`，golangci-lint 也在后端运行，因此从这三个 job 的依赖范围移除无关的根 `Makefile`；`backend/**`（含后端 Makefile）、各自命令定义及实际脚本依赖继续保留。不会因为补前端规格而重跑全部 Go 测试。

## 退役重复工作流

以下文件原样迁入 [历史归档目录](../workflow-archive/README.md)，Actions 不再加载。保留普通测试源码，合并到已有主门禁：

| 原工作流 | 独立 job 数 | 日常自动覆盖 |
| --- | ---: | --- |
| `bps-wire-contract.yml` | 1 | 主 Go unit 全包，包括 basispoints、ExcelBPS wire/access/usage |
| `dashboard-account-concurrency.yml` | 2 | 主 Go unit 的 admin handler；DashboardView、NewFeatureBadge 由主前端清单执行，生产打包统一执行 |
| `quality-oauth-401.yml` | 1 | 主 Go unit + integration，覆盖 QualityOAuth401 服务和持久化回归 |
| `quality-retest-rest.yml` | 2 | 主 Go unit + integration；AccountQualityView、qualityRulePatch、SmartOpsNav、NewFeatureBadge、localeKeyCompleteness 由主前端清单执行 |

后端主门禁通过 `go list` 动态发现全部 unit/integration package；数据库套件保留真实 PostgreSQL、Redis 和 testcontainers。独立工作流的日志白名单检查已随重复入口退役，应用测试本身继续自动执行。

## 仅保留手动验收

以下工作流移除 `pull_request`，保留 `workflow_dispatch`。不再通过创建 skipped job 减少耗时，而是根本不为普通 PR 启动这些工作流。原始 job、命令、runner、依赖与断言保留，仅为原来没有手动入口的工作流增加入口，并补齐必要的 head SHA 回退。

| 工作流 | job 数 | 日常自动覆盖与手动证据 |
| --- | ---: | --- |
| `batch-upstream-whitelist.yml` | 2 | 主 Go unit/integration、BatchUpstreamWhitelist、AccountsView.bulkEdit；历史负例、前后截图及重复全量构建留作手动 |
| `issue733-priority-ui.yml` | 4 | 主 Go unit、PrioritySchedulingReading/View/Quantile；历史配置对照和浏览器截图手动 |
| `keys-usage-layout.yml` | 1 | KeysView、UserKeyRiskNotices 和生产打包；Keys 页浏览器布局验收手动 |
| `mobile-groups-search.yml` | 1 | GroupsView 各规格、DataTable、TablePageLayout 及生产打包；桌面/移动布局对照手动 |
| `model-billing-groups.yml` | 2 | 主 Go unit、AutoConfigView、modelBilling、AutoConfigHistory；合成 API 浏览器验收手动 |
| `oauth-initial-model-selection.yml` | 2 | 主 Go unit、AutoConfigView、OAuthInitialModelSelection、EditAccountModal、SmartOpsNav；历史与浏览器对照手动 |
| `quality-jev-judge.yml` | 2 | 主 Go unit/integration 的 typesafe/Jev/quality 回归；固定历史协议负例和评分编辑器浏览器手动 |
| `quality-live-regressions.yml` | 1 | 主 Go unit/integration、QualityOwnershipReview、AccountQualityView、localeKeyCompleteness；固定旧源码回放手动 |
| `quality-orphan-removal.yml` | 1 | 主 Go unit/integration、QualityOwnershipReview、AccountQualityView；固定旧源码回放手动 |
| `quality-ownership-ui.yml` | 1 | QualityOwnershipReview 的组件交互回归；离线浏览器截图手动 |
| `quality-template-complete.yml` | 1 | 主 Go unit/integration、scheduledTests、qualityRulePatch、QualityOwnershipReview、AccountQualityView；历史负例和浏览器编辑器验收手动 |
| `qwen-cache-regression.yml` | 2 | 主 Go unit 的 Qwen/StrictSessionAffinity；Qwen 设置浏览器组件验收手动 |
| `smart-ops-sidebar-preview.yml` | 1 | SmartOpsSidebarSettings、smartOpsSidebar；固定前后版本截图手动 |
| `upstream-channel-operations.yml` | 2 | 主 Go unit/integration、UpstreamChannelOperationsView、SmartOpsNav、AppSidebar；历史 attribution 负例与截图手动 |

这些手动流程保留历史证据，不保证冻结的旧代码与未来接口始终兼容。需要取证时再维护对应旧源码适配；日常 PR 直接验证当前代码。浏览器布局/像素/真实页面断言与组件 Vitest 有区别，这部分覆盖明确改为按需运行。

## 补齐主前端清单

主 [Makefile](../../Makefile) 新增 16 份规格，原有实际存在的 165 份全部保留，总数为 181。旧清单还有一条指向已删除 OpenAISessionExportDialog 规格的陈旧条目，本次一并移除；没有删除测试文件。每份实际规格在主清单中仅列一次。主前端 job 已执行完整 lint、vue-tsc 及 localeKeyCompleteness，因此随后直接运行 `pnpm --dir frontend exec vite build`，避免再次调用包含同样类型和 i18n 检查的 `build` 脚本。

主 CI 全量后端 unit/integration/lint、安全扫描、部署及发布辅助脚本、特殊标签/race、真实 Redis/PostgreSQL、V2 凭证巡检和共享重登相关自动入口保留。发布、镜像和部署工作流不在本次范围。
