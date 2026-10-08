# 网站工单

截图来自本地演示环境：Docker 中的 PostgreSQL 18.1 / Redis 8.4、按本 PR 代码（基于 production `e8c9ae369`）构建的后端、前端开发服务器，以及无头 Chromium（CDP）。用户、邮箱（`*.v3demo.local`）、余额和工单内容全部是虚构数据，消息里的链接指向 `img.example.com`；没有连接生产数据库。截图时间为 2026-10-08 03:10 前后（北京时间）。

网站工单是新增功能，开关默认关闭；关闭时菜单和页面与修改前相同，所以没有修改前对照。

## 演示数据

- 系统设置里打开了网站工单：分类为默认的 5 个，每个用户最多同时开着 5 张，提交页说明填了一句工作时间。
- 3 个虚构用户共 5 张工单：#1 待处理（管理员回复过，用户又补充了一条，共 3 条消息）、#2 已回复（用户还没打开，所以用户菜单显示 1）、#3 已被管理员关闭、#4 和 #5 待处理。
- 管理员菜单上的 3 是待处理工单数；列表里标题前的红点表示用户发了新消息、还没有管理员打开过（#4）。

## 截图

| 内容 | 文件 |
| --- | --- |
| 用户：我的工单（新回复红点、未关闭数量和上限） | [user-list.png](user-list.png) |
| 用户：提交工单弹窗（管理员说明、分类、标题、描述、不要填写密钥的提醒） | [user-create.png](user-create.png) |
| 用户：工单详情（对话、网址变成链接、回复框、关闭） | [user-detail.png](user-detail.png) |
| 用户：工单详情，390 × 844 | [user-detail-mobile.png](user-detail-mobile.png) |
| 管理员：工单列表（默认只看未关闭、待处理排前面，按状态和分类筛选，搜编号、标题、邮箱） | [admin-list.png](admin-list.png) |
| 管理员：工单列表，390 × 844 | [admin-list-mobile.png](admin-list-mobile.png) |
| 管理员：工单详情（提问用户信息、回复后状态、标记处理中 / 关闭 / 删除） | [admin-detail.png](admin-detail.png) |
| 管理员：工单详情，深色 | [admin-detail-dark.png](admin-detail-dark.png) |
| 管理员：系统设置 → 功能开关 → 网站工单 | [admin-settings.png](admin-settings.png) |

## 验收要点

- 普通用户只看到自己的工单；打开别人的工单编号得到 404。普通用户的接口里没有管理员账号和提问用户信息字段。
- 用户回复后工单回到「待处理」并出现在管理员菜单的数字里；管理员回复后用户菜单出现数字，打开工单后消失。
- 消息按纯文本显示，只有 http(s) 网址会变成新标签页打开的链接（`rel="noopener noreferrer nofollow"`）。
- 1440 和 390 宽度下 `document.documentElement.scrollWidth` 都等于视口宽度，没有横向溢出。
