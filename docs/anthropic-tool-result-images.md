# Anthropic 工具结果图片兼容

基线：ziyue67/sub2api main，7d1585fa93014515d5a7de151e9bfc9aa6a5740c。

## 用途与开启方式

部分聚合上游会把 `tool_result.content` 整体当作文本处理，使图片 Base64 进入文本预估。针对需要适配的 Anthropic API Key 账号，在创建或编辑账号时打开 **工具结果图片兼容**。持久化字段为 `extra.anthropic_tool_result_images=true`，默认关闭。不要将账号 ID 写进代码；生产可只对需要的账号（例如 29413）开启。

## 行为

- 只处理 user 消息中带非空字符串 tool_use_id 的 tool_result，其 content 必须是数组。
- 将其中 source.type=base64 / url 且数据非空的 image 移到同一消息最后一个 tool_result 之后；原位置及图片前保留关联标记。
- 保留工具文本、is_error、未知字段和大整数；已有顶层图片、普通工具字符串、其他角色及无有效来源的图片不改。
- 工具结果的 cache_control 移到其最后一张图片；图片已有标记时增加结束文本承接，避免覆盖。已有强制 1h 处理在主 Messages/count_tokens 路径中随后执行。
- 不修改调用方的原始 body；重复处理幂等。调度器每次尝试的 body clone 隔离账号重试。
- 覆盖 Messages 普通/透传、count_tokens 普通/透传，以及 Chat Completions / Responses 转 Anthropic 的请求体。

关闭开关即可恢复原行为，无数据库迁移。开启会改变请求字节及图片对应的缓存前缀，首次请求可能重新创建缓存；关联文字也会增加少量文本 Token。

## 范围边界

这是 Sub2API 的出站请求兼容处理，不是 NewAPI 源码移植，不修改 Usage、客户费率、失败时补扣或余额。NewAPI 的 GetTokenCountMeta/relaykit 不属于本仓库，递归媒体预估与缺失 Usage 的结算仍需在实际执行这些逻辑的上游修正。

历史“百万输入、零输出”没有原始事件序列，本补丁不是该历史故障唯一原因的证明。六次直连诊断仅验证成功路径，未触发缺失 Usage 的兜底。

## 验证

`go test ./internal/service -run TestToolImages -count=1` 覆盖结构、缓存冲突、1h 强制、普通/透传真实出站 body、count_tokens、未知字段、幂等、多消息及重试隔离。转发边界测试在接入前失败、接入后通过。

前端编辑账号测试覆盖开关保存、重新加载及关闭；同时运行 i18n、TypeScript 和目标文件 ESLint。全量回归的环境限制与结果在工作区 validation-results.md 中记录。
