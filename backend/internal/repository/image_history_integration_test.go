//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestImageHistoryLegacyBillingDurableReceipt(t *testing.T) {
	ctx := context.Background()
	billing, cmd, usage, _ := durableBillingFixture(t)
	upstream := uuid.NewString()
	usage.ImageCount, usage.UpstreamRequestID = 1, &upstream
	require.NoError(t, billing.StageUsageBilling(ctx, &cmd, &usage))
	repo := &imageArchiveRepository{db: billing.db}
	// Staging alone does not produce a log or prove payment.
	got, err := repo.ImageHistoryLegacyBilling(ctx, cmd.UserID, cmd.APIKeyID, cmd.AccountID, upstream)
	require.NoError(t, err)
	require.Equal(t, "unknown", got.Status)
	_, err = billing.ApplyStagedUsageBilling(ctx, service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID})
	require.NoError(t, err)
	for _, id := range []string{upstream, cmd.RequestID} {
		got, err = repo.ImageHistoryLegacyBilling(ctx, cmd.UserID, cmd.APIKeyID, cmd.AccountID, id)
		require.NoError(t, err)
		require.Equal(t, "charged", got.Status)
		require.NotNil(t, got.Amount)
		require.Equal(t, float64(1), *got.Amount)
	}
	for _, identity := range [][3]int64{
		{cmd.UserID + 1, cmd.APIKeyID, cmd.AccountID},
		{cmd.UserID, cmd.APIKeyID + 1, cmd.AccountID},
		{cmd.UserID, cmd.APIKeyID, cmd.AccountID + 1},
	} {
		got, err = repo.ImageHistoryLegacyBilling(ctx, identity[0], identity[1], identity[2], upstream)
		require.NoError(t, err)
		require.Equal(t, "unknown", got.Status)
		require.Nil(t, got.Amount)
	}
	// Two real billable requests reusing an upstream ID must not be guessed.
	cmd.RequestID = uuid.NewString()
	usage.RequestID = cmd.RequestID
	require.NoError(t, billing.StageUsageBilling(ctx, &cmd, &usage))
	_, err = billing.ApplyStagedUsageBilling(ctx, service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID})
	require.NoError(t, err)
	got, err = repo.ImageHistoryLegacyBilling(ctx, cmd.UserID, cmd.APIKeyID, cmd.AccountID, upstream)
	require.NoError(t, err)
	require.Equal(t, "unknown", got.Status)
	require.Nil(t, got.Amount)
	// History reads must not reapply either charge.
	var balance float64
	require.NoError(t, billing.db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, float64(98), balance)
}
