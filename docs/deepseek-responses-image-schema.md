# DeepSeek Responses 图片协议

v0.2.18 适配默认按当前协议只发送 `input_image.image_url`，不会同时发送第二份 `url`。原生 DeepSeek 和 OpenAI 平台映射到 DeepSeek API 的账号均适用。

仍使用要求 `input_image.url` 的旧协议上游时，通过管理员账号 API 在该账号 `credentials` 中显式设置 `deepseek_responses_image_field` 为 `url`。此分支只发送 `url`。设置为 `image_url` 或省略该字段使用默认协议。更新账号时保留其余现有 credentials，不能仅用该字段替换整份凭据。

该选项在首次请求发出前选择协议，不根据真实上游错误自动重放请求，不改变 DeepSeek compaction/SSE 或其他平台协议。新老协议均保留只有 file_id、没有可用 URL 的图片项。
