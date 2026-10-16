# 定向打票改为探针验收

源码基线：`ranxi2001/sub2api` 的 `production@d977e7a5e24e5536db6ce8b8478743e0c4b11f77`（2.10.5）。本地分支：`fix/targeted-ticket-probe-admission-20261011`。

旧流程只读到 `response.created`，用目标网关与 780 形态决定入库。新的原生采票流程是：节点轮换 → 目标账号自采 → 回带原票的完整探针 → 入库 → 账号选中。

- 每个候选从空 Cookie/state 开始。目标账号在同一定向出口、会话和协议完成两次短请求；第二次回带第一张票及其路由 Cookie。SSE 读取完整流，WebSocket 读取元数据及 `response.completed`，并通过 `client_metadata` 回带 state。
- 两次均完整成功、续接没有返回不同 state、路由 Cookie 对没有改变，才记为探针通过。只有握手、780/33 或模型声明均不能通过。重签按降级换节点规则处理；网络、流错误、取消、限流、预算不足不入库。
- 不按数据中心 host 筛票；旧 `target_gateway` 字段继续兼容读取，但不参与采票、票池查找或页面就绪判定。Cookie 完整性、有效期、票体格式及 240 秒票龄约束继续保留。
- 串行、后台、并行共用验收函数，两次上游请求均消耗原有共享预算。并行通道按每路两次请求限制实际并发；节点搜索和重试仍有原预算边界。
- 验收请求显式绕过网关借票。不会改变网关借票设置或账号业务轮询设置。
- 票据持久化增加可选 `probe_verified` 属性，无数据库迁移。旧的形态合格票需要重新采验；业务响应替换票或路由后不能继承该标记。旧流程事件没有探针标记，重启后也不会显示为探针通过。

页面的“780 校验”改为“探针校验”，成功说明是“完整续接通过 · 原票未重签 · 路由未变”。移除固定 host 输入，补充两次请求的预算说明。中英文同时更新。

这是门票续接探针，不是糖果题或完整能力测试；未执行半小时接力实验，也不作单票延寿结论。

## 本地验证

- 后端首次全量 `GOMAXPROCS=4 make test-unit` 通过；后续全量复跑仅 `TestGatewayService_StreamingKeepaliveUsesIdleTimer` 未等到 ping 而失败，该文件与实现未修改，随后独立 `-count=3` 复测通过。`GOMAXPROCS=4 make test-integration` 通过。
- 定向竞态测试覆盖原票回带、重签、路由变化、SSE 截断/后续错误、WebSocket 101 不足以通过、串并行共享预算、取消、旧票及旧事件、业务响应换票。
- 静态检查使用仓库 CI 对应的 golangci-lint 2.14.0，结果为 `0 issues`。系统默认旧 linter 不支持仓库 Go 版本，未作为有效检查结果。
- `make test-frontend`：lint、类型检查和 78 文件 / 1323 项测试通过。首轮新增页面测试的 mock 翻译预期有误，修正后全量通过。
- 前端生产构建和带前端嵌入的 Go 构建均在本机进行。测试使用 mock/本地输入，没有发送真实采票请求。
- 集成命令中依赖未配置外部服务的用例可能跳过；本机未设置 `HARVEST_TEST_POSTGRES_DSN`，专用 PostgreSQL 持久化测试未执行。
- CDP 在本地 mock 页面核对中英文文案、旧 host 输入移除及桌面无横向溢出。[改前页面](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/changes/images/targeted-ticket-before.png)与[改后页面](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/changes/images/targeted-ticket-probe.png)使用虚构管理员与节点；浏览器缺中文字体，截图采用英文，中文文案通过 DOM 核对。

截图均来自 `/admin/harvest-flow` 的实际页面及本地 mock API：改前源码为 `d977e7a5e24e5536db6ce8b8478743e0c4b11f77`，改后源码为 `f79b6b3088208c043e6ab71275554a9ab028123b`。画面展示各版本的成功状态，不代表真实上游结果。

本记录只说明源码与本地验证；源码版本号仍为基线版本。PR、发布和部署状态以对应记录为准。运维工作区的实验材料未修改。
