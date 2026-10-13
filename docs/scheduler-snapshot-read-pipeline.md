# 调度候选读取的 Redis 往返优化

GetSnapshot 仍按请求读取共享 Redis，没有增加本地 TTL 或改变缓存 schema。
ready/active 两个 GET 使用一个 pipeline，账号元数据与最近使用时间的 MGET
每个分块使用一个 pipeline。每条 MGET 的 key 数仍受 snapshot_mget_chunk_size 限制，
不会把所有分块一次性排入 pipeline。

有 N 个候选、分块大小 C 时，应用层串行执行次数从
3 + 2 × ceil(N/C) 降到 2 + ceil(N/C)。N <= C 时从 5 到 3。
这是调用结构变化，不是线上延迟实测；实际耗时依赖 Redis RTT、候选大小和负载。

最近使用时间和账号元数据仍每次读取，因此不依赖 bucket 版本变化。
ready 未设置或不是 1 时仍返回 miss；ready/active 的错误类型仍保留 GET 语义。
不改变并发准入、API Key 认证、计费、OAuth 刷新和流式重试。

回归测试：backend/internal/repository/scheduler_snapshot_pipeline_unit_test.go。
运行：go test -tags=unit ./internal/repository -run 'TestScheduler(SnapshotPipeline|Cache)' -count=1。
关闭或回滚不需要数据迁移。周期本地快照、独立地区数据库同步不属于本修复。
