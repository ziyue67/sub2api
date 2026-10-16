# v0.2.16 cherry-pick 记录

基线：`3c54ff2a32b681a846162af2da68063978f7b1e1`（production，版本 2.10.7）。上游：Wei-Shaw/sub2api v0.2.16，`01842576b3d5c1e2545b5a99ad555d36e28ed306`。

按 v0.2.15..v0.2.16 的 first-parent 合并顺序提取实际提交，使用 cherry-pick -x，不引入重复 merge commit。补齐 Gemini 缓存依赖的 4 个在途预留提交：43f88c99a、52c123e6f、652a7d74f、b886fb8ed。

跳过：上游 VERSION 同步（保持 fork 2.10.7）；69d6d106f（fork #404 已支持缺失与自定义平台及排序）；f7f1112cd、daa791574（fork 已使用 Vue 3.5.43、source-map-js 1.2.2、SheetJS 0.20.3）。

冲突适配保留用户禁用模型过滤、观察员管理 DTO、API Key 并发控制、账号售价倍率、模型定价配置、协议 profile 路由、DeepSeek compaction/SSE、票据身份隔离与质量观测。Ent 按实际 fork schema 重新生成，修复新增 i18n 字段后的字段索引。Wire 再生成核对后保留原有 SetNewAPIGroupReader 接线，避免生成器移除现有手动接线。

新增迁移 242_group_i18n、243_gemini_cached_contents、244_gemini_cached_contents_channel_usage、245_gemini_cached_contents_mutation_lease 均为新增表/列/索引。迁移以完整文件名识别，与 fork 同数字前缀的迁移并存。回退旧二进制不删除迁移；旧版本无法管理新建缓存，应先停止缓存创建并处理活跃缓存。

新增 billing.inflight_reservation 默认开启；Redis 预留失败仍为上游 fail-open 语义。Gemini 缓存最长 TTL 默认 86400 秒；自建网关需显式开启账号支持开关。此 PR 不改变 292 开关、fork 版本或更新仓库地址。

## UI 证据

本地真实 GroupI18nField 组件，虚构数据；验证默认内容与英文译文切换。截图不代表完整后台页面或真实接口验收。其余 UI 由 Vitest 验证，未逐页截图。

[默认内容](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/upstream-v0.2.16/group-default.png)
[英文译文](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/upstream-v0.2.16/group-english.png)

## 提交清单

每个提交正文保留原始 upstream SHA。

```text
fccd703677ebac81472955703de0b1588a6bc731 test(web): use shipped SVG asset in embedded frontend tests
d88399e9cbc31d9652751db528166e3a6a8644dc fix(frontend): show full account group names
14f287c308aba6771812f6c96e38620d53e75720 fix(gemini): preserve authorization URL error messages
88d8cf6ddc5a60cca9986be82d963d38d642f25a fix(monitor): refresh cached availability on automatic reload
879146269d50f4dcdbae27babfd645f30c0b3970 fix(groups): reject nonpositive rate multiplier edits
a19df797513c9158e0ed062018cea82af1847a57 fix(select): preserve keyboard input during IME composition
7aa53e6a04234b4951b3e6649234f172b822eb9d fix(ops): initialize custom time ranges in local time
a4ef9e2a1ee835b99f6bd4be0520bd2061a116aa feat(ops): show account names in request details table
b32324a53bf809a7cc093badeb30d27cf68b5b6c fix(payment): calculate QR countdowns from elapsed time
106757de4e7da2c20fe5402c65c5063ff2acefb1 fix(ui): refresh countdown when tab becomes visible
283cae0430b533e43c64c2d20374f1a686fffb71 fix(risk-control): remove deleted groups from moderation scope
d296b497faf921117fbf74bd265f59d0fa18ff96 fix(openai): only inject the Codex image generation bridge for OpenAI accounts
002d0267ad41b39f18a2001264da6be488b4adaa fix(groups): ignore stale RPM override loads
4eed55621ed0b0d369f6b6627c7fd42627d5a3bd fix(billing): 取消 GPT-6.1 Sol 订阅长上下文附加费
4161e10a5bf9ff5210beb0ea1156ab947031683f fix(codex): raise GPT-6.1 Sol generated context default to 500k
e3f8d937d027532c98fa33b5284f3431659749fa feat(groups): 分组名称与描述支持多语言
622c8ab9e32ae8582b1ac3d8b0c86f8ae2ce981f fix(groups): 广场 Vary 保留 Origin，分组译文语言代码统一大小写
ae100b7d9cc8a1d19feac9aa88395a1c19590c07 feat(gateway): adapt Claude Haiku 5.5
f7b682c5b49479d9b12541a1553fae7ad7985f81 fix(gateway): wire Claude Haiku 5.5 into Responses bridge
21d41ec136aff178c4c8a0d8283a83371db2d762 fix(haiku): 修正采样边界、默认模型配置与 lint
d5ae7844e84808307b6b2af483ee55f234b7d6f0 test(frontend): 将模型白名单回归用例纳入 CI
3a3beb7d56e0fa9f9b2e800a7c83c62c079eeeee fix(gemini): 引用缓存不存在的 403 不再停用账号并换号
d6353fd058f833a82b847d3bf941cc6d59a84544 fix(billing): reserve in-flight balance to prevent concurrent overdraft
94a53cf061a9cef2a1afba63fad30fa746a94470 fix(billing): hold inflight reservation until billing lands; price estimate like billing; wire remaining endpoints
e27b93383d437d0b75a06ede7414c8823c835136 fix(billing): estimate unpriced aliases via billing fallback models; don't renew expired reservations
890d7176cbfb13a8cfff0c2628b3ddd090ade428 fix(billing): read account-level mappings from scheduler snapshot only when unpriced
301e77957f54fd436d70c5d9f7596e343ca28eac feat(gemini): 支持显式上下文缓存（AI Studio / Vertex / 自建网关上游）
c0b29b323fc3be02a5de0d5feafa41bacda19f21 fix(ops): Gemini 原生路由的网关本地错误按错误类型归类
fa0c37ad90040d42904a9085a0b4c317aa64be99 ui(gemini): Vertex 显示名改为 Agent Platform (Vertex)
03f8e441c3ea991d067156eabfb44a34c1eb6782 feat(gemini): Vertex 账号支持 API Key 认证
b44d5ef7bdc5f1994198b4e5ef121f4e8eab0cff fix(gemini): 第三方上游 API Key 与 Vertex 不再套用 AI Studio 等级和模拟配额
fdd6c015e192d32a91edb159477b9e2695137f77 feat(gemini): 适配 AI Studio AQ. 格式 Key
0d5edd3c2ed15923365b2c7dd62a4bd33358babc feat(gemini): Vertex 服务账号支持同步上游模型列表
5b132c6b8ce9a85635decdf80fe04b264ba619bb fix(account): 同步上游模型失败时显示后端返回的错误原因
37969abcfd091232fa695eda372e0bcf6fed8545 fix(gemini): 显式缓存错误透传收窄到上游网关账号，延长沿用创建时计费口径
e442534843a9055f81ac3e5d6a8253ef3c262834 feat(gemini): 显式缓存存储价内置官方价并按 Pro / Flash 系列兜底
a897763f93b29ac67838c26237e8e42876f6002e fix(gemini): 3.6 Flash 兜底价改为官方优惠价
4d7e7950660bc0bc58bab97d35edfecb92b53a16 fix(gemini): 模型列表与模型详情接口支持 Agent Platform (Vertex) 账号
463a8c30e3b0329e6ee8fbb74e732e7b92452f38 fix(gemini): 显式缓存修改串行化、失败回滚，确定金额费用按 Key / 订阅预留
dfe33187259381c14a73512184ce244c04075021 fix(gemini): 显式缓存恢复流程区分账号已删除与暂不支持缓存，上游必已过期后作废意图
dfe02f9afca34126f2d7bd26736331ba721de5f4 feat(account): 临时不可调度规则新增作用范围 scope 选项
66ebb3b648ddc86b747fea78d4e07df7d6e7637a test(account): 补充临时不可调度作用范围单元测试
835e913e948adaa887c9eaa5acfd9bc0149fcc60 fix(account): 显式模型级 401 规则在网关故障转移入口生效
30cd7686754f953e879d2493a2a14bd84560aad9 fix(account): 计数、Bedrock 与重试耗尽入口把上游模型交给错误处理
13259e5b86cc4df3653234d0bbf72f6998e6923a fix(antigravity): isolate image model rate limits
887727969dc7ff2530907a6f2b90e591ce164aa4 fix(gemini): preserve images from tool results
672ae16790c3b405157c4b7e66d8a894d9ad9b39 fix(gemini): satisfy test lint
dd54ea1b4d57cc02b6edf8855f8269b7d1133c32 fix(quota): recognize international coding endpoints
01b541169601a799426cc01b91f47980658aaad3 fix(gateway): retry rejected structured input through chat endpoint
4758b5ab2842f885d1e095c3d4b55af9d72ad2bb fix(gateway): preserve Responses semantics in chat fallback
e4627b285aff386c29996e446840ae376d8b617b fix(gateway): guard structured-input fallback against unsafe retries
```
