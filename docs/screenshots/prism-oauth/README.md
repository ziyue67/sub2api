# Prism OAuth 开关截图

入口：Accounts → Edit。截图来自本地构建的真实账号列表与 EditAccountModal，后端接口均为 mock，账号名称、邮箱和数据均为虚构。展示开关关闭和打开两种状态，不能作为生产 UI/API 端到端验收证据。

渲染源码：`4482c64` 的前端，此后本分支只新增开关保存回归测试，没有改变被截图的组件或文案。构建工具：pnpm 9.15.9、Vite 5.4.21；浏览器为本机 Playwright Chromium。

- `oauth-switch-off.png`：关闭 Prism。
- `oauth-switch-on.png`：启用 Prism，显示仅支持 Sol 文本的范围。
