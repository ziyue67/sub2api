# 固定 tag 定价与计量适配记录

本次用户已授权所有命名源码增量；无待审批前置条件。值描述固定 tag 源码，不代表重新查询当前官方价。

| 源提交 | 旧行为 → 本 PR 行为 | fork 冲突适配 |
| --- | --- | --- |
| 34236674 | 新增 gpt-6.1-terra：每 MTok 输入 $1、输出 $5、cache write $1.25、cache read $0.05；priority 2×、flex/batch 半价；>272k 输入 2×/输出 1.5×；1.05M context；none/max、工具路由 Responses | 独立新卡，不覆盖 5.6 Terra；保留显式缓存单价、自定义价卡、unknown-model fail-closed 及 fork 定价优先级 |
| 5db2d695 | Pro 2026-09-14 后转 Flash → Pro 始终保留 $0.66/$1.98/$0.022（输入/输出/cache read）每 MTok，Flash 仍 $0.15/$0.60/$0.003 | 保留 PricingAt 峰谷和分组/渠道自定义价；不回写任何历史金额/账本 |
| 3e18e78a | OpenAI usage 补 prompt_cache_hit_tokens 为缓存读取 fallback | 嵌套 cached_tokens 包括 0 优先，保持既有 authoritative-zero 语义 |
| fd998129/1487fa51/cb295924/aa1a1b11/3ce09c8a | 新增默认 false 的 ImageCountBilling、API/cache/Ent/UI/迁移；按实际返回图片数，未知数开关开启时 1 | 保留显式 group/channel token 卡优先；开关仅门控图片兜底及图片端点；关闭时 JSON/OAuth 未知数仍 N、SSE 仍 0。token 不丢，上下文/尺寸档继续可用。迁移 311，auth snapshot 31 保留 local epoch |
| 917dbdc7 | fileData/file_data 图片计数与 inline 等价，每 part 一次、累计 chunk 取 max | 已通过既有 ForwardResult.ImageCount 路径接入，实际 data/URI/MIME 校验保留，无 URI 下载 |
| 31564b2e | shared gemini-3.1-flash-lite-image 分类补齐 | 与默认列表同时生效，无旧模型 whitelist 删除 |
| d0afece1 | 模型广场显式分组模型图片/按次价优先于兜底图片档 | 保留 token context/time 与 official pricing 展示；未改实收算法优先级 |
| 21 个 Grok leaf | 响应模型审计、目录/parser/scope/CAS/同步、managed extra 和诊断 UI 全部导入；本范围无新 Grok 价卡 | 响应模型冲突/未知/更高价保持既有保护；不以审计数据覆盖收费模型，不改团队限流/SSRF/凭据保护 |

3ce09c8a 的原 channel per-request Tokens 行为在 ff9739b14 已等价；新 per-image 分支重放该保护并保留来源。
所有增量只影响本 tag 命名源码，不包含历史账本补写、结算或支付重写/生产操作。验证与来源见 [完整审计](upstream-v0.2.19-audit.md)。
