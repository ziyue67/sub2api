# v0.2.15 同步界面证据

使用实际 Vue 组件与仓库 Tailwind 配置在本地 Vite 中渲染。API 客户端替换为 mock，统计值为虚构数据，API Key 字段为空；未连接生产、未提交账号，也未执行真实上游调用。

- 改动前源码：`d3e43f2de33af9e987cffa511d76dfabbcd749da`。
- 改动后源码：`30dc573f3cc4d1ec5760b06ea9ccdb5dd271c5ee`。
- 入口：账号管理 → 添加账号，以及运维监控；这里为组件预览，未验证整页路由、登录或后端接口。
- `before-overview.png` / `after-overview.png`：账号筛选紧凑布局、运维监控新增单请求输出 TPS 卡片。
- `cline-light.png` / `cline-mobile.png`：点击添加账号后选择 Cline；展示可用协议与默认端点。
- `command-code-light.png` / `command-code-dark.png`：选择 Command Code；展示自适应与三种协议端点。
- `after-filters-mobile.png`：390px 宽度下展开“更多筛选”。

截图由 CDP `Page.captureScreenshot` 直接生成，没有拼图或后期文字。尺寸和来源见 `manifest.json`。
