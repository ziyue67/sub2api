# V3 责任分类预览

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/monitor-client-rejections)；下列文件名与验证说明保留用于追溯。

截图来自本地 V3 组件预览，所有分组、请求和统计均为虚构数据，没有连接生产数据库或真实上游。

- `before.png`：旧行为把无近期请求的旧红色状态继续显示为当前服务异常。
- `after.png`：无足够近期请求的组件显示为“暂无近期请求数据”，历史红格保留；真实上游 502 组件仍显示“服务异常”。

预览由 Chromium CDP 渲染，宽度 1440，页面没有横向溢出。
