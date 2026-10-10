# BPS 授权错误展示

入口：账号管理 → 测试账号连接。截图为实际 Vue 组件的本地预览，API 使用虚构账号与 `OPENAI_EXCEL_AUTH_VERIFICATION_REQUIRED` 响应，不连接生产或上游。组件外的预览标识不是产品界面。

- `before.png`：基线 `c703a99a1ca5976e68957b0ef4d8c801ca3d0eb3`，`frontend/src/components/account/AccountTestModal.vue`；模型列表失败后没有错误提示。
- `preparing.png`：自动授权期间保留原生模型选择，成功后自动切换，无需重复操作开关。
- `after.png`：本提交的同一组件；明确显示自动登录被安全验证阻塞、重新加载模型和凭据运维入口。
- `admin.png`：本提交的 `frontend/src/components/admin/account/AccountTestModal.vue`；同样的错误状态。

浏览器：本地 Chromium，经 CDP 控制，1100 × 900。已验证将 mock 切换为返回模型后，点击“重新加载模型”会清除错误并恢复模型选择。截图不代表完整后台导航或真实授权成功。
