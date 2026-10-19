# OpenAI 生图账号筛选与图片权限拒绝

分组开启 `allow_image_generation` 只授予调用者生图权限，不代表分组内每个上游账号都支持生图。对于显式生图的 `/v1/responses` 请求，调度同时检查外层文本模型和生图工具的图片模型是否在账号 `credentials.model_mapping` 白名单内。

例如，账号只配置 `gpt-6-astra` 时，普通文本请求仍可使用它；请求包含 `{"type":"image_generation","model":"gpt-image-2"}` 时，该账号被排除。工具省略 `model` 时按现有默认值 `gpt-image-2` 检查。支持生图的账号需要在现有模型配置中声明实际可用的图片模型；通配符规则与普通模型一致，空映射继续表示允许所有模型。`/v1/images/*` 保留其请求模型及自定义模型别名，不强制替换成默认工具模型。

被动声明 Codex `image_gen` namespace 不会单独触发生图筛选；自动 hosted image-generation 桥也不会向图片模型不在白名单或处于图片冷却的账号注入生图工具。HTTP、WebSocket 首轮、后续回合和发送前的最新账号检查共用图片模型约束，后续文本回合不会继承上一轮的生图意图。

上游明确返回 HTTP 403，且结构化错误为 `error.type=permission_error`、`error.message=Image generation is not enabled for this group` 时，仅写入 30 分钟的 `openai:image_generation` 冷却。该响应不进入账号通用 403 累计，不整号冷却或停用；请求继续使用原有的失败换号预算。普通 403、账号失效、内容审查拒绝和非结构化文本不按此规则豁免。账号自定义错误码策略排除 403 时不写图片冷却，也不会回落到账号级处罚。

没有新增配置或数据库迁移。已有图片冷却记录继续生效；本次不会自动恢复过去已因该报错停用的账号。恢复需由管理员核对实际停用原因后单独执行。回滚旧二进制不会删除图片冷却记录，旧版本仍可能把再次出现的图片权限 403 计入账号停用阈值。
