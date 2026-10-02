# Prism OAuth P1 文本验证（2026-10-01）

## 已确认

现有 OpenAI OAuth 账号经 Sub2API 管理员账号测试入口调用 Prism，能够返回 `gpt-5.6-sol` 文本终态。测试未在生产机编译应用，使用 Mac 交叉构建的 Linux 二进制和已下载的 Chromium。

新建空白项目的糖果题结果：

| 项目 | 实际结果 |
| --- | --- |
| 入口 | `POST /api/v1/admin/accounts/<test-account>/test` |
| 请求模型 | `gpt-5.6-sol` |
| Prism start 观察及门控模型 | `gpt-5.6-sol` / `medium` |
| start 次数 | 1 |
| status 次数 | 18 |
| HTTP / SSE | 200；`test_start` → `content` → `test_complete(success=true)` |
| 客户端总耗时 | 101.38 秒 |
| 最终答案 | 21 颗：9 颗圆形糖、12 颗五角星形糖 |
| 答案 SHA-256 | `8e0f859a0ba8dbceba962be855382559073bcdb3b2ab49004e101d76624b29b6` |
| 终态后待决文件 | 已清除 |

这次采用空白项目，未把答案或证明写入提示。此前重复使用项目的一次糖果测试也返回 21，但其回答引用了项目中已有结论，不能作为独立推理验证。另有两次空白项目准备失败（sandbox 启动和模型控件初始化）均发生于 start 之前，未计作模型测试成功。

实测主二进制源码为 `4482c64f8510f7bcc2ebc03b6984b2e0f989e621`，适配器文件 SHA-256 为 `d6d0c5df408158c5919a05ec96475b26e1dbbb7242b6f9b049ad3b46da2b71aa`。后续补充目录 fsync、严格终态识别和缩小请求拦截范围后，重新执行了离线测试，没有为了覆盖无关改动重发模型请求。

## 证据边界

- 单题结果不能证明完整模型能力、长期稳定性或“不降智”。
- 本次生产验证覆盖 OAuth → 管理员账号测试 → Prism；公共 `/v1/responses` 的凭据传递、终态校验、JSON/SSE 和未知 usage 处理由本地 HTTP mock 回归覆盖，尚未做 Codex CLI 端到端实测。
- 不支持 function/custom 工具、namespace、`additional_tools`、工具结果回灌、`previous_response_id` 和取消恢复。没有宣称完成 Issue #256 的全部验收。
- 当前没有上游权威 usage，不能作为按 token 收费通道。试验结束后测试账号开关关闭，避免把该账号的普通请求切到纯文本通道。
- `gpt-6-astra` 明确返回 422，没有作为 Sol 的别名或 fallback。

界面截图位于 `docs/screenshots/prism-oauth/`，为真实组件的本地 mock 预览，不含真实账号资料。
