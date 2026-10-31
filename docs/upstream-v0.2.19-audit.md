# 上游 v0.2.19 全范围源码集成（Refs #589）

用户已授权固定 tag 全集集成，包含定价、usage、per-image、fileData、model-plaza 和 Grok；旧审批前置条件已废止。
基线 `e6b30da8f75b382a20b56b712a209e9f7d8c72fb`；目标 ranxi2001/sub2api production，沿用 Draft #590。
v0.2.18 `ac1b8f1271be0d674fa6f074403c1410f30a3a81` → v0.2.19 `dece26153621959ccd7635814f4b2d08304e681a`。
71 个提交 = 50 个非 merge leaf + 21 个 merge；未 merge main、未引入 tag 后 hotfix。
47 个源码 leaf 已移植或适配，3 个机械/已有等价项明确映射；当前所有新增实现等待新 head 远端验收。

## 发布说明清单

- [x] 管理员 API Key：可按操作和资源范围授权，支持过期时间和 IP 白名单；只保存不可逆摘要，撤销记录保留用于审计（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 按张计费：OpenAI 和 Composite 分组新增「按张计费」开关（默认关闭），一次生成多张图时按上游实际返回的张数计费，只对专用图片端点生效（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] OpenAI：支持 GPT-6.1 Terra 模型（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] Gemini：同账号重试次数可通过 gateway.max_same_account_retries_gemini 配置（默认 4，范围 0–10）（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] Grok：管理端新增账号模型目录诊断，可查看来源、可信状态和与本地映射的差异；手动同步和后台观察统一使用同一套解析规则（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] Gemini：刷新默认模型列表，移除已停用的 2.0/2.5 系列和 3-pro-preview；连接测试默认模型改为 gemini-3.5-flash（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] Gemini：分组内账号都配置了模型映射时，/v1beta/models 列表和详情按声明的模型返回，与 /v1/models 口径一致（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] Gemini：绑定显式缓存的请求在池模式上游网关账号上，按配置在同一账号重试（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 国产 Coding Plan 账号遇到请求频率型 429 时改为秒级冷却，不再停调到 5h 窗口结束（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 模型列表刷新在并发冷启动时不再重复请求上游（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] Grok：原生 Responses 和各转换路径都会记录上游原始响应模型，便于审计（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复 Gemini 以 fileData 形式返回的图片未计费的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复渠道按次计费的图片档位丢失 token 用量的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复 DeepSeek V4 Pro 在 9 月 14 日之后定价失效的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复 DeepSeek prompt_cache_hit_tokens 未计为缓存读取的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复 OpenCode 转发 DeepSeek 时 developer 角色导致 422 的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复 Antigravity 非流式畸形工具调用返回空成功响应的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复超大 multipart 图片上传被静默截断的问题，现在直接拒绝（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复分页参数过大时偏移量溢出导致 500 的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复管理员保护规则校验返回 500 的问题，现返回 400（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复保存渠道时覆盖 web_search_emulation 平台维度的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复模型广场分组模型定价被图片兜底档位覆盖的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 修复 Grok 模型目录快照被失败请求、乱序请求或旧表单覆盖的问题（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] 本版本包含数据库迁移 247（scoped_admin_api_keys 表）和 248（groups.image_count_billing 列），启动时会自动执行（源码已集成，动态证据见 PR 与 PROGRESS.md）
- [x] Gemini 默认模型列表已移除 2.x 系列；仍需使用这些模型的账号请自行在白名单或模型映射中配置（源码已集成，动态证据见 PR 与 PROGRESS.md）

## 调用链与 fork 适配

- 先前 ff9739b14 的分页、429、guard、multipart/tool/角色/UTF-16/websearch、Gemini retry/cache/models/singleflight、scoped keys 全部保留，历史 CI 证据仍在 PROGRESS.md。
- DeepSeek Pro：移除默认价卡按日期转 Flash 的策略，保留 PricingAt 峰谷时点及自定义分组/渠道价格优先级；不修改任何历史账本数据。
- DeepSeek cache：OpenAI usage fallback 补 prompt_cache_hit_tokens；嵌套 cached_tokens（含显式 0）仍权威。
- Terra 6.1：协议/别名/目录/价卡独立增加，GPT-5.6 Terra 价卡不覆盖；保留 fork unknown-model fail-closed、priority/context/custom pricing 行为。
- 图片：实际返回数优先；开启新开关的图片端点未知数兜底 1，关闭时保留 fork JSON/OAuth 的 N 与 SSE 的 0。显式 group/channel token 价始终优先；按次/图片卡保留 Token 用于上下文档位选择。混合 Responses/Chat/WS 不因开关强制按图。fileData 与 inlineData 每 part 最多计一次，累计 chunk 取 max。
- 新迁移：scoped keys 使用 310；image_count_billing 使用 311，避免覆盖 fork 已有 247/248。Ent 描述符/索引按 fork 字段位置适配；auth cache 版本 31，保留配置组及 local epoch。
- model-plaza：分组模型级图片/按次卡不再被图片兜底档覆盖，保留官方价展示和 token context/time 路径。
- Grok：原生/聚合事件模型审计、统一 parser/scope、失败/乱序 CAS、手动/后台同步、诊断 DTO/UI/模式隔离/草稿/无障碍全部导入；保留团队限流、client disconnect、SSRF、metadata、媒体资格、凭据/质量控件。
- Grok managed extra 复用 fork 行锁内完整 currentExtra，保留 ticket/quality/admission/probe 状态归属；新增单元 fixture 与 fork SQL 投影一致，不改原有断言。

## 50 leaf 来源映射

| 原 SHA | 上游主题 | fork SHA / 处置 |
| --- | --- | --- |
| [5db2d695](https://github.com/Wei-Shaw/sub2api/commit/5db2d6956ba60fb5c9401823561a166d38295095) | fix(billing): retain DeepSeek V4 Pro pricing after September 14 | `2f56bd419`，已适配；待当前 head CI |
| [de908ed6](https://github.com/Wei-Shaw/sub2api/commit/de908ed6d29339fec04dcd9b39dab95b47a49575) | fix(ratelimit): 国产 Coding Plan 的频率型 429 改用秒级冷却 | `6e7229f8b`，已适配；待当前 head CI |
| [4197fa62](https://github.com/Wei-Shaw/sub2api/commit/4197fa62021ca1fe7933fe3eb08ae119dc1fe91b) | feat(gemini): 同账号重试次数改为可配置 gateway.max_same_account_retries_gemini | `45554fc25`，已适配；待当前 head CI |
| [f9652804](https://github.com/Wei-Shaw/sub2api/commit/f9652804894eb150942af2b108bbce670594460c) | fix(pagination): keep page offsets from overflowing int | `1213442fa`，已适配；待当前 head CI |
| [9aa16c8c](https://github.com/Wei-Shaw/sub2api/commit/9aa16c8c66326afdd2e1b070e3403ac36e8a7050) | fix(pagination): validate numeric ranges and reuse safe offsets | `0c3cf0226`，已适配；待当前 head CI |
| [5c185c5c](https://github.com/Wei-Shaw/sub2api/commit/5c185c5c5d4f5e409e2139806e0964ae944e03e8) | chore: sync VERSION to 0.2.18 [skip ci] | fork 版本映射：保留 fork VERSION，跳过上游机械版本写入 |
| [e445cc3e](https://github.com/Wei-Shaw/sub2api/commit/e445cc3e2d9c9f330f2af069d6693bfdeb2245ad) | 新增按操作与资源范围授权的管理员 API 密钥 | `c4c565c3d`，已适配；待当前 head CI |
| [fd998129](https://github.com/Wei-Shaw/sub2api/commit/fd998129ec8375c6623e061c7e5e391752dc954a) | feat(billing): support per-image billing for multi-image requests | `8a400e177`，已适配；待当前 head CI |
| [1487fa51](https://github.com/Wei-Shaw/sub2api/commit/1487fa511bffbb26ebcebb82a717264c84ffb3a0) | fix(billing): count only images returned in upstream response | `8a400e177`，已适配；待当前 head CI |
| [cb295924](https://github.com/Wei-Shaw/sub2api/commit/cb2959245f7ab452e152a2e77bf811f9be66cbfa) | test(api): include image_count_billing in available groups contract | `8a400e177`，已适配；待当前 head CI |
| [aa1a1b11](https://github.com/Wei-Shaw/sub2api/commit/aa1a1b11c864a009ddd83e1f9010b0f92bd1da0b) | fix(billing): gate per-image billing by endpoint and pricing mode | `8a400e177`，已适配；待当前 head CI |
| [917dbdc7](https://github.com/Wei-Shaw/sub2api/commit/917dbdc705d69a65e1fccdd0bba2dc0f7afdbc67) | fix(gemini): count fileData image outputs for billing | `34bfb1c92`，已适配；待当前 head CI |
| [014cc016](https://github.com/Wei-Shaw/sub2api/commit/014cc016f9a01b0b5ee61493cbb0ff282e81d7ac) | fix(grok): 补全原生 Responses 响应模型审计 | `38e3f9581`，已适配；待当前 head CI |
| [7131b997](https://github.com/Wei-Shaw/sub2api/commit/7131b997ca0bb5fb68459ced150a1428d3107935) | fix(grok): 补齐聚合路径跨事件模型冲突 | `ba4c83119`，已适配；待当前 head CI |
| [d01f2eb4](https://github.com/Wei-Shaw/sub2api/commit/d01f2eb42cf4dccc22690acc849be2d6ce54f118) | fix(grok): 固定聚合前序模型审计并跳过非 Grok | `a11bf53d9`，已适配；待当前 head CI |
| [6202da0f](https://github.com/Wei-Shaw/sub2api/commit/6202da0f53b0ed41d889a66c03a61d2854aea0f9) | fix(grok): 统一模型目录解析与观测状态 | `71c9af021`，已适配；待当前 head CI |
| [87266a7b](https://github.com/Wei-Shaw/sub2api/commit/87266a7b181024f1e48dcdb3ff5c53018496c30a) | fix(grok): 原子保存模型目录观测快照 | `0b6b9cbf2`，已适配；待当前 head CI |
| [b1cb2eb1](https://github.com/Wei-Shaw/sub2api/commit/b1cb2eb1f30d5f098fe509964b0d793c3ceb40ea) | fix(grok): 补齐取消与调用方事务的回归断言 | `473e40f64`，已适配；待当前 head CI |
| [41a6ff8a](https://github.com/Wei-Shaw/sub2api/commit/41a6ff8ac1209da6057f46e1ae49b92852d3baa2) | fix(grok): 对齐手动同步与后台目录观察 | `7b0a1d615`，已适配；待当前 head CI |
| [635b8353](https://github.com/Wei-Shaw/sub2api/commit/635b8353508eb0b8994afcc5b1ef73c5b270d1b1) | fix(grok): 让旧后台观察测试走 CAS 持久化 | `83e903519`，已适配；待当前 head CI |
| [d4136cea](https://github.com/Wei-Shaw/sub2api/commit/d4136cea5e6c3c8fb40487f3f4692ab72919e8b1) | fix(grok): 提供账号模型目录差异诊断 | `cd600e781`，已适配；待当前 head CI |
| [bb11e83c](https://github.com/Wei-Shaw/sub2api/commit/bb11e83c44abe9786c20cffca4445190bf2b63fb) | fix(grok): 裸别名无法核验且自定义 ID 保留大小写 | `5ddd97cd2`，已适配；待当前 head CI |
| [60931bce](https://github.com/Wei-Shaw/sub2api/commit/60931bce3114eb1d240e5bd1eb54546181216056) | fix(grok): 展示模型目录诊断并保留编辑草稿 | `529c2de54`，已适配；待当前 head CI |
| [ae5705da](https://github.com/Wei-Shaw/sub2api/commit/ae5705da1d0b13c351d52329067eb2763e986f8f) | fix(grok): 同账号刷新不再卡住目录同步 | `fda5e9f6e`，已适配；待当前 head CI |
| [2db7ce3d](https://github.com/Wei-Shaw/sub2api/commit/2db7ce3d25a9b2dc8285bbc1fbf51185c1211f43) | fix(grok): 隔离过期目录同步并在切模式后刷新诊断 | `d6fda14b6`，已适配；待当前 head CI |
| [02db748c](https://github.com/Wei-Shaw/sub2api/commit/02db748cd5a8767378c5f174eedf83367655e3bb) | fix(grok): 保留受管目录快照并阻止非法时间与敏感失败日志 | `777bb5766`，已适配；待当前 head CI |
| [3f1c8f36](https://github.com/Wei-Shaw/sub2api/commit/3f1c8f36ce8c2cd378015830a90e89970b9a9ea7) | chore: update Sponsors | fork 品牌映射：保留 fork Sponsors/branding |
| [0d374b25](https://github.com/Wei-Shaw/sub2api/commit/0d374b25494d7b49915b51af15f9a7eff99a87b7) | fix(grok): 精简目录诊断并按需展开详情 | `736fbf63b`，已适配；待当前 head CI |
| [c68d3123](https://github.com/Wei-Shaw/sub2api/commit/c68d312372999f696ddca35bb6f394061878602d) | fix(grok): 未知目录详情改为历史标题 | `e890184f0`，已适配；待当前 head CI |
| [32c00d6e](https://github.com/Wei-Shaw/sub2api/commit/32c00d6e72135eb367d80d014ff1a929deffae4e) | fix(grok): 诊断文案不再读取继承属性 | `13845f3a7`，已适配；待当前 head CI |
| [21b5b49d](https://github.com/Wei-Shaw/sub2api/commit/21b5b49d0a5a673c051d16ad807d6b57be3694d5) | fix(grok): 统一目录同步入口并保留模式隔离 | `1ffa48c6f`，已适配；待当前 head CI |
| [7f18239b](https://github.com/Wei-Shaw/sub2api/commit/7f18239be2c09bd7c08663bd4d55ee1f73d42571) | fix(grok): 对齐模型编辑布局与响应式展示 | `423f5d4b0`，已适配；待当前 head CI |
| [9534f1c0](https://github.com/Wei-Shaw/sub2api/commit/9534f1c089d67fbfac8784489579791bd50df0e4) | fix(grok): 提高删除图标对比度并补齐同步按钮 Label in Name | `faa48115d`，已适配；待当前 head CI |
| [3ce09c8a](https://github.com/Wei-Shaw/sub2api/commit/3ce09c8aff085ce42538d539d515742d8650e6f0) | fix(billing): keep tokens for channel per-request image tiers | `8a400e177`，已适配；待当前 head CI |
| [dfc13ce7](https://github.com/Wei-Shaw/sub2api/commit/dfc13ce7f9ff9300c524d21d871f672c91d235c2) | fix(grok): 修复模型目录审计相关 lint 错误 | `139b6a18c`，已适配；待当前 head CI |
| [cde6ddfd](https://github.com/Wei-Shaw/sub2api/commit/cde6ddfd8f41e1cf173ed83ac42e79f1e8af34c8) | fix(images): reject oversized multipart uploads instead of truncating them | `08ed8f1dd`，已适配；待当前 head CI |
| [3e18e78a](https://github.com/Wei-Shaw/sub2api/commit/3e18e78ab1d06d3cce1fc019d8969acceafaaf5e) | fix(usage): DeepSeek prompt_cache_hit_tokens 计为缓存读取 | `19af237c6`，已适配；待当前 head CI |
| [88d9c43b](https://github.com/Wei-Shaw/sub2api/commit/88d9c43b3e9a30d9e7f74a369114eff0f5674379) | fix(admin): 管理员保护规则返回 400 而不是 500 | `d50fcedc7`，已适配；待当前 head CI |
| [14a5fe30](https://github.com/Wei-Shaw/sub2api/commit/14a5fe3050616e35b5f123614f57e0e6adcaf069) | fix(antigravity): reject malformed non-streaming tool calls | `9067ac10e`，已适配；待当前 head CI |
| [34236674](https://github.com/Wei-Shaw/sub2api/commit/34236674223877d265bb5dbc071277d04ded0d34) | feat(openai): support GPT-6.1 Terra | `ef9cfa6f1`，已适配；待当前 head CI |
| [d0afece1](https://github.com/Wei-Shaw/sub2api/commit/d0afece15f9f16da32193816c2cc50dbb04938e5) | fix(model-plaza): prefer group model pricing over image fallback tiers | `6cbd7a1c3`，已适配；待当前 head CI |
| [a2074123](https://github.com/Wei-Shaw/sub2api/commit/a207412375448f85e816b1b56139813d04d831d8) | fix(gemini): 绑定显式缓存的请求在池模式上游网关账号上按配置同账号重试 | `c8b77507a`，已适配；待当前 head CI |
| [b2b1bcf2](https://github.com/Wei-Shaw/sub2api/commit/b2b1bcf260b584dc4d268a8ae36bec31a2de2130) | fix(models): 模型列表刷新进入 singleflight 后复查缓存，避免并发冷启动重复请求上游 | `9a574f65a`，已适配；待当前 head CI |
| [ba5b2187](https://github.com/Wei-Shaw/sub2api/commit/ba5b21879430fdbe3ab87dc791b09bcdece5e4a7) | fix(gateway): 修复 OpenCode 转发 DeepSeek 的角色兼容 | `a36876ccb`，已适配；待当前 head CI |
| [7bce2cfb](https://github.com/Wei-Shaw/sub2api/commit/7bce2cfb45d8cf6e8ea7f9cf2710c9febb8a2f51) | fix(claude): match JavaScript UTF-16 billing fingerprint semantics | `a06eaaffb`，已适配；待当前 head CI |
| [9eba6173](https://github.com/Wei-Shaw/sub2api/commit/9eba617367469e3e37fa4aa131275499ea2f8c14) | fix(channels): preserve web_search_emulation platform dimensions on update | `69ae1f904`，已适配；待当前 head CI |
| [1352e899](https://github.com/Wei-Shaw/sub2api/commit/1352e899ecd418ecf0b81a5151a9c992c953d945) | test(channels): use checked type assertions in web search merge tests | `89a861867`，已适配；待当前 head CI |
| [0a2a29b3](https://github.com/Wei-Shaw/sub2api/commit/0a2a29b32e6724714c69822296ccd05a6b86bfdd) | chore(deps): bump golang.org/x/net to v0.62.0 (GO-2026-6860) | 树级等价：backend/go.mod 已为 golang.org/x/net v0.62.0，来源 production a0b4c39f2 |
| [de44e3a0](https://github.com/Wei-Shaw/sub2api/commit/de44e3a0bfd63db10fc733a71d6577f5070a6b8e) | fix(gemini): 分组账号都声明模型映射时，/v1beta/models 列表与详情按声明的模型应答 | `6da45b7e2`，已适配；待当前 head CI |
| [31564b2e](https://github.com/Wei-Shaw/sub2api/commit/31564b2e86ccb6f4de348a21eb9e47b366f326ba) | fix(gemini): 刷新 Gemini 默认模型列表，移除已停用的 2.x 模型 | `dc9a24014`，已适配；待当前 head CI |

31564b2e 的 shared image classifier 剩余 hunk 在 2a9f02c57 补齐。
per-image 5 个依赖 leaf 在 8a400e177 合并适配，所有原 SHA trailer 保留；3ce09c8a 的 Token 保留行为在原 fork 已存在，新路径仍保留并纳入回归。

## 21 merge 处置

20 个 remerge-diff 为空，不重复导入对应 leaf。3f4e13aa 仅解决 DeepSeek 注释与 CacheStoragePricePerTokenHour 邻接冲突；本 fork 2f56bd419 保留该函数及相同 Pro 策略注释，树级覆盖。
固定范围价卡 JSON diff 只新增 GPT-6.1 Terra，无 Grok 价格表增量。Grok 21 leaf 均已列于上表，不再作为后续排除项。

## 验证边界

ff9739b14 的精确完整 CI 与独立 RED/GREEN/race/PostgreSQL/前端证据归档保留；新增源码必须由当前 head CI 验收。
工作流明确 checkout 并断言 head，保留原测试并新增价格/Grok 行为 RED、计费优先级、CAS/事务、Grok UI 用例。
本机不 Go 编译/动态测试、Docker、全量前端 build/lint/typecheck；仅静态/gofmt/离线 Python/Node。
不改历史账本、结算、支付实现，不生产 SSH/DB/付费/真实邮件；不 merge/release/deploy，不自动关闭 issue。

Agent：Codex，唯一 writer，GPT-6（更精确运行型号未提供），默认推理强度（未手动设置）；无子 agent。
