# 自动重登服务状态

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/reauth-runtime)；下列文件名与验证说明保留用于追溯。

截图使用当前 TokenGuardV2View.vue 和本地合成 API；AppLayout 和 SmartOpsNav 替换为空壳，以便聚焦页面内容。无账号、令牌或生产连接。密钥状态固定为 configured/local_file。

- before.png：模拟原接口没有 worker 字段的展示，不声称是旧版本完整运行环境。
- running.png：内置重登服务就绪。
- unavailable.png：运行环境准备失败，独立显示原因和自动重试提示。
- mobile.png：390px 视口，页面 scrollWidth=390，无横向溢出。

浏览器为独立 Chrome profile，使用 CDP 渲染截图。桌面视口 1280×900，移动视口 390×844；不代表生产自动下载或真实登录验收。

[原接口展示](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/reauth-runtime/before.png)
[已就绪](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/reauth-runtime/running.png)
[准备失败](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/reauth-runtime/unavailable.png)
[移动端](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/reauth-runtime/mobile.png)
