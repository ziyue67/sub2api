//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageBillingPlatformDBFailureRecoveryAndLegacySnapshotIsolation(t *testing.T) {
	ctx := context.Background()
	repo, cmd, usage, client := durableBillingFixture(t)
	db := repo.db
	cmd.PlatformQuotaPlatform, cmd.PlatformQuotaCost, cmd.PlatformUsageAt = "openai", 1, usage.CreatedAt
	var quotaID int64
	day, week := timezone.StartOfDay(usage.CreatedAt), timezone.StartOfWeek(usage.CreatedAt)
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO user_platform_quotas
		(user_id,platform,daily_limit_usd,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start)
		VALUES($1,'openai',100,2,2,2,$2,$3,$4) RETURNING id`, cmd.UserID, day, week, usage.CreatedAt).Scan(&quotaID))
	require.NoError(t, repo.StageUsageBilling(ctx, &cmd, &usage))
	name := "quota_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.quota_id=%d THEN RAISE EXCEPTION 'injected quota DB failure'; END IF; RETURN NEW; END; $$;
	CREATE TRIGGER %s BEFORE INSERT ON usage_platform_quota_totals FOR EACH ROW EXECUTE FUNCTION %s()`, name, quotaID, name, name))
	require.NoError(t, err)
	cleanup := func() {
		_, _ = db.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON usage_platform_quota_totals; DROP FUNCTION IF EXISTS %s()", name, name))
	}
	t.Cleanup(cleanup)
	key := service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID}
	_, err = repo.ApplyStagedUsageBilling(ctx, key)
	require.Error(t, err)
	var balance string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, "100.00000000", balance, "quota failure rolls back the debit, not an untracked post-commit goroutine")
	cleanup()
	for attempt := 0; attempt < 2; attempt++ {
		_, err = repo.ApplyStagedUsageBilling(ctx, key)
		require.NoError(t, err)
	}
	quotaRepo := NewUserPlatformQuotaRepository(client)
	rec, err := quotaRepo.GetByUserPlatform(ctx, cmd.UserID, "openai")
	require.NoError(t, err)
	require.Equal(t, 3.0, rec.DailyUsageUSD)
	// A late legacy flusher snapshot changes only the old baseline (2 -> 2.5).
	// It cannot replace the new durable delta of 1, under either flusher setting.
	require.NoError(t, quotaRepo.BatchSnapshotUsage(ctx, []UserPlatformQuotaSnapshot{{UserID: cmd.UserID, Platform: "openai",
		DailyUsageUSD: 2.5, WeeklyUsageUSD: 2.5, MonthlyUsageUSD: 2.5,
		DailyWindowStart: day, WeeklyWindowStart: week, MonthlyWindowStart: usage.CreatedAt}}, time.Now()))
	rec, err = quotaRepo.GetByUserPlatform(ctx, cmd.UserID, "openai")
	require.NoError(t, err)
	require.Equal(t, 3.5, rec.DailyUsageUSD)
	require.NoError(t, quotaRepo.ResetExpiredWindow(ctx, cmd.UserID, "openai", "daily", day))
	rec, err = quotaRepo.GetByUserPlatform(ctx, cmd.UserID, "openai")
	require.NoError(t, err)
	require.Zero(t, rec.DailyUsageUSD)
	require.Equal(t, 3.5, rec.WeeklyUsageUSD, "explicit daily reset must not reset another window")
}

func TestUsageBillingPlatformLateRetryDoesNotRewindNewWindow(t *testing.T) {
	ctx := context.Background()
	repo, first, usage, client := durableBillingFixture(t)
	db := repo.db
	first.PlatformQuotaPlatform, first.PlatformQuotaCost = "openai", 1
	first.PlatformUsageAt = usage.CreatedAt.Add(-48 * time.Hour)
	_, err := db.ExecContext(ctx, "INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd) VALUES($1,'openai',100)", first.UserID)
	require.NoError(t, err)
	second := first
	second.RequestID = uuid.NewString()
	second.PlatformUsageAt = usage.CreatedAt
	// Apply the current event first, then a recovered older observation.
	for _, cmd := range []*service.UsageBillingCommand{&second, &first} {
		log := usage
		log.RequestID = cmd.RequestID
		require.NoError(t, repo.StageUsageBilling(ctx, cmd, &log))
		_, err = repo.ApplyStagedUsageBilling(ctx, service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID})
		require.NoError(t, err)
	}
	rec, err := NewUserPlatformQuotaRepository(client).GetByUserPlatform(ctx, first.UserID, "openai")
	require.NoError(t, err)
	require.Equal(t, 1.0, rec.DailyUsageUSD)
	require.True(t, rec.DailyWindowStart.Equal(timezone.StartOfDay(usage.CreatedAt)))
}
