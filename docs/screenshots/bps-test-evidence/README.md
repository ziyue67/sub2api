# 本地组件预览

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-test-evidence)；下列文件名与验证说明保留用于追溯。

入口：账号管理 → 连接测试窗口中的上游响应详情。

截图于 2026-10-10 从本地 Chrome 实际渲染的 `AccountTestEvidence.vue` 采集，组件源码对应 `0ab0c94988793eedd09c0fab65895f00504df102`。采用虚构数据，视口 1100 × 850。组件挂载在本地独立预览页；本次整合树中的组件与该提交逐字节一致。

覆盖 HTTP 200 与 403/model access changed 的响应证据，不把 HTTP 200 当成业务终态成功。

这是新增组件的状态展示，不代表整页导航、真实接口、上游认证或生产验收。
