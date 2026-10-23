# OpenAI HTTP 上传期限与首输出总预算

两个开关默认都是 0（关闭），不改变现有部署的时间限制：

```yaml
gateway:
  openai_upload_timeout_seconds: 0
  openai_pre_output_budget_seconds: 0
```

`openai_upload_timeout_seconds`（0–1800 秒）从同一个入站请求第一次启用保护的 HTTP 提交开始计时；覆盖连接、TLS、上传和内部代理重试，成功 `WroteRequest` 或收到提前响应头后停止本次上传等待。它不是收到响应头后的生成期限。后续账号重试共用原截止点，等待重试的时间也消耗预算。

账号可通过 `accounts.extra.openai_upload_timeout_seconds` 覆盖上传值，接受整数 0–1800（显式 0 禁用该账号的上传保护；无效值回退全局）。该字段暂无后台 UI，需通过已有账号 API 设置并保留其他 extra 字段；修改配置或账号须按自己的上线流程执行。初始可只对有证据的慢供应商启用，不能直接向全部账号套用一个阈值。

`openai_pre_output_budget_seconds`（0–3600 秒）从第一次进入 Forward 开始计时，跨同账号重试和换号共享，HTTP SSE 首个完整输出事件后解除。metadata、心跳和错误不算成功输出。它不限制首个输出之后的正常长 SSE，也不取代流内 idle timeout。非流式请求在完整响应前受该预算限制。

如果已耗尽预算，下一次 HTTP 提交在网络调用前结束；不会因为换号重新获得全部时间。两个开关均不主动取消原本允许继续 drain 用量的下游断开；既有 API Key capacity owner 的终止信号仍然有效。

期限到达后使用 `upload_timeout` 或 `pre_output_budget` 记录脱敏 Ops 事件；HTTP 尚未提交时返回 504，已提交 SSE 时遵循既有单终态错误协议。上传可能已经部分发送，不能假定上游没有执行，所以这类错误**不转换成自动 failover**，不惩罚整账号为永久认证失败。调用方仍需按自身幂等语义恢复。

覆盖范围：通过 `doOpenAIUpstream` 的 native HTTP 与自动透传、代理/TLS 内部重试；保护安装在取消 detachment 之后，并保持到响应体关闭。插件应尊重传入 context，插件若没有 HTTP trace，上传保护持续到返回响应头；不承诺其内部阶段与原生传输完全相同。WS、BPS、Prism 和独立 sidecar 路由的专属期限不由本改动统一设置；禁止将这份配置描述为全通道总超时。

灰度时按模型、请求大小、reasoning effort 比较实际超时数、completed/failed、每入站请求尝试数及费用。未知执行结果不自动补发，不以 SLA 排除掩盖失败。回滚配置置 0；无数据库迁移，二进制回滚后新配置不再生效。

CI 的 `openai_http_phase_guard_test.go` 使用虚拟时间和离线上游验证：取消 detachment 后的上传超时、跨账号共享截止点、上传成功/提前响应、首输出后长流、账户 override，以及过期预算不再次提交。未进行真实付费上游测试。
