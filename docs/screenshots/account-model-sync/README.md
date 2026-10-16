# 新建账号模型同步

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/account-model-sync)；下列文件名与验证说明保留用于追溯。

入口：账号管理 → 添加账号 → OpenAI → API Key → 同步上游支持的模型。

截图来自实际 `CreateAccountModal`、`ModelWhitelistSelector`、`Toast` 组件及项目样式，在本地 Chromium 151 中渲染。使用英文界面、1200 × 900 视口、虚构账号和连接参数，所有接口响应均为本地 mock，没有调用生产或真实上游。

| 图片 | 代码版本 | 场景 |
| --- | --- | --- |
| `before-error.png` | `d977e7a5e24e5536db6ce8b8478743e0c4b11f77` | 模拟上游 HTTP 401；页面重复显示通用同步失败提示。 |
| `after-error.png` | `5d35757827d4587b693d281c60deaa38ac449609` | 相同响应下，页面显示安全的上游 HTTP 401 原因。 |
| `after-success.png` | `5d35757827d4587b693d281c60deaa38ac449609` | 修改尚未保存的 URL、Key 后手动重试，返回 `draft-model` 并更新白名单。 |

浏览器记录确认两次预览请求都使用当前输入；账号创建请求为 0。这个本地验证覆盖错误提示和未保存时的重试，不证明用户历史故障的唯一根因，也不替代真实上游或生产验收。
