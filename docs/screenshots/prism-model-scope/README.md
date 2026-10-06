# Prism 专属模型选择

入口：账号管理 → 编辑 OpenAI OAuth 账号 → Prism。

截图来自本 PR 的实际 `EditAccountModal.vue`，使用本地 Vite、Playwright 和虚构账号 / mock API。验证了四模型默认勾选、仅保留 6.1 Sol 并提交保存、移动端深色布局；不是生产截图或整页导航验收。

- `before.png`：生产基线 `bc83ff9c367883e5b7d0140e6bb42e2e7cc5239c`，只有总开关。
- `after.png`：默认勾选四个已支持模型。
- `selected.png`：仅勾选 6.1 Sol，mock 更新接口收到 `openai_prism_browser_models: ["gpt-6.1-sol"]`。
- `mobile-dark.png`：相同选择在 390px 深色视口下的布局。

源码改动与截图在同一提交；`manifest.json` 记录生成时的基线、修改状态和浏览器错误。可复用生成脚本在运维仓库 `.agents/skills/sub2api-pr-management/scripts/screenshot-prism-model-scope.mjs`。
