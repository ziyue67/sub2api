# 2026-11-01 安全缺陷修复与验证

基线：ziyue67/sub2api，adfef7f（PR #149）。独立工作分支
`fix/security-retests-20261101`；不修改原有脏工作区，不连接生产账本执行测试。

## 修复范围

### #1420：暂定报废，暂停采用（2026-11-02）

按用户决定，本 PR 已撤下 reasoning 缓存用户隔离实现及其专属测试，恢复为
基线 adfef7f 的原有行为。不合并该方案，不清理生产 Redis，不改变生产程序。
#1420 保持未解决状态，不标记为漏洞已修复；“报废”仅指本次候选方案暂停采用。

此前单层真实验证证明直接用户隔离有效，但两层真实链路中，客户共用源站 Key
再分发时仍能通过相同 reasoning item ID 回注另一最终用户的 reasoning。
因此不能声称该候选覆盖多层转售。历史测试结果仅适用于撤回前提交 1cf4756，
不再作为当前 PR 包含 #1420 修复的证明。原始证据保留，不删除历史报告。

本 PR 现仅包含 #1419、#1410、#1425 三项修复。

### #1419：退款同步冲正订单返利

仅使用 `source_order_id` 明确关联的 accrue 流水，按退款额/订单额比例计算应冲正值，
减去已有 refund_reversal，避免重复扣回。保持原始 accrue 流水供追溯。

依次冲正冻结返利、可用返利、邀请人余额；已转余额或已消费/提现不能使返利免责。
余额不足时允许形成负余额债务，不用零余额截断冲正。部分退款仅按比例冲正。
冻结负流水与原入账保留相同冻结截止时间，后续解冻按净额处理，避免返利复活。

订单退款终态、返利冲正与最终退款审计写入同一数据库事务，失败全部回滚。
网关 pending 不提前冲正；confirmed success 才处理。事务提交后清除邀请人余额及
认证缓存。退款与转余额/提现/解冻使用相同 affiliate 行锁顺序；延迟返利任务在
事务内重新检查订单状态，退款中或已退款订单不可再次入账返利。

不自动扫描历史退款、不猜测无 source_order_id 的老返利归属。旧退款问题需独立
对账决定是否追缴。此版本不调整历史返利入账列表的原始记录展示。
如支付网关已退款、但本地最终事务失败，应核对网关后恢复本地终态，不能直接再次
发起支付退款；本次未重写现有支付退款恢复流程。

### #1410：V1 渠道监控按可用分组过滤

列表和详情统一取得服务端 GetAvailableGroups，普通用户/observer 只可见精确同名
的可用分组。不可见详情返回原有 404；分组查询失败不泄漏任何监控数据。
admin 保留完整视图。V1 没有 group_id，仅有 group_name，因此空名称、已删除名称和
无法对应真实可用分组的自定义标签对非 admin 隐藏；管理员应校准这些名称。

### #1425：Observer 凭据导出

从 Observer 能力白名单移除 accounts GET /data、export-session 创建与下载，及
batch-update-credentials。管理员权限不变；普通账号管理/测试及受控导入保留。
不创建、不升级任何 Observer 用户。

## 验证方式

- 原基线 + 新断言：退款未追回返利、Observer 导出均出现
  预期断言失败；修复版相同核心断言通过。V1 另有基线镜像 HTTP 复现证据及角色测试。
- 定向单元测试：角色隔离、退款待确认、部分冲正、重复确认、审计失败回滚。
- Reasoning/DeepSeek 旧有测试保留作兼容回归，不代表当前 PR 修复了缓存隔离。
- 独立 PostgreSQL 18：可用/转余额/已消费/冻结/部分退款、待确认完成、比例幂等。
  并发用例必须观测到真实 PostgreSQL 行锁等待，然后提交转账，断言余额和返利
  最终均归零且仅冲正一次。
- 兼容回归：Payment、Affiliate、Refund、Fulfillment、Reasoning、DeepSeek、Billing。
- 新增 security-retests GitHub workflow，在 Linux 和独立 PostgreSQL 上执行
  race、上述回归与服务端构建。工作流存在不代表远端 CI 已执行或通过。

## 当前验收边界

本地 Windows 全包回归曾出现目录 fsync/symlink 权限、拒绝连接代理识别失败及
服务测试总时限不足。代理与仓储平台失败已在未修改基线复现；另有两个时序/目录
用例在基线单独运行通过，不能把整包失败统一称作已证明的旧问题。
定向通过不能替代全部必需 CI。最终交付应分别列明源码测试、PostgreSQL、race、
远端 CI、生产部署状态；本修复不以健康检查代替业务验收。

## 本轮已完成的结果

- 定向 service/handler/middleware：通过；最终 race 三包均通过。
- Payment/Affiliate/Refund/Fulfillment/Reasoning/DeepSeek/Billing 两包回归：通过。
- 独立 PostgreSQL：六类全额/部分退款、pending 成功、比例幂等全部通过。
- PostgreSQL 真实转账行锁等待用例：通过；实际仓储解冻的全额/部分冲正用例：通过。
- 服务端构建通过，git diff --check 无错误。
- 没有发起真实付费模型请求；临时 PostgreSQL 容器和 SSH 隧道已清理。
- 生产容器仍为 sub2api-pr149-adfef7f-production，running/healthy，未部署本分支。
- 已提交 PR #150。首轮 Linux CI 发现两条旧 Observer 测试仍期望授权分组内可导出凭据；已改为 403，并补充会话下载、批量凭据路径断言，保留普通操作的分组隔离与合法操作断言。安全工作流同时纳入 handler/admin。远端最终状态以 PR 最新提交的检查结果为准。

本地日志（忽略文件，不作为生产日志）：`.security-target-verified.log`、
`.security-race-final.log`、`.security-business-regression.log`、
`.security-postgres-verified.log`、`.security-postgres-concurrency.log`、
`.security-postgres-thaw.log`。未修改基线的失败证据位于相邻
`security-fixes-baseline-20261101/.security-baseline-red.log`。
