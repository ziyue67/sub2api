# 账号状态 BPS 标签预览

基线：`a53a7ff163d9337a094e7537df3aac2b31f308b7`（`production`，版本 `2.9.5`）。

截图于 2026-09-30 在本地 Chromium 中生成，使用实际 `AccountStatusIndicator.vue`、项目样式和中文语言包。展示的是隔离组件预览，不是生产账号管理页面；所有数据均为虚构数据，未连接生产 API。

- `before.png`：基线组件，正常、错误、限流和未开启 BPS 的四种示例。
- `light.png`：修改后的浅色模式，相同四种示例。
- `dark.png`：修改后的深色模式。
- `mobile.png`：修改后的深色模式，390 CSS px 宽。

桌面视口为 880 × 250 CSS px，手机视口为 390 × 400 CSS px，device scale factor 为 2。前三个示例使用 OpenAI OAuth、Plus 和 `extra.openai_excel_bps: true`；第四个不设置 BPS 开关。限流示例使用约 10 分钟后的恢复时间。

浏览器检查：修改前无 BPS 标签；修改后前三个示例显示标签、第四个不显示；背景色为 `rgb(33, 115, 70)`，文字为白色；无页面运行错误。截图已检查，不包含凭据、邮箱或真实账号信息。

本地验证通过：

- `make test-frontend`：lint、类型检查、51 个关键测试文件 / 852 项测试。
- `pnpm --dir frontend exec vitest run src/components/account/__tests__/AccountStatusIndicator.spec.ts src/components/account/__tests__/ExcelBPS403Badge.spec.ts`：43 项通过。
- `pnpm --dir frontend build`：通过。构建提示 Browserslist 数据过期及部分 chunk 超过 500 kB。
- `git diff --check`：通过。

本次未运行后端测试、完整前端测试集和生产联调；改动仅涉及状态展示及对应测试。
