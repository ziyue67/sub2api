# Gateway 后台任务边界

`runtime.role=gateway` 是请求副本。它仍然要处理本机请求产生的数据，不能把所有后台 goroutine 都当作全局任务关闭。空角色与 `full` 保持兼容。

| 任务 | 分类 | gateway 行为 | 原因 |
| --- | --- | --- | --- |
| BPS warm pool | 每实例本地 | 保留 | 已验证出口池保存在进程内；请求只消费该池，关闭预热会产生 `warm_pool_empty`。PR #531 另行缩小账号查询投影。 |
| Deferred last-used | 每实例本地 | 保留 | 本机请求观测保存在内存，其他节点不能代为刷新。 |
| Ops system log sink | 每实例本地 | 保留 | 必须排空本机 warning/error 与已启用的访问日志。 |
| Audit writer | 每实例本地 | 保留 | 请求产生的审计记录必须落库。 |
| AccountOps 配置/事件队列 | 每实例本地 | 保留 | 配置变更继续生效，本机请求事件继续落库；自动配置队列保留。 |
| AccountOps 手工分组校准 | 全局维护 | 不启动 | full 根据完整账号集合维护共享分组，gateway 仅消费已保存的配置；不每15秒重新加载完整账号及关系。 |
| Audit retention | 全局维护 | 不启动 | 多副本扫描同一共享表不增加请求能力。 |
| Ops metrics collector | 共享协调的全局维护 | 不启动 | 既有 leader lease 避免同时聚合；gateway 仍无需反复读设置、竞争锁。full 的原有 lease 保留。 |
| Gemini cached-content mutation recovery | 共享协调的全局维护 | 不启动 | 扫描共享过期修改意图；请求内修改/补偿仍可运行，full 继续按原租约恢复。 |
| Concurrency startup/periodic stale scans | 全局维护 | 不启动 | 实现只回收共享过期槽位索引，并不代表本进程拥有的存活请求。 |
| Concurrency request admission/lease refresh/regional runtime | 请求路径与每实例本地 | 保留 | 必须维持跨节点并发语义和本地运行态。 |

并发周期扫描复用现有 API Key queue 停止 context；服务停止会取消当前扫描并退出 ticker 循环，不会继续保留后台 goroutine。请求槽位仍由请求所有者释放。

该分工要求至少保留一个 `full` 节点。角色门控本身不提供 full 节点选主，也不改变账本、schema、鉴权、真实出口、负载均衡或灰度路由。BPS 全账号重复读取与调度快照读写分离分别由其他聚焦改动处理。

验证由 CI 执行角色矩阵、审计排空、并发扫描取消回归；本地只格式化和静态差异检查，不代表多主机上线或真实请求验收通过。
