//go:build integration

package repository

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type failingCheckedBillingAuth struct{ fail atomic.Bool }

func (*failingCheckedBillingAuth) InvalidateAuthCacheByKey(context.Context, string) {
	panic("unchecked invalidation cannot confirm durable billing")
}
func (*failingCheckedBillingAuth) InvalidateAuthCacheByUserID(context.Context, int64) {
	panic("unchecked invalidation cannot confirm durable billing")
}
func (*failingCheckedBillingAuth) InvalidateAuthCacheByGroupID(context.Context, int64) {
	panic("unchecked invalidation cannot confirm durable billing")
}
func (a *failingCheckedBillingAuth) InvalidateAuthCacheByUserIDChecked(context.Context, int64) error {
	if a.fail.Load() {
		return errors.New("injected checked broadcast failure")
	}
	return nil
}

func TestUsageBillingQuotaExhaustionAuthFailureRetriesWithoutSecondDebit(t *testing.T) {
	ctx := context.Background()
	repo, cmd, usage, _ := durableBillingFixture(t)
	db := repo.db
	_, err := db.ExecContext(ctx, "UPDATE api_keys SET quota=1 WHERE id=$1", cmd.APIKeyID)
	require.NoError(t, err)
	require.NoError(t, repo.StageUsageBilling(ctx, &cmd, &usage))
	result, err := repo.ApplyStagedUsageBilling(ctx, service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID})
	require.NoError(t, err)
	require.True(t, result.Result.APIKeyQuotaExhausted)
	auth := &failingCheckedBillingAuth{}
	auth.fail.Store(true)
	worker := service.ProvideUsageRecordWorkerPool(&config.Config{}, repo, nil, auth)
	t.Cleanup(worker.Stop)
	var state, failure string
	require.Eventually(t, func() bool {
		err := db.QueryRowContext(ctx, `SELECT state,COALESCE(failure_code,'') FROM usage_billing_events WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&state, &failure)
		return err == nil && state == "applied" && failure == "cache_invalidation_failed"
	}, 10*time.Second, 10*time.Millisecond)
	auth.fail.Store(false)
	// Advance the synthetic retry clock after the failure receipt is durable.
	_, err = db.ExecContext(ctx, "UPDATE usage_billing_events SET next_attempt_at=NOW() WHERE request_id=$1 AND api_key_id=$2", cmd.RequestID, cmd.APIKeyID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		err := db.QueryRowContext(ctx, "SELECT state FROM usage_billing_events WHERE request_id=$1 AND api_key_id=$2", cmd.RequestID, cmd.APIKeyID).Scan(&state)
		return err == nil && state == "complete"
	}, 10*time.Second, 10*time.Millisecond)
	var balance, quota, status string
	var canonicalRows int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT quota_used::text,status FROM api_keys WHERE id=$1", cmd.APIKeyID).Scan(&quota, &status))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", cmd.RequestID, cmd.APIKeyID).Scan(&canonicalRows))
	require.Equal(t, "99.00000000", balance)
	require.Equal(t, "1.00000000", quota)
	require.Equal(t, service.StatusAPIKeyQuotaExhausted, status)
	require.Equal(t, 1, canonicalRows)
}
