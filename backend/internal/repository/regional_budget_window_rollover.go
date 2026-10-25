package repository

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strings"
	"time"
)

// Advance settled counters only. Outstanding allocations live in a separate
// table and remain occupied through every reset, including unknown started work.
// Use admission time for event attribution; an old event cannot rewind a newer
// window or charge it a second time. Missing legacy starts preserve their usage.
func advanceRegionalWindow(ctx context.Context, tx *sql.Tx, table, usage, start string, id int64, desired time.Time) error {
	_, err := tx.ExecContext(ctx, "UPDATE "+table+" SET "+usage+"=CASE WHEN "+start+" IS NOT NULL AND "+start+"<$2 THEN 0 ELSE "+usage+" END,"+start+"=CASE WHEN "+start+" IS NULL OR "+start+"<$2 THEN $2 ELSE "+start+" END WHERE id=$1", id, desired)
	return err
}
func regionalKeyWindows(ctx context.Context, tx *sql.Tx, id int64, admitted time.Time) error {
	var starts [3]sql.NullTime
	if err := tx.QueryRowContext(ctx, "SELECT window_5h_start,window_1d_start,window_7d_start FROM api_keys WHERE id=$1 FOR UPDATE", id).Scan(&starts[0], &starts[1], &starts[2]); err != nil {
		return err
	}
	for i, p := range []struct {
		usage, start string
		period       time.Duration
	}{{"usage_5h", "window_5h_start", 5 * time.Hour}, {"usage_1d", "window_1d_start", 24 * time.Hour}, {"usage_7d", "window_7d_start", 7 * 24 * time.Hour}} {
		desired := admitted
		if i > 0 {
			if err := tx.QueryRowContext(ctx, "SELECT date_trunc('day',$1::timestamptz)", admitted).Scan(&desired); err != nil {
				return err
			}
		}
		if starts[i].Valid && admitted.Before(starts[i].Time.Add(p.period)) {
			continue
		}
		if err := advanceRegionalWindow(ctx, tx, "api_keys", p.usage, p.start, id, desired); err != nil {
			return err
		}
	}
	return nil
}
func regionalSubscriptionWindows(ctx context.Context, tx *sql.Tx, id int64, admitted time.Time) error {
	if id == 0 {
		return nil
	}
	sub := service.UserSubscription{}
	if err := tx.QueryRowContext(ctx, "SELECT starts_at,expires_at,daily_window_start,weekly_window_start,monthly_window_start FROM user_subscriptions WHERE id=$1 FOR UPDATE", id).Scan(&sub.StartsAt, &sub.ExpiresAt, &sub.DailyWindowStart, &sub.WeeklyWindowStart, &sub.MonthlyWindowStart); err != nil {
		return err
	}
	desired := sub.BillingWindowStarts(admitted)
	for i, w := range []string{"daily", "weekly", "monthly"} {
		if err := advanceRegionalWindow(ctx, tx, "user_subscriptions", w+"_usage_usd", w+"_window_start", id, desired[i]); err != nil {
			return err
		}
	}
	return nil
}
func regionalPlatformWindows(ctx context.Context, tx *sql.Tx, user int64, platform string, admitted time.Time) error {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM user_platform_quotas WHERE user_id=$1 AND platform=$2 AND deleted_at IS NULL FOR UPDATE", user, platform).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	// Ordinary delivery may already have advanced its durable projection while
	// the legacy baseline still has an expired anchor. Join that effective
	// window before admission checks; inventing a newer baseline would hide
	// current ordinary usage. The shared helper also retains exact reset cutoffs.
	desired, err := effectivePlatformQuotaWindows(ctx, tx, id, admitted)
	if err != nil {
		return err
	}
	for i, w := range []string{"daily", "weekly", "monthly"} {
		if err := advanceRegionalWindow(ctx, tx, "user_platform_quotas", w+"_usage_usd", w+"_window_start", id, desired[i]); err != nil {
			return err
		}
	}
	return nil
}
func regionalAccountWindows(ctx context.Context, tx *sql.Tx, id int64, admitted time.Time) error {
	// Reuse the account repository's fixed/rolling reset policy, replacing its
	// wall clock with authenticated admission time. Future/current starts persist.
	for _, w := range []struct{ name, expired, next string }{{"daily", dailyExpiredExpr, nextDailyResetAtExpr}, {"weekly", weeklyExpiredExpr, nextWeeklyResetAtExpr}} {
		replace := func(expr string) string { return strings.ReplaceAll(expr, "NOW()", "$2::timestamptz") }
		expired := replace(w.expired)
		current := "(extra->>'quota_" + w.name + "_start')::timestamptz"
		advance := "(" + current + " IS NULL OR (" + current + "<=$2 AND " + expired + "))"
		query := "UPDATE accounts SET extra=COALESCE(extra,'{}'::jsonb)||jsonb_build_object('quota_" + w.name + "_used',CASE WHEN " + current + " IS NOT NULL AND " + advance + " THEN 0 ELSE COALESCE((extra->>'quota_" + w.name + "_used')::numeric,0) END,'quota_" + w.name + "_start',CASE WHEN " + advance + " THEN " + replace(nowUTC) + " ELSE extra->>'quota_" + w.name + "_start' END)||CASE WHEN " + advance + " AND " + replace(w.next) + " IS NOT NULL THEN jsonb_build_object('quota_" + w.name + "_reset_at'," + replace(w.next) + ") ELSE '{}'::jsonb END WHERE id=$1 AND type IN ('apikey','bedrock') AND COALESCE((extra->>'quota_" + w.name + "_limit')::numeric,0)>0"
		if _, err := tx.ExecContext(ctx, query, id, admitted); err != nil {
			return err
		}
	}
	return nil
}
