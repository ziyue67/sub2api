package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"time"
)

// RegionalBudgetScope serializes against authoritative monetary rows. Existing
// counters reset under the same locks while unresolved allocations remain
// occupied across windows. Budget cutover must stop legacy
// asynchronous writers before this scope is installed.
type RegionalBudgetScope struct{}

func (s RegionalBudgetScope) Reserve(ctx context.Context, tx *sql.Tx, id string, user, key, account int64, maximum regional.Amount, admitted time.Time) error {
	amount, err := ledger.USD(maximum)
	if err != nil {
		return err
	}
	if maximum <= 0 || user <= 0 || key <= 0 || account <= 0 {
		return ledger.ErrConflict
	}
	var oldUser, oldKey, oldAccount, oldMaximum int64
	err = tx.QueryRowContext(ctx, "SELECT user_id,api_key_id,account_id,maximum FROM regional_budget_allocations WHERE allocation_id=$1 FOR UPDATE", id).Scan(&oldUser, &oldKey, &oldAccount, &oldMaximum)
	if err == nil {
		if oldUser != user || oldKey != key || oldAccount != account || oldMaximum != int64(maximum) {
			return ledger.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var unattributed bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_budget_allocations WHERE state='held' AND group_id IS NULL)").Scan(&unattributed); err != nil {
		return err
	}
	if unattributed {
		return errors.New("legacy held allocation requires billing attribution drain")
	}
	if err = regionalKeyWindows(ctx, tx, key, admitted); err != nil {
		return err
	}
	var group int64
	var enough bool
	var lockedKey int64
	if err = tx.QueryRowContext(ctx, "SELECT id FROM api_keys WHERE id=$1 FOR UPDATE", key).Scan(&lockedKey); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(group_id,0), quota=0 OR quota_used+$2::numeric+COALESCE((SELECT SUM(maximum-spent)::numeric/1000000000 FROM regional_budget_allocations WHERE api_key_id=$1 AND state='held'),0)<=quota FROM api_keys WHERE id=$1 AND user_id=$3 AND status='active' AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp()) FOR UPDATE`, key, amount, user).Scan(&group, &enough)
	if err != nil {
		return err
	}
	if !enough {
		return ledger.ErrExhausted
	}
	for _, pair := range [][2]string{{"rate_limit_5h", "usage_5h"}, {"rate_limit_1d", "usage_1d"}, {"rate_limit_7d", "usage_7d"}} {
		query := "SELECT " + pair[0] + "=0 OR " + pair[1] + "+$2::numeric+COALESCE((SELECT SUM(maximum-spent)::numeric/1000000000 FROM regional_budget_allocations WHERE api_key_id=$1 AND state='held'),0)<=" + pair[0] + " FROM api_keys WHERE id=$1"
		if err = tx.QueryRowContext(ctx, query, key, amount).Scan(&enough); err != nil {
			return err
		}
		if !enough {
			return ledger.ErrExhausted
		}
	}
	var platform, accountType string
	var accountUnitRates bool
	if err = tx.QueryRowContext(ctx, `SELECT platform,type,COALESCE(rate_multiplier,1)=1 AND COALESCE(group_rate_multiplier,1)=1 FROM accounts WHERE id=$1 AND status='active' AND schedulable AND deleted_at IS NULL FOR UPDATE`, account).Scan(&platform, &accountType, &accountUnitRates); err != nil {
		return err
	}
	if !accountUnitRates {
		return errors.New("account multiplier requires an immutable scoped price contract")
	}
	if err = regionalAccountWindows(ctx, tx, account, admitted); err != nil {
		return err
	}
	if group <= 0 {
		return errors.New("funded request requires concrete group")
	}
	var subscriptionType string
	var unitRates bool
	if err = tx.QueryRowContext(ctx, `SELECT subscription_type,rate_multiplier=1 AND peak_rate_multiplier=1 AND NOT EXISTS(SELECT 1 FROM user_group_rate_multipliers WHERE user_id=$2 AND group_id=$1 AND rate_multiplier IS NOT NULL AND rate_multiplier<>1) FROM groups WHERE id=$1 AND deleted_at IS NULL AND status='active' AND platform=$3 FOR SHARE`, group, user, platform).Scan(&subscriptionType, &unitRates); err != nil {
		return err
	}
	if !unitRates {
		return errors.New("non-unit rate requires an immutable scoped price contract")
	}
	var member bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM account_groups WHERE account_id=$1 AND group_id=$2)", account, group).Scan(&member); err != nil {
		return err
	}
	if !member {
		return ledger.ErrConflict
	}
	var subscription int64
	if subscriptionType == "subscription" {
		if err = tx.QueryRowContext(ctx, "SELECT id FROM user_subscriptions WHERE user_id=$1 AND group_id=$2 AND status='active' AND deleted_at IS NULL AND starts_at<=clock_timestamp() AND expires_at>clock_timestamp() FOR UPDATE", user, group).Scan(&subscription); err != nil {
			return err
		}
		if err = regionalSubscriptionWindows(ctx, tx, subscription, admitted); err != nil {
			return err
		}
		for _, window := range []string{"daily", "weekly", "monthly"} {
			query := "SELECT g." + window + "_limit_usd IS NULL OR g." + window + "_limit_usd<=0 OR us." + window + "_usage_usd+$2::numeric+COALESCE((SELECT SUM(maximum-spent)::numeric/1000000000 FROM regional_budget_allocations WHERE subscription_id=$1 AND state='held'),0)<=g." + window + "_limit_usd FROM user_subscriptions us JOIN groups g ON g.id=us.group_id WHERE us.id=$1"
			if err = tx.QueryRowContext(ctx, query, subscription, amount).Scan(&enough); err != nil {
				return err
			}
			if !enough {
				return ledger.ErrExhausted
			}
		}
	}
	if accountType == "apikey" || accountType == "bedrock" {
		for _, pair := range [][2]string{{"quota_limit", "quota_used"}, {"quota_daily_limit", "quota_daily_used"}, {"quota_weekly_limit", "quota_weekly_used"}} {
			query := "SELECT COALESCE((extra->>'" + pair[0] + "')::numeric,0)<=0 OR COALESCE((extra->>'" + pair[1] + "')::numeric,0)+$2::numeric+COALESCE((SELECT SUM(maximum-spent)::numeric/1000000000 FROM regional_budget_allocations WHERE account_id=$1 AND state='held'),0)<=(extra->>'" + pair[0] + "')::numeric FROM accounts WHERE id=$1"
			if err = tx.QueryRowContext(ctx, query, account, amount).Scan(&enough); err != nil {
				return err
			}
			if !enough {
				return ledger.ErrExhausted
			}
		}
	}
	if err = regionalPlatformWindows(ctx, tx, user, platform, admitted); err != nil {
		return err
	}
	var quotaID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM user_platform_quotas WHERE user_id=$1 AND platform=$2 AND deleted_at IS NULL FOR UPDATE", user, platform).Scan(&quotaID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		for _, window := range []string{"daily", "weekly", "monthly"} {
			query := "SELECT " + window + "_limit_usd IS NULL OR " + window + "_usage_usd+$2::numeric+COALESCE((SELECT SUM(maximum-spent)::numeric/1000000000 FROM regional_budget_allocations WHERE user_id=$3 AND platform=$4 AND state='held'),0)<=" + window + "_limit_usd FROM usage_platform_quota_effective WHERE id=$1"
			if err = tx.QueryRowContext(ctx, query, quotaID, amount, user, platform).Scan(&enough); err != nil {
				return err
			}
			if !enough {
				return ledger.ErrExhausted
			}
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO regional_budget_allocations(allocation_id,user_id,api_key_id,account_id,subscription_id,platform,maximum,group_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", id, user, key, account, subscription, platform, int64(maximum), group)
	return err
}

func (s RegionalBudgetScope) Charge(ctx context.Context, tx *sql.Tx, id string, e ledger.Event) error {
	var user, key, account, subscription, maximum, spent int64
	var platform, state string
	if err := tx.QueryRowContext(ctx, "SELECT user_id,api_key_id,account_id,subscription_id,platform,maximum,spent,state FROM regional_budget_allocations WHERE allocation_id=$1 FOR UPDATE", id).Scan(&user, &key, &account, &subscription, &platform, &maximum, &spent, &state); err != nil {
		return err
	}
	if state != "held" || e.Usage.UserID != user || e.Usage.APIKeyID != key || e.Usage.AccountID != account || e.Usage.Charge > regional.Amount(maximum-spent) {
		return ledger.ErrConflict
	}
	admitted := e.Usage.AdmittedAt
	if err := regionalKeyWindows(ctx, tx, key, admitted); err != nil {
		return err
	}
	if err := regionalAccountWindows(ctx, tx, account, admitted); err != nil {
		return err
	}
	if err := regionalSubscriptionWindows(ctx, tx, subscription, admitted); err != nil {
		return err
	}
	if err := regionalPlatformWindows(ctx, tx, user, platform, admitted); err != nil {
		return err
	}
	amount, err := ledger.USD(e.Usage.Charge)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE api_keys SET quota_used=quota_used+$2::numeric,usage_5h=usage_5h+CASE WHEN window_5h_start<=$3 THEN $2::numeric ELSE 0 END,usage_1d=usage_1d+CASE WHEN window_1d_start<=$3 THEN $2::numeric ELSE 0 END,usage_7d=usage_7d+CASE WHEN window_7d_start<=$3 THEN $2::numeric ELSE 0 END,updated_at=NOW() WHERE id=$1", key, amount, admitted); err != nil {
		return err
	}
	if subscription != 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+CASE WHEN daily_window_start<=$3 THEN $2::numeric ELSE 0 END,weekly_usage_usd=weekly_usage_usd+CASE WHEN weekly_window_start<=$3 THEN $2::numeric ELSE 0 END,monthly_usage_usd=monthly_usage_usd+CASE WHEN monthly_window_start<=$3 THEN $2::numeric ELSE 0 END,updated_at=NOW() WHERE id=$1", subscription, amount, admitted); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=COALESCE(extra,'{}'::jsonb)||jsonb_build_object('quota_used',COALESCE((extra->>'quota_used')::numeric,0)+$2::numeric,'quota_daily_used',COALESCE((extra->>'quota_daily_used')::numeric,0)+CASE WHEN (extra->>'quota_daily_start')::timestamptz<=$3 THEN $2::numeric ELSE 0 END,'quota_weekly_used',COALESCE((extra->>'quota_weekly_used')::numeric,0)+CASE WHEN (extra->>'quota_weekly_start')::timestamptz<=$3 THEN $2::numeric ELSE 0 END),updated_at=NOW() WHERE id=$1 AND type IN ('apikey','bedrock')`, account, amount, admitted); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE user_platform_quotas SET daily_usage_usd=daily_usage_usd+CASE WHEN daily_window_start<=$4 THEN $3::numeric ELSE 0 END,weekly_usage_usd=weekly_usage_usd+CASE WHEN weekly_window_start<=$4 THEN $3::numeric ELSE 0 END,monthly_usage_usd=monthly_usage_usd+CASE WHEN monthly_window_start<=$4 THEN $3::numeric ELSE 0 END,updated_at=NOW() WHERE user_id=$1 AND platform=$2 AND deleted_at IS NULL", user, platform, amount, admitted); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE regional_budget_allocations SET spent=spent+$2 WHERE allocation_id=$1", id, int64(e.Usage.Charge))
	return err
}
func (s RegionalBudgetScope) Close(ctx context.Context, tx *sql.Tx, id string, spent regional.Amount) error {
	result, err := tx.ExecContext(ctx, "UPDATE regional_budget_allocations SET state='closed' WHERE allocation_id=$1 AND spent=$2", id, int64(spent))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ledger.ErrConflict
	}
	return nil
}
