-- 跨实例共享的「DB 兜底窗口」（Redis 预留后端故障期间的全局状态）。
--
-- 为什么需要它：Redis 预留与 DB 兜底预留是两本独立的账。若实例 A 连不上 Redis 而切到
-- DB 账本，实例 B 仍连得上、继续用 Redis 账本，同一份额度就会被两本账各算一遍 ——
-- 两边各自都没超过上限，合起来却超了（少算 = 超额放行）。
--
-- 协议：任一实例因 Redis 故障切到 DB 账本时，把 fallback_until 抬到
-- max(现值, now + 预留 TTL)。所有实例在每笔预检前探测该窗口（进程内缓存 1s），
-- 窗口生效期间**统一**走 DB 账本，绝不与 Redis 账本混用；窗口至少覆盖"切换时刻
-- 之前已存在的 Redis 预留"的最长存活时间，等它们全部自然过期后才重新信任 Redis。
-- 活跃期间每次 DB 预留/续期都会把窗口顺延到该笔凭据的过期时刻，因此只要还有
-- DB 账本上的活跃预留，窗口就不会提前结束。
CREATE TABLE IF NOT EXISTS billing_reservation_fallback_state (
    singleton      BOOLEAN PRIMARY KEY DEFAULT TRUE,
    fallback_until TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_billing_reservation_fallback_singleton CHECK (singleton)
);

INSERT INTO billing_reservation_fallback_state (singleton)
VALUES (TRUE)
ON CONFLICT (singleton) DO NOTHING;
