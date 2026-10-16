//go:build integration

package repository

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRegionalBudgetWindowResetRetainsUnknownAndAttributesLateUsage(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "window-" + uuid.NewString(), Platform: service.PlatformOpenAI, RateMultiplier: 1})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user, GroupID: &group.ID, Key: "sk-mock-" + uuid.NewString(), Name: "window"})
	account := mustCreateAccount(t, client, &service.Account{Name: "window-" + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI, Status: "active", Schedulable: true})
	_, err := integrationDB.Exec("INSERT INTO account_groups(account_id,group_id)VALUES($1,$2)", account.ID, group.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec("INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd) VALUES($1,'openai',7)", user)
	require.NoError(t, err)
	_, err = integrationDB.Exec("UPDATE accounts SET extra=jsonb_build_object('quota_daily_limit',7) WHERE id=$1", account.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM regional_budget_allocations WHERE user_id=$1", user) })
	scope := RegionalBudgetScope{}
	oldTime := time.Now().Add(-48 * time.Hour)
	now := time.Now()
	first := "window-old-" + uuid.NewString()
	second := "window-new-" + uuid.NewString()
	withTx := func(f func(*sql.Tx) error) error {
		tx, e := integrationDB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer func() { _ = tx.Rollback() }()
		if e = f(tx); e != nil {
			return e
		}
		return tx.Commit()
	}
	require.NoError(t, withTx(func(tx *sql.Tx) error {
		return scope.Reserve(ctx, tx, first, user, key.ID, account.ID, 4_000_000_000, oldTime)
	}))
	_, err = integrationDB.Exec("UPDATE user_platform_quotas SET daily_usage_usd=6 WHERE user_id=$1", user)
	require.NoError(t, err)
	_, err = integrationDB.Exec("UPDATE accounts SET extra=extra||jsonb_build_object('quota_daily_used',6) WHERE id=$1", account.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec("UPDATE api_keys SET usage_5h=6,rate_limit_5h=7 WHERE id=$1", key.ID)
	require.NoError(t, err)
	require.ErrorIs(t, withTx(func(tx *sql.Tx) error {
		return scope.Reserve(ctx, tx, second, user, key.ID, account.ID, 4_000_000_000, now)
	}), ledger.ErrExhausted)
	require.NoError(t, withTx(func(tx *sql.Tx) error {
		return scope.Reserve(ctx, tx, second, user, key.ID, account.ID, 3_000_000_000, now)
	}))
	event := ledger.Event{Usage: regional.UsageEvent{UserID: user, APIKeyID: key.ID, AccountID: account.ID, AdmittedAt: oldTime, Charge: 2_000_000_000}}
	require.NoError(t, withTx(func(tx *sql.Tx) error { return scope.Charge(ctx, tx, first, event) }))
	var daily, monthly string
	var start time.Time
	require.NoError(t, integrationDB.QueryRow("SELECT daily_usage_usd::text,monthly_usage_usd::text,daily_window_start FROM user_platform_quotas WHERE user_id=$1", user).Scan(&daily, &monthly, &start))
	require.Equal(t, "0.0000000000", daily)
	require.Equal(t, "2.0000000000", monthly)
	require.True(t, start.Equal(timezone.StartOfDay(now)))
	var remaining int64
	require.NoError(t, integrationDB.QueryRow("SELECT SUM(maximum-spent) FROM regional_budget_allocations WHERE user_id=$1 AND state='held'", user).Scan(&remaining))
	require.Equal(t, int64(5_000_000_000), remaining)
	var accountDaily string
	require.NoError(t, integrationDB.QueryRow("SELECT extra->>'quota_daily_used' FROM accounts WHERE id=$1", account.ID).Scan(&accountDaily))
	require.Equal(t, "0", accountDaily)
}

func TestRegionalBudgetWindowsResetEveryPeriodAndFixedAccountPolicy(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "all-windows-" + uuid.NewString(), Platform: service.PlatformOpenAI, RateMultiplier: 1})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user, GroupID: &group.ID, Key: "sk-mock-" + uuid.NewString(), Name: "windows"})
	account := mustCreateAccount(t, client, &service.Account{Name: "fixed-" + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI})
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-90 * 24 * time.Hour)
	_, err := integrationDB.Exec("UPDATE api_keys SET usage_5h=6,usage_1d=6,usage_7d=6,window_5h_start=$2,window_1d_start=$2,window_7d_start=$2 WHERE id=$1", key.ID, old)
	require.NoError(t, err)
	_, err = integrationDB.Exec("UPDATE accounts SET extra=jsonb_build_object('quota_daily_limit',7,'quota_daily_used',6,'quota_daily_start',$2::text,'quota_daily_reset_mode','fixed','quota_daily_reset_at',$2::text,'quota_weekly_limit',7,'quota_weekly_used',6,'quota_weekly_start',$2::text,'quota_weekly_reset_mode','fixed','quota_weekly_reset_at',$2::text,'quota_reset_timezone','Asia/Tokyo') WHERE id=$1", account.ID, old.Format(time.RFC3339Nano))
	require.NoError(t, err)
	_, err = integrationDB.Exec("INSERT INTO user_platform_quotas(user_id,platform,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start)VALUES($1,'openai',6,6,6,$2,$2,$2)", user, old)
	require.NoError(t, err)
	var subscription int64
	require.NoError(t, integrationDB.QueryRow("INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at,status,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start)VALUES($1,$2,$3,$4,'active',6,6,6,$5,$5,$5)RETURNING id", user, group.ID, now.Add(-120*24*time.Hour), now.Add(120*24*time.Hour), old).Scan(&subscription))
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, regionalKeyWindows(ctx, tx, key.ID, now))
	require.NoError(t, regionalAccountWindows(ctx, tx, account.ID, now))
	require.NoError(t, regionalSubscriptionWindows(ctx, tx, subscription, now))
	require.NoError(t, regionalPlatformWindows(ctx, tx, user, "openai", now))
	require.NoError(t, tx.Commit())
	var zero bool
	require.NoError(t, integrationDB.QueryRow("SELECT usage_5h+usage_1d+usage_7d=0 FROM api_keys WHERE id=$1", key.ID).Scan(&zero))
	require.True(t, zero)
	require.NoError(t, integrationDB.QueryRow("SELECT daily_usage_usd+weekly_usage_usd+monthly_usage_usd=0 FROM user_platform_quotas WHERE user_id=$1", user).Scan(&zero))
	require.True(t, zero)
	require.NoError(t, integrationDB.QueryRow("SELECT daily_usage_usd+weekly_usage_usd+monthly_usage_usd=0 FROM user_subscriptions WHERE id=$1", subscription).Scan(&zero))
	require.True(t, zero)
	var daily, weekly time.Time
	require.NoError(t, integrationDB.QueryRow("SELECT (extra->>'quota_daily_reset_at')::timestamptz,(extra->>'quota_weekly_reset_at')::timestamptz,(extra->>'quota_daily_used')::numeric+(extra->>'quota_weekly_used')::numeric=0 FROM accounts WHERE id=$1", account.ID).Scan(&daily, &weekly, &zero))
	require.True(t, zero)
	require.True(t, daily.After(now))
	require.True(t, weekly.After(now))
}
