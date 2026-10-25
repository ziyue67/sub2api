//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type recoveryCacheFailure struct{ BillingCache }

func (*recoveryCacheFailure) InvalidateUserBalance(_ context.Context, user int64) error {
	if user == 1 {
		return errors.New("injected cache failure")
	}
	return nil
}

type recoveryCacheRepo struct {
	UsageBillingRepository
	seen     map[string]bool
	acked    []string
	deferred []string
}

func (*recoveryCacheRepo) StageUsageBilling(context.Context, *UsageBillingCommand, *UsageLog) error {
	return nil
}
func (*recoveryCacheRepo) PendingUsageBilling(context.Context, int) ([]UsageBillingEventKey, error) {
	return []UsageBillingEventKey{{RequestID: "first", APIKeyID: 1}, {RequestID: "second", APIKeyID: 2}}, nil
}
func (r *recoveryCacheRepo) ApplyStagedUsageBilling(_ context.Context, key UsageBillingEventKey) (*UsageBillingRecovery, error) {
	fresh := !r.seen[key.RequestID]
	r.seen[key.RequestID] = true
	return &UsageBillingRecovery{Event: UsageBillingEvent{Command: UsageBillingCommand{UserID: key.APIKeyID, BalanceCost: 1}},
		Result: UsageBillingApplyResult{Applied: fresh}, NeedsCache: true}, nil
}
func (r *recoveryCacheRepo) AcknowledgeUsageBillingCache(_ context.Context, key UsageBillingEventKey) error {
	r.acked = append(r.acked, key.RequestID)
	return nil
}
func (r *recoveryCacheRepo) DeferUsageBillingCache(_ context.Context, key UsageBillingEventKey) error {
	r.deferred = append(r.deferred, key.RequestID)
	return nil
}

func TestUsageRecoveryCacheFailureDoesNotStopAnotherEventOrReplayDeltas(t *testing.T) {
	for _, flusher := range []bool{false, true} {
		r := &recoveryCacheRepo{seen: map[string]bool{}}
		cfg := &config.Config{}
		cfg.Database.UserPlatformQuotaFlusherEnabled = flusher
		cache := &BillingCacheService{cache: &recoveryCacheFailure{}, cfg: cfg}
		for attempt := 0; attempt < 2; attempt++ {
			require.Error(t, recoverUsageBillingBatch(context.Background(), r, cache, nil))
		}
		require.Equal(t, []string{"second", "second"}, r.acked)
		require.Equal(t, []string{"first", "first"}, r.deferred)
		require.Len(t, r.seen, 2)
		// The embedded nil BillingCache panics on a monetary delta method. This
		// path only invalidates, under both legacy flusher configurations.
	}
}
