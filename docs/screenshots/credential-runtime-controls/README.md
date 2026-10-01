# 凭证运营即时设置截图

2026-09-30，基于 production 9f7d96be（2.9.6）。实际 TokenGuardV2View、加密设置卡片和 BaseDialog，使用虚构 API 数据；外围布局及导航替换为预览容器，未连接生产接口。

- before-add-account.png：基线新增弹窗。
- desktop.png：顶部引擎 / Worker 数量和账号行即时开关。
- switched.png：切换 Session Studio、5 个 Worker，并关闭自动巡检。
- add-account.png：移除运行开关后的新增弹窗。
- dark.png、mobile.png：深色与 390 px 宽度。

截图不含真实邮箱或凭据。浏览器交互无 pageerror。Worker 实际伸缩另由 Python 真实子进程测试验证；截图不证明真实上游登录成功。
