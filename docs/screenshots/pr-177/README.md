# PR #177 页面宽度验证

截图来自本地 Vite + Chrome，使用 mock 配置、虚构管理员和空列表，界面语言为中文。

- `*-before.png`：2560px 视口，在同一页面恢复原 `max-width: 1660px; margin: auto` 样式后截图。
- `*-2560-after.png`：2560px 视口，修复后使用全部可用内容宽度。
- `*-390-after.png`：390px 视口，修复后的窄屏布局。

`ops` 为账号运维，`guard` 为凭证守护。截图验证对应源码提交 `a6192bbe45211fa8f6d51364c1b9d049710f27c3`；未连接生产服务。
