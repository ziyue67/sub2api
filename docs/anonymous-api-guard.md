# 匿名 API 鉴权失败防护

本功能的上线与启用需由维护者另行执行；本改动不部署、不自动启用。

目标是限制明确无效的客户端凭据反复失败，不能用 HTTP 状态码、trace 中缺失 user_id 或下游错误判断匿名攻击。

## 配置和入口

后台「设置 → IP 风控」中的「匿名 API 鉴权失败防护」独立开关，保存于现有 `ip_risk_control_v1.anonymous_api`。不依赖全站 IP 风控的 enabled/observe_only；紧急关闭 `IP_RISK_DISABLED=true` 同时禁用两者。现有白名单共享，但手动黑名单、国家/UA 策略和登录临时封禁保持原语义，可能独立拒绝流量。

建议起点（不是最优值）：60 秒内 30 次明确失败，临时封禁 300 秒，最多跟踪 10000 个 IP。它给偶发误配留出重试余量，短 TTL 方便恢复；启用前应按实际匿名失败量和共享出口规模调整。所有数值可编辑并受边界验证。IPv6 使用精确 /128，IPv4 使用 /32，IPv4-mapped IPv6 归一化，不按 /64 聚合共享用户。

只使用现有可信 IP 解析器 `GetTrustedClientIP`（Gin trusted_proxies 和现有经验证的内部转发身份），不使用兼容模式的原始转发头优先逻辑。必须先正确配置可信反代；不任意信任 XFF、Forwarded、X-Real-IP。非法、带 zone、内网、回环、链路本地、多播、CGNAT、文档/基准/保留地址跳过自动计数，避免将内部反代合并为攻击者。IPv6 仅处理当前全球单播 `2000::/3` 分配范围，并保守跳过协议专用段（包括其中的可路由例外），不把特殊用途源地址视作普通公网客户端。地址范围依据 [IANA IPv4 注册表](https://www.iana.org/assignments/iana-ipv4-special-registry/) 和 [IANA IPv6 注册表](https://www.iana.org/assignments/iana-ipv6-special-registry/) 核对；这些范围是保守跳过策略，不是可路由性探测。

## 分类和共享 NAT

计数点在 API key 的明确失败分支，覆盖主 API 鉴权及 Google 原生鉴权。区域网关 local-auth preview 仍走原有独立路径（模型服务本来就被无条件 503 gate 阻断），未接入此保护；其查找错误混合了未知 key 和业务权限拒绝，不能直接当匿名攻击计数。缺失、格式错误/过长、未知 key 和显式 disabled key 计数。过期/配额耗尽 key 保留业务语义，不当成匿名攻击。已知 key 对应用户停用/缺失、IP ACL、分组权限、余额/订阅/额度限制、鉴权数据库故障/过载及 handler/上游的 401、503、断网均不计数。URL 中已弃用凭据的策略拒绝不计入新计数器。

必须先确认凭据无效才应用 IP 临时 ban。因此同出口合法 key 不会因别人的匿名垃圾被拒绝。代价是带未知 key 的请求仍需现有缓存/数据库鉴权判定；本功能不是边缘流量清洗，不承诺消除鉴权查询成本。缺失和过长凭据在鉴权查询前直接判定。原有本机 invalid_abuse 限制也移至明确失败分支，避免其在读 key 前拒绝合法用户。

`reset_on_success` 默认 false：合法 key 不改变同 IP 的匿名失败预算，靠滑窗和 ban TTL 自然衰减，防止一个合法 key 永久洗掉攻击计数。可选 true：通过 key、用户状态和 IP ACL 检查后清理该 IP 的失败历史与临时 ban；此配置意味着共享出口任一有效 key 都可以重置匿名预算。下游业务失败不增加匿名预算。

## Redis 和资源界限

滑窗计数、容量准入、创建 ban、记录脱敏原因均在同一个 Redis Lua 原子操作内。使用 Redis TIME，所有实例共享时间/状态；不使用进程本地惩罚降级。窗口左边界排除，达到阈值的该次失败即返回 429 和 Retry-After。ban TTL 固定，继续失败不会延长。每个 IP 最多保留 threshold 个时间点；全局 registry 的成员最多 max_tracked_ips，计数/ban key 都有 TTL。容量满时新身份不再分配状态，原鉴权仍返回 401；不全局封禁陌生 IP。不启动全量 key 扫描/清理任务，仅原子操作按有界 registry 清除过期成员。

Redis 访问复用已有 150ms 超时和 1 秒故障退避；故障时自动防护 fail-open，原鉴权继续执行（无效 key 仍 401，合法 key 仍可通过）。跨实例 ban/解除通过 Redis 即时可见；配置沿用现有最长约 5 秒的缓存刷新。Redis 驱逐/重启会丢失惩罚状态，恢复后重新计数。新共享保护启用时接管旧本机 invalid_abuse 惩罚，防止白名单被旧保护再次拒绝或 Redis 故障时出现不一致降级；新开关关闭后，旧配置仍按原设置生效且不跨实例。旧保护也已改为只在明确凭据失败后检查。

## 管理、隐私与解除

管理员专用 API：`GET /api/v1/admin/settings/ip-risk/anonymous-bans` 查看最多 500 条当前临时 ban，`POST /api/v1/admin/settings/ip-risk/anonymous-unban` 以 `{ "network": "IP 或精确 CIDR" }` 解除。后台有独立列表、每条解除按钮及精确 IP 解除输入框，可处理列表 500 条限制之外的已知 IP。仅解除这个精确 IP 的匿名计数和 ban，不删除永久 deny、登录封禁、账户状态或其他安全规则；持续失败会重新触发，需维护者自行确认原因。批量修改 max_tracked_ips 下降时，存量状态自然到期后满足新上限；不扫描删除其他 IP。

配置更新沿用管理员权限/审计；匿名解除使用独立 `admin.ip_risk.anonymous.unban` 审计动作。列表包含 IP 网络、固定错误类别、创建/到期时间，不包含 key、token、用户邮箱、UA、URL、请求正文，也不把 key 作为 Redis 维度。自动 ban 原因只接受 missing/malformed/unknown/disabled 白名单，禁止传入任意错误正文。成功时的清理不开额外请求日志。后台现有受限审计管道记录管理操作；IP 风控配置保存和匿名解除整段请求体不入审计，配置只附布尔摘要，解除附 IP 网络的截断 SHA-256 摘要，不新增正文采集。

无数据库迁移，不涉及账本/结算，不发模型请求。禁用开关立即停止新增计数/应用 ban（跨实例配置刷新窗口除外）；已有 TTL 状态自行到期，也可管理员提前解除。回退二进制忽略新增配置字段；使用旧后台保存配置可能丢失该字段，重新启用需再次确认。

## 安全审计二级 IP 审计与恢复

「安全审计 → IP 审计」提供独立管理员入口 `/admin/ip-audit`，与现有内容/提示词审计同级；提示词审计页面也有 IP 二级 Tab（`?tab=ip`）。IP 审计本身不受内容审核开关阻断。阈值与规则编辑仍打开已有 `/admin/settings?tab=security#ip-risk-settings` 的 IPRiskSettings，不复制第二份配置；旧 `/ip-risk` 管理路由和原列表继续兼容。

来源能力清单来自后端，而非前端假设：

| 来源 | 权威存储/范围 | 列举与管理 |
| --- | --- | --- |
| 匿名 API ban | Redis，精确 IP / 固定 TTL | 索引分页；仅解除选中 ban 和该来源触发计数 |
| IP 风控 ban/观察 | Redis，全站网段；执行/观察分离 | 分来源索引分页、独立解除；不同时清掉登录旧 ban |
| 旧登录失败 ban | Redis，IP 哈希（IPv6 /64） | 无反向 IP 索引，仅可按已知精确 IP 查询；解除该登录 ban 与失败计数 |
| 登录频率 limiter | Redis，四个既有登录路由桶、60 秒窗口 | 无反向索引，精确 IP 查询实际计数/剩余 TTL；逐路由清理桶 |
| 手工 deny / allowlist | settings，IP/CIDR | 有界真实规则、临时/永久状态，只读并链接已有规则编辑，不自动改白名单 |
| API key ACL | 每个 key 的 PostgreSQL 授权 | 无通用 IP 管理能力，只读能力说明；不遍历/改写用户所有 key |
| 用户/会话安全惩罚 | PostgreSQL，非 IP 范围 | 只有显式已知 UID 才查现有状态，不从 IP 反查；单条状态只读。用户范围恢复明确确认会重置该 UID **所有当前**惩罚（含本页外会话/策略），复用已有用户级事务和解除审计，再读 Summary；不改 users.status、ACL 或余额 |
| 旧单实例 API guard | 本地内存 | 无共享管理 API，只读未核实说明 |
| Fail2ban / iptables | 外部宿主 | 明确未接入/不可核实，需获授权运维人员操作；不创建 shell、SSH、root 或挂载通道 |

`GET /api/v1/admin/settings/ip-audit` 支持 `source`、`ip`/CIDR（仅适用来源）、`state`、`start`/`end`（Unix 秒）、`sort=expires_desc|expires_asc`、`cursor`、`limit`（1–100，默认 20，cursor 0–1000000）。索引来源每次最多检查 500 个候选；筛选后空页仍可能 has_more，cursor 会推进。来源持续变化时分页不是一致性快照；需刷新核对。手工规则最多沿用现有 2048 条，Redis 历史最多沿用 2000 条；禁止扫描全部 Redis key。用户 Summary 自身最多 100 条时明确 partial，不把这部分视图当全部。开始/到期未采集时返回 null，不补造创建时间；默认显示当前配置阈值，不伪造 ban 创建时的历史阈值。

`POST /api/v1/admin/settings/ip-audit/release` 只接受一个明确来源和稳定 ID，必须 `confirm=true` 和固定原因码（operator_review/false_positive/configuration_fix/support_request）。Redis 来源带读取时的版本指纹；新匿名/IP 风控 ban 的 Redis 值附内部随机代际标识（不公开到列表，也不改变计数/TTL），防止同秒内重新触发被旧确认匹配。Lua 在同一原子操作内比较值和期限，冲突返回 409 要求刷新，不将空筛选扩大为全量解除。登录桶期限指纹使用 Redis TIME/PTTL，在 Redis 6+ 可用，容忍最多 2ms 计算误差。解除后检查真实来源 key 是否仍存在，并再查同 IP 的其他已接入来源；返回 `released/readback/remaining/unknown_sources`，`unrestricted` 始终 false。IPv6 /64 登录 ban 的恢复不会扩展为其他来源的网段清理。精确查询产生的 `check_ip` 会在解除时保留，并校验其属于选中网段，用它重查该原始地址的 /128 匿名 ban；没有具体地址的网段操作会明确增加未穷尽来源提示。

用户级安全惩罚恢复是独立、明确的全用户范围确认，不假装逐会话解除；新命中仍可能在恢复后再次限制。并发新命中可能使重读显示 still_present。来源失败/重读失败只说明未确认；前端刷新后展示错误，不声称全链路已放行。

管理员 GET 也进入既有受限审计；解除审计保留操作者、时间、来源、固定原因、结果、重读状态及目标网络摘要/明确 UID，整段请求体不入库。历史 Tab 只列既有 Redis IP 事件或真正已存在的操作审计；旧记录目标未知则保持未知，不解析原正文重建目标，不把操作者 IP 当解除对象。清除历史不代表解除。

## 停用用户的观测关联

`GetByKey` 成功后已有的 Ops fallback 只在 request_timing 收集元数据时作为正式 authctx 的回退，记录可信 user/key/group ID。新 trace.detail 附 `identity_source=recognized_credential` 和固定 `auth_rejection=USER_INACTIVE`，管理员连接诊断以已有 ID JOIN 显示关联用户名，并明确标识“已识别 key；认证被拒绝”。未知 key 保持无归属。

这不提前设置已认证上下文，不改变 401，不放行停用用户，不调用上游；USER_INACTIVE 继续不进入匿名 ban。只影响新观测，没有历史回填或生产查询。已有手动外部处置仍为用户指定的 manual 属性，开发代码不把它转换为匿名攻击判定，也不写入该真实目标 IP 的示例数据。
