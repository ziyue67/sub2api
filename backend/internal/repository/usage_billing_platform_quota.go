package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func incrementUsageBillingPlatformQuota(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	if !domain.IsConcretePlatform(cmd.PlatformQuotaPlatform) || cmd.PlatformUsageAt.IsZero() {
		return service.ErrUsageBillingRequestConflict
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM user_platform_quotas
		WHERE user_id=$1 AND platform=$2 AND deleted_at IS NULL
		AND (daily_limit_usd IS NOT NULL OR weekly_limit_usd IS NOT NULL OR monthly_limit_usd IS NOT NULL) FOR UPDATE`,
		cmd.UserID, cmd.PlatformQuotaPlatform).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // absent quota is unlimited; never create limits implicitly
	}
	if err != nil {
		return err
	}
	at := cmd.PlatformUsageAt
	starts, err := effectivePlatformQuotaWindows(ctx, tx, id, at)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_platform_quota_totals
		(quota_id,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start,legacy_daily_anchor,legacy_weekly_anchor,legacy_monthly_anchor)
		VALUES($1,CASE WHEN $3::timestamptz>$6::timestamptz THEN 0 ELSE $2::numeric END,CASE WHEN $4::timestamptz>$6::timestamptz THEN 0 ELSE $2::numeric END,
		 CASE WHEN $5::timestamptz>$6::timestamptz THEN 0 ELSE $2::numeric END,$3,$4,$5,$3,$4,$5)
		ON CONFLICT(quota_id) DO UPDATE SET
		daily_usage_usd=CASE WHEN usage_platform_quota_totals.daily_window_start>EXCLUDED.daily_window_start THEN usage_platform_quota_totals.daily_usage_usd
		 WHEN usage_platform_quota_totals.daily_window_start<EXCLUDED.daily_window_start THEN EXCLUDED.daily_usage_usd
		 ELSE usage_platform_quota_totals.daily_usage_usd+EXCLUDED.daily_usage_usd END,
		weekly_usage_usd=CASE WHEN usage_platform_quota_totals.weekly_window_start>EXCLUDED.weekly_window_start THEN usage_platform_quota_totals.weekly_usage_usd
		 WHEN usage_platform_quota_totals.weekly_window_start<EXCLUDED.weekly_window_start THEN EXCLUDED.weekly_usage_usd
		 ELSE usage_platform_quota_totals.weekly_usage_usd+EXCLUDED.weekly_usage_usd END,
		monthly_usage_usd=CASE WHEN usage_platform_quota_totals.monthly_window_start>EXCLUDED.monthly_window_start THEN usage_platform_quota_totals.monthly_usage_usd
		 WHEN usage_platform_quota_totals.monthly_window_start<EXCLUDED.monthly_window_start THEN EXCLUDED.monthly_usage_usd
		 ELSE usage_platform_quota_totals.monthly_usage_usd+EXCLUDED.monthly_usage_usd END,
		daily_window_start=GREATEST(usage_platform_quota_totals.daily_window_start,EXCLUDED.daily_window_start),
		weekly_window_start=GREATEST(usage_platform_quota_totals.weekly_window_start,EXCLUDED.weekly_window_start),
		monthly_window_start=GREATEST(usage_platform_quota_totals.monthly_window_start,EXCLUDED.monthly_window_start)`,
		id, cmd.PlatformQuotaCost, starts[0], starts[1], starts[2], at)
	return err
}

// Caller holds the baseline quota row lock, which also fences projection and
// explicit reset writers. A window_start can be a natural boundary OR an exact
// administrative reset cutoff. Never truncate that cutoff: doing so would count
// late pre-reset events again. New events in that day/week share the cutoff;
// the next natural boundary advances it. Monthly writers share the effective
// rolling anchor instead of independently inventing a newer one.
func effectivePlatformQuotaWindows(ctx context.Context, tx *sql.Tx, id int64, at time.Time) ([3]time.Time, error) {
	var current [3]sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT daily_window_start,weekly_window_start,monthly_window_start
		FROM usage_platform_quota_effective WHERE id=$1`, id).Scan(&current[0], &current[1], &current[2])
	desired := [3]time.Time{timezone.StartOfDay(at), timezone.StartOfWeek(at), at}
	if err != nil {
		return desired, err
	}
	for i := 0; i < 2; i++ {
		if current[i].Valid && current[i].Time.After(desired[i]) {
			desired[i] = current[i].Time
		}
	}
	if current[2].Valid && at.Before(current[2].Time.Add(30*24*time.Hour)) {
		desired[2] = current[2].Time
	}
	return desired, nil
}
