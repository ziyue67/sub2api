# BPS 上游无进展超时

`gateway.excel_bps_stream_data_interval_timeout` 默认 120 秒；环境变量为 `GATEWAY_EXCEL_BPS_STREAM_DATA_INTERVAL_TIMEOUT`。0 禁用，启用时允许 30–300 秒。只控制 BPS Responses 响应体，独立于其他上游的流超时和连接池空闲期。

普通生成、加密 reasoning 400 恢复、工具修正与图片 compaction 的原始响应读取均受保护。计时累计等待上游 Read 的时间；读到非注释数据恢复预算。SSE 注释/空行和给客户端发送的 keepalive 不续期。工具校验、结构化输出和客户端消费之间的本地缓冲不消耗上游读取预算。

超时关闭响应体，释放其资源，走已有失败/用量路径；保留已经报告的用量，不重放已接受的生成。客户端取消仍保持原始取消原因，终态仍立即结束。建连/响应头超时、连接池保留期和 BPS 代理选择没有改变。

旧配置缺省时会启用 120 秒保护；长静默业务可在允许范围调整，设 0 恢复原行为。无数据库迁移。回滚二进制后该字段不再生效。

验证：静默流、注释心跳、碎片数据、下游暂停、客户端取消、关闭开关、完整 Forward 不重放并保留用量的本地 mock；config、basispoints、service 测试。未发真实模型请求。

行为参考：[Nonary/ghcp_proxy f89ccfc](https://github.com/Nonary/ghcp_proxy/commit/f89ccfcea4497c4e60be891119a389a3cb68b050)。独立 Go 实现，关联 #357 第 3 项。
