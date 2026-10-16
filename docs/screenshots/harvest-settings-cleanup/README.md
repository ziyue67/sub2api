# 打票设置与结果文案清理

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/harvest-settings-cleanup)；下列文件名与验证说明保留用于追溯。

截图使用 Chrome CDP 渲染实际 Vue 组件及仓库样式，接口均为本地 mock。设置页由 `SettingsView.vue` 渲染并截取 Codex 设置区域；策略和出口截图分别由 `HarvestPolicyPanel.vue`、`HarvestProxySettings.vue` 渲染。数据使用示例配置，不含生产账号、票据或代理凭据；没有发送模型请求或保存生产设置。

修改前来自 `production@1a74ed0e3cc6cf6923ba4747e8147decf15556b0`，修改后截图与本目录同一提交中的代码对应。浏览器为 1280 像素宽、浅色模式，预览补充本地中文字体；未验证移动端、深色模式或真实生产接口。

- [修改前的 Codex 设置](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/harvest-settings-cleanup/before-settings.png)：同时出现旧票据长度文案、打票策略和代理配置。
- [修改后的 Codex 设置](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/harvest-settings-cleanup/after-settings.png)：保留请求时区和客户端限制，并指向功能所属页面。
- [打票全流程中的自动打票策略](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/harvest-settings-cleanup/after-policy.png)：开关、缺票策略、模型、采集范围和刷新策略独立保存。
- [IP 管理中的采票出口](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/harvest-settings-cleanup/after-proxy.png)：统一选择内核、静态代理或 IP 管理池，独立保存。

页面入口：`/admin/settings?tab=gateway`、`/admin/harvest-flow`、`/admin/proxies#harvest`。保存行为由组件测试和后端设置接口测试覆盖；截图仅证明实际组件渲染。
