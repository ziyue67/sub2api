# 节点添加为静态代理：组件截图

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/mihomo-static-proxies)；下列文件名与验证说明保留用于追溯。

入口：IP 管理 → 节点。Chrome 实际渲染 MihomoSettings.vue，1440 × 950，本地 mock 数据。

- before.png：production 基线 433fb65cc88f900c94aeae920852ddf5e580a0f0 的组件。
- after.png：功能提交 7c810c483 的单条、批量入口。
- confirm.png：选中两个订阅节点和一个动态代理后的确认状态，含动态公网 IP 提示。

截图使用虚构节点，不连接生产接口；只验证组件渲染和弹窗交互，不代表真实账号请求或生产验收。预览使用本机字体解决浏览器中文字库缺失，字体文件不纳入提交。
