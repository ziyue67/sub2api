# 自动配置近期日志

在「智能运维 → 自动配置」的保存按钮下方展示近期日志。首次加载 20 条，可按类型筛选、手动刷新和加载更早的记录；保存配置成功后自动刷新日志，不覆盖未保存的配置草稿。退出登录、切换用户或切换筛选条件后，过期响应不会重新填回旧记录。

## 记录范围

- **配置已保存**：保存时的开关、初始并发、优先级、负载因子，以及可展开的分组和升档规则快照。
- **首次配置已应用**：新 OAuth 账号创建且分组绑定成功后的实际初始值；包含普通创建和 CRS 新账号同步路径。重新授权、已有账号更新和手动编辑不产生该事件。
- **并发已升级**：升级前后并发值及冷却时长。正常请求累计进度不逐条记录。
- **失败冷却**：失败导致进度归零和冷却；同一冷却期内的连续失败合并为一次记录。升档/手动调档冷却中的首次失败仍会记录。

历史记录不回填，不根据当前账号设置伪造过去操作。账号名称保存快照，账号删除后仍可查看历史。不存储 OAuth 凭据、请求正文或上游原始报错。

## 接口与存储

管理员接口：`GET /api/v1/admin/account-ops/auto-config/events`。

- `limit`：默认 20，范围 1–100。
- `kind`：空值或 `config_saved`、`initial_applied`、`concurrency_upgraded`、`failure_cooldown`。
- `before`：上一页最后一条记录 ID。按 ID 倒序游标分页，新日志写入不会导致重复翻页。
- 返回 `items` 和 `has_more`；空结果返回数组；参数错误返回 400，存储异常返回不含内部连接信息的 503。

迁移 `259_account_auto_config_events.sql` 新建事件表和两个索引，不修改已有表。旧二进制可以忽略新增表；不会移动已发布版本或运行部署脚本。

配置保存及其快照在同一事务提交；并发变化及其事件在原账号锁事务提交，日志失败则回滚该次变更。首次应用日志在账号与分组成功写入后记录；日志写入失败只产生包含账号 ID 的服务告警，不把已完成的导入报告为失败而诱发重复导入。

## 验证与截图

基线：owner fork `production` 的 `7b9fb3332f3b84110b88d81f517132a047f808e3`（源码版本 2.9.1）。开发分支：`feat/auto-config-recent-logs`。

已执行定向后端单元测试及 PostgreSQL/Redis testcontainers 集成测试，覆盖配置事务回滚、并发升级仅记一条、连续失败合并、删除账号保留快照、游标分页、参数校验和敏感字段过滤。仓库规定的 Go 1.27.0 / golangci-lint 2.13.0 对受影响包检查通过。

前端检查与构建已通过：

- make test-frontend（ESLint、类型检查及仓库关键用例）。
- AutoConfigHistory、AutoConfigView 定向测试和语言文案编译检查。
- pnpm --dir frontend run build。
- 后端 go build ./cmd/server，以及 CGO_ENABLED=0 go build -tags embed -trimpath ./cmd/server 内嵌前端构建。

未运行全量后端测试、GitHub CI 或生产部署验收；本次验证范围为修改相关用例、仓库前端关键用例、构建及本地浏览器。

浏览器使用本地 mock API 和虚构账号，不连接生产后端：

- 1920×1280 桌面布局；日志位于原配置和保存按钮下方。
- 类型筛选、加载失败保留原记录、重试、保存后自动刷新与空状态。
- 深色模式和 390×844 手机布局；页面无横向溢出，日志表格在卡片内部横向滚动。
- 浏览器捕获的运行时异常为 0。

截图仅为虚构数据预览，不能作为线上运行或真实业务历史的证据：

- [修改前](screenshots/auto-config-history/before.png)
- [桌面近期日志](screenshots/auto-config-history/desktop.png)
- [深色模式](screenshots/auto-config-history/dark.png)
- [手机布局](screenshots/auto-config-history/mobile.png)
