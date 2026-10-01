# Serverless 设置面板截图

对应源码提交：f56180dd6591f424a12fdf808e06dd78b906000a，组件 frontend/src/components/settings/ServerlessSettings.vue。

页面入口：设置 → 网关服务 → Serverless 管理。截图直接渲染实际 Vue 组件和项目样式；外层标题为本地预览容器，未运行完整 Settings 页导航。API、Pod、example.com 地址和流量计数全部为虚构数据，不是生产截图或后端业务验收。

- collapsed.png：默认折叠，功能默认关闭。
- expanded-light.png：展开后的配置、健康状态、地区与 Pod 绑定、统计。
- saved.png：模拟连接检查和保存成功状态。
- expanded-dark.png：深色主题，已等待主题过渡结束。
- mobile.png：390px 窄屏深色布局。

复现脚本位于运维仓库 .agents/skills/sub2api-pr-management/scripts/screenshot-serverless.mjs；参数及依赖说明见同技能 references/ui-screenshots.md。脚本只监听 loopback，浏览器仅允许访问本地预览，不连接生产 API。复用已安装依赖，未重新安装依赖或完整构建应用。

展开、模拟探测、启用与模拟保存均执行成功；页面错误和失败响应为空。manifest.json 记录源码 SHA、数据边界和生成时间。截图不证明真实健康探测、GeoJS、Pod 注册、SSE 或 WebSocket 在生产环境已经通过。
