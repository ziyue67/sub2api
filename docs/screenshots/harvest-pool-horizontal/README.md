# 号池调度分组选项横向排列

入口：管理后台 → 打票全流程 → 号池调度 → 自动加入 / 移出指定分组。

2026-10-12 使用 Chrome CDP 渲染实际 `HarvestPoolSchedulingPanel.vue` 组件，Axios adapter 返回 13 个虚构分组和已开启的 groups 模式，拒绝非 GET 请求。截图不包含真实用户数据，也没有访问生产 API。

- `before-desktop.png`：基线 `367a2372b8a78ab51c84037e95432361726e34ff`，1440×900，13 个选项占 13 行。
- `after-desktop.png`：本目录首次提交对应的组件，1440×900，13 个选项占 3 行。
- `after-mobile.png`：同一修改后组件，390×900，长名称换行，页面 scrollWidth 和 innerWidth 均为 390，无横向溢出。

交互检查：点击分组复选框后，checked 为 true，保存按钮解除禁用。现有组件测试覆盖保存 groups 模式及失败保留编辑。本次没有点击保存连接真实接口，也未验证整页导航或生产部署。
