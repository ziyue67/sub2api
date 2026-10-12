# Beta 糖果观测页面预览

实际 `HarvestFlowView.vue` / `HarvestPolicyPanel.vue` 本地浏览器渲染，API、账号与统计均为 mock；未连接生产。

- `before.png`：生产基线 `781942a82` 的流程页，尚无糖果观测。
- `after.png`：`e4ab63d52` 流程组件，展示观测通过/失败，同时账号仍可调度。
- `policy-off.png` / `policy-on.png`：`7ebb33c6c` 设置组件，默认关闭，以及点击启用并在 mock API 保存成功。真实设置 API 由后端测试覆盖，截图不代表生产保存。

预览使用原有 CSS；浏览器缺少中文字体时临时加载本机中文字体，字体文件未提交。布局外壳及无关设置面板已简化，不代表整页导航验收。
