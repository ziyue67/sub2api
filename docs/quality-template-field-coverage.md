# 检测模板完整配置覆盖

本项接续 PR #587 的孤立隔离修复；原已通过 head `94937267b2acb32bbbd44a7aae13218709c466fc` 的源码与证据保留。

## 实际漏点

当前 `copyQualityRuleConfig` 和后端 `reusableQualityConfig` 原本就列有 `action`、`remove_group_ids`、`auto_restore`，正常完整模板不会被 DTO 白名单删掉这三个字段。真正缺口：旧模板缺少 action 时没有归一为安全观察策略；被动模板只有保存和读取，没有编辑入口；批量可选字段没有结果保留数和 5xx 触发；非当前 action 的隐藏处罚字段能留在复用草稿；指定组只校验正整数/重复，未验证存活组。现有测试也没有覆盖四种策略完整的 save-read-apply-save-read 链路。

修复保留完整字段复制，统一安全缺省和隐藏字段清理，增加被动模板编辑及批量显式字段，补真实 PostgreSQL/HTTP 和实际 Vue 表单矩阵。不把字段本来已存在误写成 DTO 漏字段。

## 字段矩阵

| 业务配置 | 持久化字段 | 被动模板保存/编辑/读回 | 新建/单编辑应用与复制 | 批量选择 |
| --- | --- | --- | --- | --- |
| 主模型、多个检测模型 | `model_id`, `pelican_config.model_ids` | 保留，独立数组 | 整套替换 | `model` |
| 检测模式 | `question_kind`（candy/random_reasoning/state_probe） | 保留 | 整套替换；探针仅一个模型、并行 1 | `test` |
| 检测通道 | `test_channel`（account/bps） | 保留 | BPS 通道仅观察 | `test`，依赖 action/restore 必须确认 |
| 题目、参考答案 | `prompt`, `quality.expected_answer` | 保留；随机题/探针清空不适用内容 | 保留，随机生成结果不复制 | `test` |
| 推理强度、每模型样本并行数 | `reasoning_effort`, `parallel_count` | 保留 | 保留，按现有 1–8 / 每轮最多 100 样本校验 | `test` |
| 判题 provider、分组、模型、提示词 | `quality.judge.kind/group_id/model_id/prompt` | 保留 LLM/Jev；探针不判题 | 不复制 provider 凭据；Jev 只引用全局审计配置 | `test` |
| 时间调度 | `cron_expression` | 保留 | 不复制 next/last run | `schedule` |
| 结果保留次数 | `max_results` | 保留，1–200 且容纳本轮 | 保留 | 新增 `retention`，未勾选保留目标自己的值 |
| 上游 5xx 触发 | `quality.trigger_on_upstream_5xx` | 保留 | 保留现有运行语义 | 新增 `trigger`，未勾选不变 |
| 答错后只记录 | `action=observe_only` | 保留，清除隐藏处罚及自动恢复 | 无 action 的旧模板默认此策略 | `action`；依赖清理需显式确认 |
| 移出指定组 | `action=remove_groups`, `remove_group_ids` | 保存/读回指定组 | 只影响各目标账号现有成员关系交集 | `action` |
| 按模型隔离，全部降智才移组 | `action=isolate_models`, `remove_group_ids` | 保存/读回指定组 | 严格现有 ownership/scope guard | `action`，模型范围属 `model` |
| 关闭账号调度保留分组 | `action=disable_scheduling` | 保留；隐藏移组字段清空 | 保留 | `action` |
| 原有模型 cooldown / 恢复并发 | `remove_models`, `recovery_concurrency` | 仅 remove_models 保存有效值 | 不带入别的 action | `action` |
| 自动恢复 | `auto_restore` | 保留；observe_only 强制 false | 使用各 action 现有语义，不制造 passing | `restore` |
| 原有 BPS 策略与恢复选项 | `quality.bps.*` | 仅 enable_bps 保留白名单业务字段 | 保留现有探针/BPS 依赖 | `action` 与 `restore` 原有拆分 |
| 启用默认值 | `enabled` | 只是未来规则默认，被动模板自身不运行 | 仅规则保存后按正常调度生效 | `enabled` |

`rule_name` 是目标规则显示名，应用时保留目标草稿名；模板单独使用 `name`。`auto_recover` 是原连通性测试账号恢复开关，质量恢复使用 `quality.auto_restore`，不从源复制 `auto_recover`。

超时不是当前质量规则表单或 `PelicanTestConfig` 中的可编辑字段：运行器固定轮次预算 10 分钟；LLM 判题总体 90 秒、单次路由 30 秒，Jev 使用全局审计配置且有自身总预算。模板不添加无法生效的 timeout 字段，也不复制全局审计地址/密钥。次数指现有每模型样本数和历史保留次数，没有另一个可编辑的“失败累计 N 次”适用于这四种普通 action；BPS 自有 failure/pass threshold 仍保留。

## 边界与真实入口

被动模板 `account-quality-presets`：保存/编辑/读取不创建规则、不执行检测、不处罚账号。应用只改变草稿，规则保存是独立请求；有明确的编辑模板按钮，仍使用完整配置表单。规则卡片复制与模板应用支持新建、单编辑、批量，批量字段勾选不因换模板被重置或扩充；依赖变化沿用显式复用确认。

动态分发模板 `account-quality-templates`：保持创建时覆盖当前匹配账户、以后同步、编辑推送已链接规则的原行为，不把它改成被动预设。保存配置时使用业务字段白名单；原有隔离冲突仍由目标规则 repository guard 拒绝，不绕过显式恢复。没有新增通用删 Extra/ownership 接口。

目标组必须为存活组；观察者不能保存/编辑/读取全局被动模板，普通规则引用的组也必须在授权范围内。跨账号复用允许引用目标尚未加入的有效组，运行移组只处理该账号成员关系交集，绝不复制模板源账号成员关系。失效组在模板保存和规则实际保存时重新校验。

不复制 `account_id`、凭据、账号状态、ownership、隔离标记、lease/running_until、next/last run、历史、计数、自动生成题目或结果诊断。指定组 ID 是配置目标，不是源账号身份，也不是线上修改授权。

## 验收证据

独立工作流记录精确 head、基线与测试输入 SHA256，同一组回归先对旧源码验证 RED，再恢复当前源码执行 PostgreSQL/HTTP 和前端 GREEN。实际覆盖四种 action 的被动模板保存/编辑/读回/应用/规则保存读回，四种 action 的分发同步及隔离 guard，以及新建/单编辑/批量 × 模板/账号源的实际 Vue 入口矩阵。浏览器使用真实页面与虚构离线夹具，记录被动模板写入次数、规则写入 0 和运行调用 0。

最终执行状态与证据以 PR #587 的两项独立验收清单及对应精确 head 的 CI artifacts 为准，不能用孤立隔离的旧 head 代替本项验收。本机不运行 Go 动态/编译、Docker、全量前端 build/lint/typecheck；没有生产、付费请求、合并、发布或部署。
