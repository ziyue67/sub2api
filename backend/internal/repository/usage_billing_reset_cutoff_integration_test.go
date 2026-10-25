//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageBillingNonBoundaryResetPreservesNewUsageAndRejectsLateWindowUsage(t *testing.T) {
	for _, window := range []string{"daily", "weekly"} {
		for _, existingTotals := range []bool{false, true} {
			for _, lateFirst := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/totals=%t/late_first=%t", window, existingTotals, lateFirst), func(t *testing.T) {
					ctx := context.Background()
					repo, template, usage, client := durableBillingFixture(t)
					db := repo.db
					cutoff := time.Date(2026, 10, 21, 12, 34, 56, 123000000, timezone.Location())
					_, err := db.ExecContext(ctx, `INSERT INTO user_platform_quotas
					 (user_id,platform,daily_limit_usd,weekly_limit_usd,daily_usage_usd,weekly_usage_usd,daily_window_start,weekly_window_start)
					 VALUES($1,'openai',100,100,2,2,$2,$3)`, template.UserID, timezone.StartOfDay(cutoff), timezone.StartOfWeek(cutoff))
					require.NoError(t, err)
					stage := func(at time.Time, amount float64) service.UsageBillingEventKey {
						cmd, log := template, usage
						cmd.RequestID, log.RequestID = uuid.NewString(), ""
						cmd.RequestFingerprint = ""
						cmd.BalanceCost, cmd.APIKeyQuotaCost = amount, amount
						cmd.PlatformQuotaPlatform, cmd.PlatformQuotaCost, cmd.PlatformUsageAt = "openai", amount, at
						log.RequestID, log.ActualCost, log.TotalCost, log.CreatedAt = cmd.RequestID, amount, amount, at
						require.NoError(t, repo.StageUsageBilling(ctx, &cmd, &log))
						return service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID}
					}
					if existingTotals {
						_, err = repo.ApplyStagedUsageBilling(ctx, stage(cutoff.Add(-time.Hour), 0.5))
						require.NoError(t, err)
					}
					// Persist an observed request before the exact administrative reset,
					// but leave its monetary execution pending across that reset.
					late := stage(cutoff.Add(-time.Minute), 0.25)
					quotaRepo := NewUserPlatformQuotaRepository(client)
					require.NoError(t, quotaRepo.ResetExpiredWindow(ctx, template.UserID, "openai", window, cutoff))
					fresh := stage(cutoff.Add(time.Minute), 0.4)
					order := []service.UsageBillingEventKey{fresh, late}
					if lateFirst {
						order = []service.UsageBillingEventKey{late, fresh}
					}
					for repeat := 0; repeat < 2; repeat++ {
						for _, key := range order {
							result, err := repo.ApplyStagedUsageBilling(ctx, key)
							require.NoError(t, err)
							require.Equal(t, repeat == 0, result.Result.Applied)
						}
					}
					rec, err := quotaRepo.GetByUserPlatform(ctx, template.UserID, "openai")
					require.NoError(t, err)
					used, start := rec.DailyUsageUSD, rec.DailyWindowStart
					if window == "weekly" {
						used, start = rec.WeeklyUsageUSD, rec.WeeklyWindowStart
					}
					require.Equal(t, 0.4, used, "new post-reset usage counts; pre-reset late usage and replays do not")
					require.NotNil(t, start)
					require.True(t, start.Equal(cutoff), "retain the precise cutoff rather than rounding it to a natural boundary")
					var balance string
					require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", template.UserID).Scan(&balance))
					want := "99.35000000"
					if existingTotals {
						want = "98.85000000"
					}
					require.Equal(t, want, balance, "the quota reset never refunds or repeats the monetary charge")
				})
			}
		}
	}
}
