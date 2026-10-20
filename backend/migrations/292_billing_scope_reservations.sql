-- 通用 scope 的「在途预留」DB 兜底表（仅在 Redis 预留后端不可用时使用）。
--
-- 为什么需要它：272 的 billing_balance_reservations 以 user_id 为主键、带 users 外键，
-- 只覆盖余额模式（scope = 纯数字 userID）。订阅（sub:<user>:<group>）、user×platform
-- 配额（upq:<user>:<platform>）、API Key 额度（apikey:<id>）这些 scope 不是数字，
-- Redis 故障期在旧实现里只能 fail-open —— 并发超额在故障期重新出现。
--
-- 语义与 Redis 侧完全一致：按 scope 聚合、只有「未过期预留总额 + 本笔 <= 预算」才登记，
-- 行按 (scope, request_id) 唯一（同一笔重复预留只刷新过期时间），过期行由 TTL 自愈
-- （每次预留/续期顺手清理）。
--
-- 并发控制用 pg_advisory_xact_lock(hashtextextended(scope, 0))：同一 scope 的准入在
-- 事务内串行化，因此「读 SUM → 判定 → 写入」是原子的。与余额模式不同，这里不去锁
-- users 行 —— 这类 scope 的结算路径并不持有 users 行锁，用独立锁空间反而避免了任何
-- 锁序耦合。
CREATE TABLE IF NOT EXISTS billing_scope_reservations (
    scope      TEXT NOT NULL,
    request_id TEXT NOT NULL,
    amount     DECIMAL(20,8) NOT NULL CHECK (amount > 0),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope, request_id)
);

CREATE INDEX IF NOT EXISTS idx_billing_scope_reservations_scope_expires
    ON billing_scope_reservations(scope, expires_at);
