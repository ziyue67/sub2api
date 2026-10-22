# 请求惩罚与个人安全记录截图

这些截图来自实际 `ProfileView.vue`、`SecurityRecordsView.vue` 和
`PenaltyPolicyPanel.vue` 的本地浏览器渲染。布局容器替换为 slot-only `AppLayout`，
身份、配置和 API 使用虚构 fixture；不代表真实登录、侧边栏导航、后端或生产验收。
截图不包含真实账号、密钥或客户提示词。

- 实现提交：`373e64e732be48f6a05aa6792403e0f13cc90401`
- 个人资料旧版基线：`d70a3b1e427fba8968c929fdbe24be2e29e13f14`
- 页面入口：个人资料 `/profile`、安全记录 `/security-records`；管理员配置位于 `/admin/prompt-audit`。
- 展示状态：会话命中 3/3 次、API 请求受限但账号 active、解除时间、按需展开原文、未受限状态、用户级和两级策略、深色及手机视图。
- 浏览器中实际执行：展开原文；保存 `user_block` 和 `session_route_then_block` 到 mock API；检查 390px 视口无横向溢出。未发送模型请求。

| 场景 | 截图 |
| --- | --- |
| 个人资料（改前） | ![改前](profile-before.png) |
| 个人资料（受限） | ![受限](profile-restricted.png) |
| 受限原因及触发原文 | ![原文](profile-prompt-detail.png) |
| 未受限状态 | ![正常](records-unrestricted.png) |
| 用户级处罚配置 | ![用户级](policy-user-block.png) |
| 两级会话处罚配置 | ![两级](policy-tiered.png) |
| 深色 | ![深色](records-dark.png) |
| 手机 | ![手机](records-mobile.png) |

复现命令（需要已有前端依赖和本地 Playwright/Chromium）：

```bash
node docs/screenshots/security-penalties/capture.mjs \
  --repo /tmp/sub2api-security-penalties \
  --playwright-module /tmp/sub2api-browser-deps/node_modules/playwright/index.mjs
```

原始回执见 `manifest.json`。脚本禁止非本机网络请求，不连接生产服务器。
