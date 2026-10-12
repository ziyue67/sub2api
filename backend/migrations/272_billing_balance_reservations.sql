-- 后付费计费的「在途预留」DB 侧兜底表。
--
-- 为什么需要它：正常路径的在途预留记在 Redis（billing:reserved:*，带 TTL 自愈）。
-- Redis 不可用时的既有行为是 **fail-open** —— 请求在完全没有预留的情况下被放行，
-- 并发准入护栏（balance - Σ预留 >= reserve）整层失效，突发时产生坏账。
-- 本表给该降级路径提供与 Redis 同语义的兜底：准入时原子登记一笔带过期时间的预留，
-- 只有「未过期预留总额 + 本笔 <= 预算」才允许落库。
--
-- 语义与 Redis 实现严格对齐：
--   * expires_at 对应预留 TTL；长请求由心跳续期（UPDATE 时要求 expires_at > NOW()，
--     已过期的行不会被"复活"）；
--   * 每次准入先清理本用户的过期行，等价于 Redis 的 TTL 自愈 —— 这是必须的：
--     缺少它，进程崩溃/结算任务丢失会让预留永久钉住余额（PR#10 修过的
--     "余额远高于封底却被持续 403 且无法自愈"故障会原样复现）；
--   * 主键 (user_id, request_id) 保证幂等：同一笔请求重复预留只刷新过期时间。
--
-- 为什么不用 users 上的一个 reserved 列：列没有"按笔过期"的概念，释放责任一旦丢失
-- 就永久占用额度。按笔登记 + 过期时间才等价于 Redis 的 TTL 语义。
CREATE TABLE IF NOT EXISTS billing_balance_reservations (
    user_id    BIGINT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id TEXT          NOT NULL,
    amount     DECIMAL(20,8) NOT NULL CHECK (amount > 0),
    expires_at TIMESTAMPTZ   NOT NULL,
    created_at TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, request_id)
);

-- 准入路径按 (user_id, expires_at) 求和未过期预留；归还按主键删除。
CREATE INDEX IF NOT EXISTS idx_billing_balance_reservations_user_expires
    ON billing_balance_reservations(user_id, expires_at);
