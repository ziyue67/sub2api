# 账号允许调度的一次性计划

账号管理的「调度」列，在现有立即开关上方提供「定时打开」「定时关闭」。桌面表格和移动卡片使用同一组件。每个账号可以同时保留一个打开、一个关闭计划，形成一次开关时段；不会按天重复。同动作保存会替换旧计划，相反动作不能安排在同一 UTC 瞬间。

## 设置时间

- 相对时间：正整数小时和分钟，可直接选择「8 小时后」。从保存事务获得账号锁、服务端接受计划时起算，浏览器关闭不会影响执行。预览到保存之间的等待不计入这段时长，保存后的待执行状态展示最终时刻。
- 固定时间：日期 + `HH:mm` + IANA 时区。日期留空只预览该时区下一次这个时刻的具体日期；勾选确认后保存该明确日期及预览的 UTC 瞬间。明确填写的过期日期直接拒绝，不自动顺延。
- 时区明确默认 `Asia/Shanghai`，有北京时间快捷入口和 UTC 快捷入口。当前账号页没有独立的用户显示时区偏好；本功能不取浏览器时区、请求时区或服务器 `TZ` 充当显示偏好。
- 预览展示当地日期/时间、原始 IANA 时区和最终 UTC 偏移。服务端载入随二进制提供的 tzdata；夏令时/政治时区变更造成的不存在或重复本地时间均拒绝，需选择其他明确时间。
- 最长默认 366 天，足够覆盖一年内一次性维护，避免多年遗忘的开启计划。可在所有实例统一设置 `ACCOUNT_SCHEDULABLE_TIMER_MAX_DAYS=1..3660`；无效值回退 366 天。上限十年控制误输入及时间计算范围。降低上限只影响新建/编辑，不删除已保存计划。

待执行条目展示动作、日期、时间、时区、偏移和相对倒计时，可以编辑或取消。倒计时以服务端返回的时间和浏览器单调时钟估计，仅用于显示。服务端每 30 秒轮询，负载、锁竞争、通知传播会增加延迟，不保证秒级准点。界面每 30 秒刷新计划状态；发现执行完成后重新读取账号列表，立即开关以服务端当前值为准。

## 恢复、安全和失败

关闭计划可在离线恢复后补执行；打开最多允许逾期 15 分钟，超过即标记 `skipped/open_expired`，必须人工重新安排。非 active、已软/硬删除或到期账号的打开也会跳过，保留可解释结果。关闭不启用账号，删除账号的关闭跳过。

执行仅修改 `accounts.schedulable` 和 `updated_at`，不修改 `status`、到期日期、额度、限流、临时质量隔离、手动 IP ACL、凭据或任何余额/账本/结算/支付状态。现有调度器仍检查原硬门槛，显示「打开成功」不保证账号满足其他可用条件。自动 cooldown、到期或质量策略不会带管理员手动上下文，不会取消所有计划。

管理员使用原立即开关或批量立即开关时会收到明确确认警告：该账号全部待执行打开/关闭计划将一起取消。后端在同一事务内完成取消、立即更新及 outbox；即使立即值已等于原值，也会取消待执行计划。显式立即切换的现有契约为 `POST /accounts/:id/schedulable`，以及 bulk-update 中的 `schedulable` 字段。普通账号 PUT 的 DTO/service 输入不提供该字段，不属于立即调度切换入口；普通编辑始终保留数据库中的调度值，并去除继承的手动取消标记，防止覆盖刚执行的计划或取消无关计划。

数据库可写时，账号更新或 outbox/终态审计失败会整体回滚，按 30、60 秒退避后重试，总共最多三次执行尝试，第三次失败进入 `failed/execution_error`（终态中保留计算的 120 秒下一尝试时间，但不再被领取）。如果连数据库都不可达，无法可靠地记账尝试次数，数据库恢复后重新检查持久化待执行行，打开仍受 15 分钟补执行窗口约束。API 事务有 5 秒上下文限时；后台每批最多 50 项、5 秒上下文，避免无限阻塞。提交响应不明确时不盲目重放，下一轮重新检查状态。

审计表记录账号 ID、计划版本、动作、原计划操作者、到期 UTC 时刻、原始时区、实际记录时刻、结果及固定原因码。取消记录取消操作者；重新保存保留旧审计，新版本记录新的操作者。API 返回最近 20 条记录；审计不含凭据，数据库错误不会原文返回给前端。

## 事务与多实例

`account_schedulable_timers` 以 `(account_id, action)` 为主键，最多两行当前状态；终态保留版本，重新保存递增版本。pending 部分唯一索引拒绝同账号同瞬间冲突；`(next_attempt_at, account_id) WHERE status='pending'` 支持有界到期查询，不扫描账号全表。

所有计划保存/取消、显式手动开关、定时执行都先锁账号行，再锁计划行，遵守同一顺序。worker 使用账号 `FOR UPDATE SKIP LOCKED` 作为事务内 claim，重新核对版本、状态、重试时间和到期时刻。较晚的同账号动作等待较早 pending 动作进入终态，避免多个实例调换开关次序。取消/编辑与执行并发时，旧候选版本不能再写回；若执行已提交，取消返回版本冲突，编辑需确认最新状态。

账号 bool、任务终态、执行审计和现有 scheduler outbox 在同一事务提交。提交后复用账号快照同步；即使直接缓存写入失败，持久化 outbox 继续承担既有传播路径。该保证是数据库中至多一次逻辑效果，并不宣称通知只投递一次。普通修改/批量修改的取消沿用原 Ent 事务和原 outbox。到期 worker 复用 `AccountExpiryService` 的启动/停止及现有后台服务角色控制，没有 OS cron、任意 shell、SSH 或额外常驻生命周期。

## API（均要求管理员）

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/admin/accounts/:id/schedulable-timers` | 当前两项状态、20 条审计、服务端时刻、轮询/补执行/时长限制 |
| POST | `/api/v1/admin/accounts/:id/schedulable-timers/preview` | 解析时间并返回 UTC 和当地预览 |
| POST | `/api/v1/admin/accounts/:id/schedulable-timers` | 保存，包含 `action` 和 `expected_version` |
| DELETE | `/api/v1/admin/accounts/:id/schedulable-timers/:action?version=N` | 版本匹配时取消 |

保存相对时间例：`{"mode":"relative","hours":8,"minutes":0,"timezone":"Asia/Shanghai","action":"open","expected_version":0}`。固定保存要求明确 `date`、`time`、`timezone`、`confirmed_at`（与服务端重新解析的 UTC 瞬间一致）。首次版本 0，后续从 GET 读取，旧版本返回 409；相反动作同瞬间返回 409。观察员与普通用户不能读写新定时 API；原管理路由范围/认证机制继续生效。

## 迁移与回退

新增 `295_account_schedulable_timers.sql`，遵守不可变、forward-only 迁移规范，只增加两张表及索引，不修改现有账号 schema。没有 Ent schema 变更或 Wire 依赖生成要求。

回退到旧二进制时，旧代码可以忽略附加表，但不会执行、展示或取消这些计划。因此不能直接回退后又无审查地重新升级：先由新版本管理员取消 pending 计划并停止新版本 worker，留存审计，再回退；旧二进制期间创建的手动意图无法自动记录到新计划表。若将来删除表，应单独编写后续迁移，保留所需审计，不向本文件追加 Down SQL。本轮只验证最小隔离 fixture 的增删表兼容性和静态迁移约束，未运行应用迁移入口或真实迁移文件。

## 定向验证

核心包只有 stdlib/SQL 依赖，管理员 timer HTTP 子包独立于大型 service 编译。使用 Go 1.27.3，`GOMAXPROCS=1 GOMEMLIMIT=600MiB`，数据盘 `GOCACHE/GOTMPDIR/GOPATH`，`go test -p 1 ./internal/accounttimer ./internal/handler/admin/accounttimerapi`。

集成测试 `-tags integration ./internal/accounttimer` 需要显式 `ACCOUNT_TIMER_TEST_DSN`，只接受 loopback 下的 `schedulable_timer_fixture` 测试数据库。每个测试自行新建唯一 schema 和最小 fixtures，结束后删除自己的 schema；不使用应用数据、真实模型或迁移 runner。测试覆盖模拟时钟、时区/DST、同瞬间冲突、版本替换/取消、启动恢复、多 worker、有限失败重试、账号删除/到期/禁用、通知回滚和硬门槛哨兵。

前端定向 Vitest 位于 `SchedulableTimerControl.spec.ts`，另核对中英键完整性和现有账号页测试。桌面/移动浏览器截图来自真实 DataTable + 新组件的隔离 mock harness，完整账号页和全栈真实缓存传播仍需后续 CI/集成复核。
