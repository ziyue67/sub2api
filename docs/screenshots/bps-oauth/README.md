# BPS OAuth 入口预览

2026-10-08。截图使用 Chromium 渲染实际 Vue 组件，页面为本地组件预览、账号为虚构演示数据，没有连接生产管理 API。

- `before.png`：production 基线 `58681e537` 的添加账号组件，OpenAI 只有 OAuth、API Key、2FA。
- `after.png`：本 PR 添加 BPS OAuth，选择后展示 BPS 模型范围。
- `login.png`：BPS OAuth 授权步骤，说明官方回调 URL 的处理方式。

截图对应本 PR 最终实现；用于检查入口和文案，不作为生产部署或后台接口真实联调证据。
