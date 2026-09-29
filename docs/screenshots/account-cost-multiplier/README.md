# 账号成本倍率界面验证

所有图片均为真实 Vue 组件在本地 mock 环境中的浏览器截图，使用虚构账号，不连接生产服务。修改前基线为 `production@9e034afc4a4ab9385a57c7d2cdbb32bd272582d2`。桌面视口为 1440 × 1000，手机视口为 390 × 844。

| 界面 | 修改前 | 修改后 |
| --- | --- | --- |
| 编辑账号 | [桌面](account-edit-before-desktop.png) | [桌面](account-edit-after-desktop.png)、[手机](account-edit-after-mobile.png) |
| 优先调度 | [采购成本区](priority-before-desktop.png) | [账号倍率估算](priority-after-desktop.png) |
| 批量编辑 | 原版没有独立成本字段 | [单独设置成本倍率](account-bulk-after-desktop.png) |

浏览器交互验证：

- 单账号成本从默认 `0.1` 改为 `0.25`，保存请求携带 `extra.cost_multiplier: 0.25`，账号计费倍率和分组计费倍率均保留 `1`。
- 批量编辑仅勾选成本倍率时，请求携带固定账号 ID 和 `extra.cost_multiplier: 0.1`，不携带两类计费倍率。
- 手机页面宽度为 390，弹窗客户区与滚动宽度均为 373，没有横向溢出。
- 优先调度页不再显示单号采购成本、换算系数、成本窗口和回本状态，示例候选显示 `0.100×`。

截图内的收入、成本、分数和账号为展示数据；计算逻辑另由后端单元测试和数据库集成测试验证。
