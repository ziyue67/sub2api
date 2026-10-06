# 优先调度审查与优化

基线：owner fork `production`，`ab7cb27ceed6390441277784cc5560694d95fd73`（2.9.10）。源码在独立 worktree 修改；线上诊断时主站为 2.9.9，两版本的本次调度代码相同。没有生产配置、数据库或部署变更。

## 已修复

| 范围 | 问题与触发条件 | 修正 |
| --- | --- | --- |
| 历史缓存 | 整个候选 ID 列表参与 key；一个账号增减就丢失其他账号的命中 | 按模型、分组和统计窗口隔离，复用各账号的已有历史；新增账号局部未知 |
| 刷新失败 | 一次失败清空统计，已知质量/亏损风险也被抹去 | 保留最长 2 分钟的已知统计；30 秒刷新，失败 5 秒后可重试；过期后明确未知 |
| 资源边界 | 32 个候选集合容易占满缓存，且请求间缺乏复用 | 最多 32 个 scope、每 scope 2000 个最近账号、2 个异步查询；LRU 淘汰非刷新项，缺账号的补查至少相隔 1 秒 |
| 管理快照 | 后台完成后页面仍读取第一次冷快照，需要新的业务选号才更新 | GET 异步刷新历史并用最后一次选号输入重算；保留选号时间、并发和配置，另列重算/历史读取时间；前端最多 3 次后续读取 |
| 数据状态 | 数据为空、加载失败和未加载统一显示“尚未就绪” | 区分 ready/stale/partial/loading/error/unavailable/limited，行级状态保留已知历史；只暴露脱敏错误类别 |
| 质量 SQL | 按计划主模型过滤，多模型计划漏算其他模型，同轮总数还会与单模型并发数不符 | 按结果快照的实际模型汇总，旧记录回退计划模型；轮次包含 plan ID |
| 旧质量记录 | 非整数 parallel_count 强制 cast 失败，拖垮整次用量、延迟及质量统计；JSON null quality 也被纳入 | 验证完整轮次的并发数 1–8，无效轮次排除；quality 必须为对象 |
| 新号探索 | 要求延迟和利润同时不足；其中一项达标后，另一项可能永久缺样本；API-key 新号无探索 | 在同风险/容量/优先级约束下，任一指标缺样本即可参与原有 10% 探索；支持两类账号，已知风险及繁忙账号仍不参与 |
| 并发输入 | nil loadInfo 被直接解引用 | 缺失数据标记未知，保留容量兜底 |
| 黏性分流 | 只看实时错误和空闲度，已知质量差/亏损的替代账号也可能触发逃逸 | 查已有历史，排除已知风险及已知耗尽 RPM 的替代；不额外触发 SQL；可选均衡依赖读取限制 150ms |
| 热路径 | 先计算旧调度成本、重置、权重和 Top-K，再全部被优先调度覆盖 | 优先调度生效时直接评分及全候选抽样，跳过被覆盖的旧计算；排序前缓存容量档 |
| 利润风险 | 浮点乘减在盈亏平衡附近可能得到极小负值，误入亏损风险档 | 明确成本舍入，对相对误差 1e-12 内的盈亏平衡归零；真实微小亏损仍保留 |
| 数值/所有权 | 非有限配额可能污染抽样权重；快照依赖可变账号/负载指针 | 非有限配额中性处理；快照持有精简的独立评分输入，不保存凭据和会话内容 |

## 保留的算法约束与局限

- 继续按已知质量/错误/亏损风险、预计并发/RPM 档、体验档、账号优先级排序，同档以指数竞赛加权无放回抽样，保留 overflow。评分不等于固定流量百分比。
- 配额、分组、模型、协议、利润门槛、最终 DB 回读和 slot 获取仍由原有执行路径校验。BPS 偏好、compact、订阅优先及硬归属没有放宽。
- 没有有效质量检测计划/结果的账号仍显示质量未知；正常业务流量只积累延迟和利润样本，不会自动产生质量判定。实时错误率仍沿用账号级 EWMA，不改变既有跨模型的账号健康信号。
- 历史仍是实例内缓存，不新增 Redis 写入或分布式领导任务。两实例首次加载时间可能不同；数据库历史口径相同。
- 30 秒内的质量计划/统计变化会随刷新进入缓存；失败时最多使用 2 分钟历史。它是软评分信号，不替代实时账号禁用与配额门禁。
- 管理页重算不代表当时实际选路，也不会重新获取历史候选的实时并发。配置/成本仍为最后一次选号观察值，新的业务选号才更新这些输入。
- 未新增数据库索引：诊断中的原聚合查询约 6ms，没有慢查询证据。SQL 改动针对统计正确性；不以无证据的索引或延长超时掩盖缓存问题。

## 验证

执行环境为本机 macOS arm64、Go 1.27.0、pnpm 9.15.9；数据库测试使用本机 Docker Desktop 的 PostgreSQL 18.1 / Redis 8.4 testcontainers。

- 后端目录 `GOMAXPROCS=4 go test -tags=unit ./internal/service -run '^TestPriority' -count=1`：首轮因现有精确浮点利润断言失败；已修正利润舍入与盈亏平衡容差。
- 后端目录 `GOMAXPROCS=3 go test -tags=unit ./internal/service ./internal/handler/admin -run 'Test(Priority|OpenAIAccountScheduler|OpenAI.*Scheduler|Scheduler)' -count=1`：通过。
- 后端目录 `GOMAXPROCS=2 go test -tags=integration ./internal/repository -run '^TestPriority' -count=1 -v`：3 个测试通过，未跳过。
- 根目录 `npx --yes pnpm@9 --dir frontend run lint:check`、`npx --yes pnpm@9 --dir frontend run typecheck`、`npx --yes pnpm@9 --dir frontend run build`：通过；构建有既有 chunk 大小及 Browserslist 数据陈旧提示。
- 根目录 `npx --yes pnpm@9 --dir frontend exec vitest run src/views/admin/__tests__/PrioritySchedulingView.spec.ts src/views/admin/__tests__/AccountsView.schedulerScore.spec.ts src/views/admin/__tests__/AccountsView.priorityColumn.spec.ts src/components/account/__tests__/AccountPriorityCell.spec.ts src/i18n/__tests__/localeKeyCompleteness.spec.ts`：27 个测试通过。
- 本地实际 Vue 组件截图及“刷新评分”交互完成，无浏览器 page error；布局、鉴权、API 使用 mock，仅含虚构账号。见 `docs/screenshots/priority-scheduling/manifest.json`。
- 后端目录 `GOMAXPROCS=2 /tmp/sub2api-priority-lint/golangci-lint-2.13.0-darwin-arm64/golangci-lint run --timeout=30m ./internal/service/... ./internal/repository/... ./internal/handler/admin/...`：通过，0 issues。首次 10 分钟时限超时，未计为通过。
- 后端目录本机 `CGO_ENABLED=0 GOMAXPROCS=2 go build -tags embed -trimpath -ldflags='-X main.Version=2.9.10-priority-audit -X main.Commit=9b2e8fd55f95f7fcce21b3c0fb09d32ac81d0ba7' -o /tmp/sub2api-priority-audit-local ./cmd/server`：通过，版本/提交回读正确；仅对应首个修复提交，未部署。
- 后端目录 `CGO_ENABLED=0 GOMAXPROCS=2 go test -tags=unit ./internal/service ./internal/repository ./internal/handler/admin -count=1`：三个包均通过（service 233.411s、repository 12.060s、admin 4.448s，不含编译时间）。
- 后端目录 `GOMAXPROCS=2 GOMEMLIMIT=1500MiB go test -race -tags=unit ./internal/service -run '^TestPriority' -count=1`：通过。
- 后端目录 `CGO_ENABLED=0 GOMAXPROCS=2 go test -tags=unit ./internal/service -run '^$' -bench '^BenchmarkPriorityHistoryWarm$' -benchtime=1s -count=1`：通过。Apple M1 Pro / darwin arm64，100 个候选的单线程缓存命中为 11730 ns/op、11976 B/op、10 allocs/op；仅为本机微基准，没有前后对照，不代表生产吞吐。
- 最后补充的“查询名额等待/补查节流期间继续有限次数读取”分支已跑上述 27 个前端用例及最终构建；相关 Vue/测试文件 ESLint 通过。
- 未进行真实模型请求或生产部署；未声明吞吐/延迟收益百分比。
