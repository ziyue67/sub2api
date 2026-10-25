//go:build integration

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPlatformQuotaOrdinaryRegionalHandoffPreservesRollingMonthAndMoney(t *testing.T) {
	ctx := context.Background()
	repo, cmd, usage, client := durableBillingFixture(t)
	db := repo.db
	group := mustCreateGroup(t, client, &service.Group{Name: "handoff-" + uuid.NewString(), Platform: service.PlatformOpenAI, RateMultiplier: 1})
	_, err := db.ExecContext(ctx, "UPDATE api_keys SET group_id=$2 WHERE id=$1", cmd.APIKeyID, group.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE accounts SET platform='openai',status='active',schedulable=true WHERE id=$1", cmd.AccountID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "INSERT INTO account_groups(account_id,group_id)VALUES($1,$2)", cmd.AccountID, group.ID)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ordinaryAt := now.Add(-10 * 24 * time.Hour)
	_, err = db.ExecContext(ctx, `INSERT INTO user_platform_quotas
		(user_id,platform,monthly_limit_usd,monthly_usage_usd,monthly_window_start)
		VALUES($1,'openai',6,4,$2)`, cmd.UserID, now.Add(-45*24*time.Hour))
	require.NoError(t, err)
	ordinary := func(at time.Time, amount float64) {
		c, u := cmd, usage
		c.RequestID, c.RequestFingerprint = uuid.NewString(), ""
		c.BalanceCost, c.APIKeyQuotaCost = amount, amount
		c.PlatformQuotaPlatform, c.PlatformQuotaCost, c.PlatformUsageAt = "openai", amount, at
		u.RequestID, u.ActualCost, u.TotalCost, u.CreatedAt = c.RequestID, amount, amount, at
		require.NoError(t, repo.StageUsageBilling(ctx, &c, &u))
		_, err := repo.ApplyStagedUsageBilling(ctx, service.UsageBillingEventKey{RequestID: c.RequestID, APIKeyID: c.APIKeyID})
		require.NoError(t, err)
	}
	ordinary(ordinaryAt, 5)
	quotaRepo := NewUserPlatformQuotaRepository(client)
	assertMonth := func(amount float64, start time.Time) {
		rec, err := quotaRepo.GetByUserPlatform(ctx, cmd.UserID, "openai")
		require.NoError(t, err)
		require.Equal(t, amount, rec.MonthlyUsageUSD)
		require.NotNil(t, rec.MonthlyWindowStart)
		require.True(t, rec.MonthlyWindowStart.Equal(start))
	}
	assertMonth(5, ordinaryAt)
	_, holds, _ := NewRegionalFundsAuthorities(db, func(context.Context, *sql.Tx) error { return nil })
	price := ledger.Price{Model: "handoff-" + uuid.NewString(), Input: 500_000_000_000_000, MaxInput: 1, MaxOutput: 1}
	require.NoError(t, ledger.InstallPrice(ctx, db, price))
	request := func(at time.Time, maximum regional.Amount) ledger.Reservation {
		return ledger.Reservation{RequestID: uuid.NewString(), UserID: cmd.UserID, APIKeyID: cmd.APIKeyID, AccountID: cmd.AccountID,
			RegionID: "primary", AdmittedAt: at, Maximum: maximum, Model: price.Model,
			PricingVersion: price.Version(), PayloadSHA256: strings.Repeat("a", 64)}
	}
	// Actual primary admission, not just a helper invocation: 5 already spent
	// plus a maximum of 2 must exceed the monthly limit of 6.
	_, err = holds.Reserve(ctx, request(now, 2_000_000_000))
	require.ErrorIs(t, err, ledger.ErrExhausted)
	assertMonth(5, ordinaryAt)
	complete := func(at time.Time) {
		r := request(at, 1_000_000_000)
		fresh, err := holds.Reserve(ctx, r)
		require.NoError(t, err)
		require.True(t, fresh)
		require.NoError(t, holds.Start(ctx, r.RequestID))
		e := regionalLedgerEvent(r, 500_000_000)
		e.InputTokens, e.OutputTokens = 1, 0
		e.Usage.CompletedAt = at.Add(time.Millisecond)
		require.NoError(t, holds.Complete(ctx, e))
		require.NoError(t, holds.Complete(ctx, e))
	}
	complete(now)
	assertMonth(5.5, ordinaryAt)
	boundary := ordinaryAt.Add(30 * 24 * time.Hour)
	_, err = holds.Reserve(ctx, request(boundary.Add(-time.Microsecond), 1_000_000_000))
	require.ErrorIs(t, err, ledger.ErrExhausted)
	assertMonth(5.5, ordinaryAt)
	complete(boundary)
	assertMonth(0.5, boundary)
	// Switching back to ordinary delivery must share the newly advanced
	// regional anchor; an hour-later anchor must not hide the regional 0.5.
	ordinary(boundary.Add(time.Hour), 0.25)
	assertMonth(0.75, boundary)
	var balance string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, "93.75000000", balance)
}
