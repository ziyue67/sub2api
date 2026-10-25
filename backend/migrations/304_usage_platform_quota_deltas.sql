-- New request charges have an independent durable projection. The legacy
-- Redis flusher may still finish old snapshots into user_platform_quotas;
-- it cannot overwrite these new increments. No historical backfill/import.
CREATE TABLE usage_platform_quota_totals (
    quota_id BIGINT PRIMARY KEY REFERENCES user_platform_quotas(id) ON DELETE CASCADE,
    daily_usage_usd NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK(daily_usage_usd>=0),
    weekly_usage_usd NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK(weekly_usage_usd>=0),
    monthly_usage_usd NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK(monthly_usage_usd>=0),
    legacy_daily_anchor TIMESTAMPTZ NOT NULL,
    legacy_weekly_anchor TIMESTAMPTZ NOT NULL,
    legacy_monthly_anchor TIMESTAMPTZ NOT NULL,
    daily_window_start TIMESTAMPTZ NOT NULL,
    weekly_window_start TIMESTAMPTZ NOT NULL,
    monthly_window_start TIMESTAMPTZ NOT NULL
);

CREATE VIEW usage_platform_quota_effective AS
SELECT q.id,q.user_id,q.platform,q.daily_limit_usd,q.weekly_limit_usd,q.monthly_limit_usd,q.deleted_at,
    CASE WHEN p.quota_id IS NULL THEN q.daily_usage_usd
         ELSE CASE WHEN COALESCE(q.daily_window_start,p.legacy_daily_anchor) IS NULL OR COALESCE(q.daily_window_start,p.legacy_daily_anchor)>=p.daily_window_start THEN q.daily_usage_usd ELSE 0 END
            + CASE WHEN COALESCE(q.daily_window_start,p.legacy_daily_anchor) IS NULL OR p.daily_window_start>=COALESCE(q.daily_window_start,p.legacy_daily_anchor) THEN p.daily_usage_usd ELSE 0 END END AS daily_usage_usd,
    CASE WHEN p.quota_id IS NULL THEN q.weekly_usage_usd
         ELSE CASE WHEN COALESCE(q.weekly_window_start,p.legacy_weekly_anchor) IS NULL OR COALESCE(q.weekly_window_start,p.legacy_weekly_anchor)>=p.weekly_window_start THEN q.weekly_usage_usd ELSE 0 END
            + CASE WHEN COALESCE(q.weekly_window_start,p.legacy_weekly_anchor) IS NULL OR p.weekly_window_start>=COALESCE(q.weekly_window_start,p.legacy_weekly_anchor) THEN p.weekly_usage_usd ELSE 0 END END AS weekly_usage_usd,
    CASE WHEN p.quota_id IS NULL THEN q.monthly_usage_usd
         ELSE CASE WHEN COALESCE(q.monthly_window_start,p.legacy_monthly_anchor) IS NULL OR COALESCE(q.monthly_window_start,p.legacy_monthly_anchor)>=p.monthly_window_start THEN q.monthly_usage_usd ELSE 0 END
            + CASE WHEN COALESCE(q.monthly_window_start,p.legacy_monthly_anchor) IS NULL OR p.monthly_window_start>=COALESCE(q.monthly_window_start,p.legacy_monthly_anchor) THEN p.monthly_usage_usd ELSE 0 END END AS monthly_usage_usd,
    GREATEST(COALESCE(q.daily_window_start,p.legacy_daily_anchor),p.daily_window_start) AS daily_window_start,
    GREATEST(COALESCE(q.weekly_window_start,p.legacy_weekly_anchor),p.weekly_window_start) AS weekly_window_start,
    GREATEST(COALESCE(q.monthly_window_start,p.legacy_monthly_anchor),p.monthly_window_start) AS monthly_window_start
FROM user_platform_quotas q LEFT JOIN usage_platform_quota_totals p ON p.quota_id=q.id;
