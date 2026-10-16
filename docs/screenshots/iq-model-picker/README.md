# 智商测试模型选择截图

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/iq-model-picker)；下列文件名与验证说明保留用于追溯。

入口：账号管理 → 智商测试 → 题目测试。截图为真实 `IQTestModal.vue` 组件的本地模拟预览，使用虚构账号，不连接生产或真实模型。

- 修复前组件：`17d8c566264b0c60f1bd00f48c62fd6d08756f88`。
- 修复后组件：`435ca0843da47d9192c2b5c84ea622ff10bd634c`。
- Chrome 154，视口 1704 × 929，设备像素比 1.5。截图时关闭下拉框过渡动画，避免后台标签页停在透明过渡帧。
- `credentials.model_mapping` 配置 `codex-auto-review`、`gpt-5.5`、`gpt-5.6`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-astra`、`gpt-6-sol`、`gpt-6.1-sol`，均为同名映射。
- Axios adapter 模拟 `/admin/accounts/42/models` 返回其中 6 个模型，省略 `gpt-5.6-terra`、`gpt-6.1-sol`；思考强度接口返回 low/medium/high，默认 medium。

| 图片 | 操作与观察 |
| --- | --- |
| [before-six-models.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/iq-model-picker/before-six-models.png) | 展开修复前的模型选择器，仅显示上游目录返回的 6 个模型。 |
| [after-eight-models.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/iq-model-picker/after-eight-models.png) | 在相同 mock 条件下展开修复后的选择器，显示 8 个已配置模型。 |
| [manual-input.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/iq-model-picker/manual-input.png) | 点击“手动输入”，在真实输入框填写 `gpt-6.1-sol`。 |

截图未验证整页导航、真实账号目录、真实推理请求、移动端或生产部署。请求中的模型 ID 由 `IQTestModal.spec.ts` 的 mock 回归验证。
