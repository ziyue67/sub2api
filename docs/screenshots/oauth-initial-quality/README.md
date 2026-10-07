# OAuth 首次质量规则截图

- 入口：智能运维 → 自动配置 → OAuth 账户首次配置。
- before.png 对应基线 7719df8be40dcc612b3ce63bdbbe67f136d009b6。
- after-empty.png / after-saved.png 对应源码 afb0a5bf915db0745d8f94780b2c4a0dbaa13019。
- 实际 AutoConfigView 组件和 Tailwind 样式；外层布局、无关卡片及 API 使用本地 mock。所有账号和分组数据均为虚构。
- 已操作：选择规则 #81、保存并显示成功状态。页面 JavaScript 错误：0。
- 仅证明该组件的展示与 mock 交互；不是生产截图，不证明真实 API、整页导航或真实账号导入验收。
