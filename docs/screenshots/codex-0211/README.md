# Codex 目录与套餐显示

页面入口：用户「API Keys → 使用密钥 → Codex CLI」；管理员「账号管理」的套餐徽标。

截图由实际 `UseKeyModal.vue`、`BaseDialog.vue` 和 `PlatformTypeBadge.vue` 渲染，使用本地 Vite、Playwright Chromium 和模拟目录接口。URL、演示 Key、套餐与模型数据均为虚构数据。截图验证组件渲染和目录切换，不代表整页导航、真实 Codex 请求或生产验收。

- 修改前：`2d55b424e3cac42814e81c9fec700595985faafb`。
- 修改后：`5a07b0560b8d48a40c0094dd95c4983538f4f050`。
- 浏览器错误：两次捕获均为 0，详见各目录的 `manifest.json`。
- 复现脚本：运维仓库 `.agents/skills/sub2api-pr-management/scripts/screenshot-codex-catalog.mjs`，传入目标源码、前端依赖目录、Playwright 模块路径及输出目录；修改前版本使用 `--before`。

| 状态 | 修改前 | 修改后 |
| --- | --- | --- |
| 获取目录后的配置 | [原本地配置](before/catalog-before.png) | [默认远程配置](after/catalog-remote.png) |
| 套餐徽标 | [旧套餐名称](before/plans-before.png) | [新套餐名称](after/plans-after.png) |
| 手动选择本地文件 | — | [本地模式](after/catalog-file.png) |
| 获取超过 1 MiB 的目录 | — | [自动切换本地文件](after/catalog-oversized.png) |
| 深色模式 | — | [超限提示](after/catalog-dark.png) |
| 移动端 | — | [390 px 宽度，滚动至目录区域](after/catalog-mobile.png) |
