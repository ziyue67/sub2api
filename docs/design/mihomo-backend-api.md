# Mihomo 管理后端接口

本模块维护一个共享 Mihomo Manager，页面迁移不会新建内核或复制订阅解析逻辑。IP 管理、系统设置和运维客户端复用相同管理员接口。

本 PR 只包含管理后端。BPS warm pool、请求选路、模型转发、H2 降级及前端布局不属于本 PR。

## 接口与权限

沿用已有管理员认证、中间件和 response.Success/response.Error 包装。下文状态字段指响应 data。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | /api/v1/admin/system/mihomo | 内核、来源、节点和下载设置状态 |
| POST | /api/v1/admin/system/mihomo | 提交已有内核动作或来源管理动作 |
| PUT | /api/v1/admin/system/mihomo/download-mode | 独立保存订阅下载模式 |

POST 操作可能异步执行。HTTP 接受操作不代表应用成功；前端应轮询 busy、phase、error，完成后再显示成功。PUT 下载模式是独立的持久化操作，不下载订阅、不重启内核、不修改账号出口。管理动作串行化，忙时拒绝并发写入。

## 下载模式

请求示例：

    {"mode":"direct"}

- auto：旧配置的默认行为。内核运行时先经 Mihomo 下载，失败后尝试直连；失败也包含空响应和非 Clash/Mihomo 节点文档。内核未运行时使用直连。
- proxy：仅经已运行的 Mihomo 本地出口下载。内核未运行或代理失败时返回错误，绝不自动改成直连。
- direct：明确直连下载，不使用 Mihomo，也不继承 HTTP_PROXY 环境代理。

状态响应提供 subscription_download_mode。模式独立保存，下一次来源更新使用新值；保存模式失败不修改原值。该配置不作用于门票探测或模型业务流量。

非法模式或缺少 mode 返回 400；管理器关闭、忙或存储失败返回冲突/失败，不回显订阅 URL 或代理凭据。该 PUT 正文限制为 4 KiB；管理 POST 保持既有 256 KiB 限制。

## 来源管理动作

POST 正文保留 action、subscriptions、dynamic_proxies、append、country_filter，并支持可选 name。带目标的动作使用状态响应中的稳定 ID，不使用行号或显示名称。

| 动作 | 目标/参数 | 行为 |
| --- | --- | --- |
| subscription_add | subscriptions，可选单来源 name | 添加、去重，保留其他来源 |
| subscription_update/ID | 一个替代地址，可选 name | 更新指定来源 |
| subscription_rename/ID | name | 改名，不重新下载 |
| subscription_refresh/ID | 指定来源 ID | 只刷新指定来源 |
| subscription_enable/ID | 指定来源 ID | 启用来源，必要时取得缓存 |
| subscription_disable/ID | 指定来源 ID | 停用来源，保留配置 |
| subscription_remove/ID | 指定来源 ID | 移除来源，不重新下载被删除的坏订阅 |
| dynamic_append | dynamic_proxies | 追加动态代理，保留订阅 |
| dynamic_replace | dynamic_proxies | 替换动态代理集合，保留订阅 |
| dynamic_remove/节点ID | 动态节点 ID | 只删除指定动态代理 |
| dynamic_clear | 无 | 清空动态代理，保留订阅 |

旧 install、start、apply、apply_dynamic、节点启停/探测及地区管理入口保持兼容。新页面应使用来源管理动作；旧 apply_dynamic 仍保留原先“替换为仅动态来源”的语义，不能将它当作追加操作。

## 状态与数据边界

- subscription_items：id、label、enabled、nodes、cached、可选 updated_at。完整地址、路径令牌和认证信息不回显。
- 节点状态包含来源 ID，供前端按来源筛选；共享节点可属于多个订阅。
- 每个订阅独立缓存已解析节点。删除一个来源保留其他来源仍拥有的共享节点和动态节点。
- 最后一个来源被移除后，运行配置拒绝无来源流量，不自动变成直连。
- 旧 settings.json 无需数据库迁移；缺少的逐来源缓存在首次成功更新时补齐。不能把缺失缓存解释成已经验证可用。
- 只导入订阅中的 outbound 节点，不接受订阅提供的监听端口、控制器、规则或可执行配置。
- 配置修改先构造候选，校验和内核应用失败时保留原配置；持久化使用受限权限与原子写入。

## 集成顺序与来源

后端来源管理从 dec0cfc67db5902f43d564b06db65b9ce0be1ef3 提取，下载模式独立封装。#142 中新增的下载路由及 Manager 集成回归也归入本后端 PR；保留下载设置不改变来源缓存、标签和启停状态的测试。

#143 以 production 为基线，负责通用 Mihomo 管理后端；#142 以 #143 的 feat/mihomo-management-backend 分支为基线，只增加 IP 管理前端与接入文档。先合入 #143，再将 #142 的目标分支改为 production，核对前端差异后合入。

BPS 预热、节点优先级和模型请求选路继续由 #133 维护，不作为 #142 或 #143 的后端依赖。前端只在服务返回 bps_warm_pool / bps_ip_warm_pool 时显示预热状态；#143 单独运行时不提供这些字段。

回归覆盖：坏订阅移除、共享节点保留、最后来源移除、单源刷新失败保留原配置、控制器拒绝回滚、仅代理不直连回退、直连绕过运行代理、自动模式格式失败回退、下载配置持久化与敏感数据不回显。
