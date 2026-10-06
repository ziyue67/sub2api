# 网关借票界面验收

页面入口：`/admin/harvest-flow`。截图来自实际 `HarvestFlowView` 与 `HarvestGatewayBorrowPanel`，使用本地 Vite、简化的 slot 布局及模拟 API；不是生产截图，也未验证真实登录导航或上游借票效果。

- 修改前：`dd1d9bdf5d996254a56e967a5c3b8e81039c48a3`，`before-desktop.png`。
- 修改后：`8d61a4f4f`，`after-desktop.png`（页面入口）、`borrow-expanded.png`（配置）、`borrow-ready.png`（保存启用后的模拟探针通过状态）。
- `borrow-dark.png` 与 `borrow-mobile.png`：深色与 390px 移动端。运行表格可横向滚动；页面没有水平溢出。
- 数据为虚构账号 #101 / #202、文档 IP 192.0.2.10；不含用户凭据、Cookie 或门票原文。

脚本验证展开、保存和状态更新，浏览器错误 0。复用脚本保存在运维仓库 `.agents/skills/sub2api-pr-management/scripts/screenshot-harvest-gateway.mjs`，参数和时间见 manifest。
