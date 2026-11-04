# 旧版全局管理密钥来源 IP 白名单

仅约束 `settings.admin_api_key` 的认证分支，保留原密钥和原权限。
不改变 JWT、作用域管理密钥、业务 API Key、计费或路由策略。

## 配置与操作

入口：系统设置 → 安全 → 旧版全权密钥兼容区 → 管理员 API Key。
新增“限制来源 IP”开关及逐行 IP/CIDR 输入区。当前请求 IP 只供参考，不自动加入。
页面区分已保存模式及未保存编辑，独立保存，不随通用系统设置表单提交。

管理员 JWT 专用接口：

* `GET /api/v1/admin/settings/admin-api-key/ip-policy`
* `PUT /api/v1/admin/settings/admin-api-key/ip-policy`

PUT 必须同时提交 `enabled: boolean` 和 `allowed_ips: string[]`，并遵守现有 step-up 中间件。
例如：`{"enabled":true,"allowed_ips":["203.0.113.10","2001:db8::/64"]}`。
这些仅为文档示例，不能直接作为生产名单。接口返回 `current_ip` 供参考。
旧 Key、作用域 Key、observer 和普通用户不能读取或修改此策略。

最多 100 条，支持 IPv4/IPv6/CIDR，去空白、规范网段、去重，mapped IPv6 转为等效 IPv4。
拒绝域名、URL、端口、zone ID、通配符、非法前缀及 `/0`；mapped IPv6 全 IPv4 网段同样拒绝。
开启时必须至少有一条有效地址，关闭时允许保存合法地址备用。

## 持久化与失败策略

策略存入单个 settings 行 `legacy_admin_key_ip_policy`，JSON 同时保存开关和地址，数据库单行 upsert 原子更新。
无该行时默认关闭，以兼容升级。已有行缺字段、损坏、空列表且开启，或者数据库读取失败，旧 Key 请求拒绝（503）。
不使用进程级缓存：每次正确旧 Key 的认证读取共享数据库，所有实例的新请求立即读取已提交策略。
已有 Key 校验仍先执行；错误 Key 保留原认证失败响应，不暴露策略。
来源不匹配返回 403 / `ADMIN_KEY_IP_DENIED`，不运行目标 handler。
配置不可用返回 503 / `ADMIN_KEY_IP_POLICY_UNAVAILABLE`，不自动关闭保护。
JWT 管理员可用完整合法 PUT 修复损坏配置；不会因为旧策略读取错误而放行旧 Key。

配置变更按现有 settings 结构化审计方式记录操作者、来源和前后策略；拒绝记录方法、路由模板、来源、状态、原因。
拒绝日志全进程限速（每秒一条、突发十条），后续允许记录携带此前被抑制的数量。均不记录 Key、JWT、Cookie 或查询参数。

## 可信来源及凭据优先级

复用 `ip.GetTrustedClientIP`，不自行从 XFF、X-Real-IP 或 CF 头取值。
实际可信性仍取决于部署中的 Gin 可信代理与 verified forwarded IP 中间件配置：上线前必须核实代理链和反代覆盖写头。
禁止为兼容设置全网可信代理。CDN/Nginx/Docker/本机调用分别确认应用识别来源，不能拿浏览器出口代替脚本出口。

保留已有凭据优先级：普通 HTTP 的 x-api-key 优先；旧 Key IP 拒绝后不回退 JWT。
现有管理员 WebSocket 在带 JWT subprotocol 时优先 JWT，此分支保持不变；仅以旧 Key 认证的升级请求仍执行本策略。
本策略只约束认证时来源，不追溯关闭已通过认证的长连接。

## 验证与发布

单元测试覆盖认证拒绝不执行 handler、错误 Key、JWT、伪造头、损坏/不可用、映射地址、CIDR、配置接口角色及原子读写。
前端覆盖保存状态、空名单/数量、规范化回读及失败保留编辑。CI 增加带 race 的专项回归。
隔离实例应使用独立数据库和 Redis、自造管理员和 Key，无生产数据、无付费上游，分别从允许和拒绝的真实连接来源测试。
同时核对 JWT/作用域 Key/业务 API 不被本策略影响，并验证代理头伪造、热更新、多实例和回滚。
实测结果应另行记录；测试通过不能声称生产已启用。

首次生产部署保持关闭；另行核对所有旧 Key 调用方出口、可信代理及恢复 JWT，再启用。当前开发任务不修改生产名单。
回滚可通过管理员 JWT 保存 `enabled:false` 与原条目，或恢复备份策略。不要删除原 Key、用户或审计记录。
此功能不解决数据库中 Key 明文可取用及已允许调用主机被攻陷的风险。
