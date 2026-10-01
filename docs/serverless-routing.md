# Serverless 地区到 Pod 路由

入口：设置 → 网关服务 → Serverless 管理。默认关闭，无独立侧栏。首期管理已有 gateway Pod 的自动注册、健康、地区绑定及入口流量统计，不创建、删除或扩缩容 Kubernetes 资源。

## 接入

主站和 gateway 使用同一 PostgreSQL/Redis，并通过 Secret 注入同一个至少 32 字节的 RUNTIME_SERVERLESS_SECRET。不要把真实密钥写入配置示例、日志或表单。

gateway 额外配置 RUNTIME_ROLE=gateway、RUNTIME_SERVERLESS_ID=nerd-us-1、RUNTIME_SERVERLESS_ENDPOINT=https://pod.example.com、RUNTIME_SERVERLESS_REGION=US。主站只需设置集群密钥。逻辑 ID 全局唯一，进程每次启动获得新实例标识；同 ID 的新进程不能覆盖仍有新鲜心跳的旧实例。

endpoint 必须由主站可达，并固定到指定实例，不能指向随机负载均衡的 Service。跨集群 Pod/ClusterIP 通常不可达，应使用专属 HTTPS 入口或受信隧道；HTTP 只接受私网/loopback IP。反向代理须同时转发 /internal/serverless/probe、模型路由及 /api/bps-images/，不能把专属入口又送回主站。

在面板确认上报地址、保存、检查连接后，启用接收新绑定并创建地区规则。例如 US → nerd-us-1。一个地区可关联多个 Pod，使用 API Key 标识的稳定散列排序选择健康实例。心跳和入口签名健康探测都通过才接收新绑定。

## 地区识别

复用现有前端 IP 地区分析所用的 GeoJS 接口及 country_code 口径。浏览器 localStorage 不作为后端可信路由输入；服务端对经受信代理链解析的客户端 IP 查询 GeoJS，结果存 Redis 24 小时，失败短缓存 1 分钟。无需导入国家 IP 网段。私有/无效 IP 不向外查询，未知地区回默认主站。

首次新 API Key 绑定可能发生一次最多 1.5 秒的查询；每实例最多 8 个并行唯一 IP 查询，同 IP 合并请求。开启功能后，首次公共客户端 IP 查询会从服务器发送给现有 GeoJS 提供者。IP 归属不保证实际物理位置。配置 server.trusted_proxies，地区选择不信任客户端直接提交的国家头。

## 请求与会话边界

- 覆盖 /v1 及 OpenAI/Codex 根路径别名中的同步 responses、messages、chat/completions，以及 Responses WebSocket；图片生成、异步任务、管理、支付和其他协议端点保持原路径。
- 入口在现有 API Key 鉴权后路由，不申请第二份账号并发、不写第二份用量；目标继续正常鉴权、账号选择、计费。
- 同一 API Key 固定 Pod/启动实例，最后使用后保留 24 小时，不按单次地区变化重新分配。首次启用前结束已有 BPS/WS 会话。首期不提供细粒度会话迁移。
- 默认主站也是绑定。未知地区/健康失败的新绑定回主站，之后不会因地区查询恢复就迁移已有绑定。
- 停用全局开关或 Pod 停止新绑定，已有绑定继续；删除 Pod 规则、实例重启或绑定目标失联返回 503，不悄悄迁移会话。移除前先排空。密钥轮换会改变签名及绑定命名空间，需要协调排空。
- 跨节点转发包含绑定到方法、路径、认证头、客户端 IP、实例及时间的 HMAC，Redis 防重放；目标不再次转发。用户 Token 只发到管理员确认且通过签名探测的地址。
- SSE 即时刷新，取消传到目标；WebSocket 保持双向连接。转发失败不跨节点重试已发送请求。
- BPS 图片链接携带签名实例归属，匿名能力下载回到原生成 Pod；实例丢失时链接不可恢复。

统计为 UTC 当日已完成入口 HTTP/WS 请求、HTTP 错误及路由原因，保留 8 天；流内业务错误不一定改变 HTTP 状态。统计失败不改变计费或请求结果。Pod 表保留最近 24 小时注册，35 秒未更新视作未就绪。

## 验证与上线

无数据库迁移，配置存现有 settings，注册/绑定/计数使用独立 Redis 前缀。代码完成不代表已启用，所有入口实例和目标 Pod 必须先部署兼容版本。先验证健康、可信 IP、协议回放和成本缓存，再由管理员显式开启；健康探测不调用模型。

回归使用虚构 IP、miniredis、HTTP/SSE/WS 模拟服务，覆盖签名、防重放、重复 Pod ID、实例重启、断流取消、图片所有者与配置停用。模拟测试不等同生产流量验收。
