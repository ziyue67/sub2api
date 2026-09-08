//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type balanceEligibilityCacheStub struct {
	billingCacheWorkerStub

	balance                  float64
	cacheMissAfterInvalidate bool
	invalidated              atomic.Bool
	getCalls                 atomic.Int64
	setBalanceCalls          atomic.Int64
	deductCalls              atomic.Int64
	invalidateCalls          atomic.Int64
}

func (s *balanceEligibilityCacheStub) GetUserBalance(context.Context, int64) (float64, error) {
	s.getCalls.Add(1)
	if s.cacheMissAfterInvalidate && s.invalidated.Load() {
		return 0, errors.New("cache miss")
	}
	return s.balance, nil
}

func (s *balanceEligibilityCacheStub) SetUserBalance(_ context.Context, _ int64, balance float64) error {
	s.setBalanceCalls.Add(1)
	s.balance = balance
	return nil
}

func (s *balanceEligibilityCacheStub) DeductUserBalance(context.Context, int64, float64) error {
	s.deductCalls.Add(1)
	return nil
}

func (s *balanceEligibilityCacheStub) InvalidateUserBalance(context.Context, int64) error {
	s.invalidateCalls.Add(1)
	s.invalidated.Store(true)
	return nil
}

type gatewayReservationRepoStub struct {
	reserveCalls atomic.Int64
	command      BalanceReservationCommand
}

func (s *gatewayReservationRepoStub) ReserveGatewayBalance(_ context.Context, cmd *BalanceReservationCommand) (*BalanceReservationResult, error) {
	s.reserveCalls.Add(1)
	if cmd != nil {
		s.command = *cmd
	}
	return &BalanceReservationResult{Applied: true, Reserved: 5, NewBalance: 0, FrozenBalance: 5}, nil
}

func (s *gatewayReservationRepoStub) CaptureGatewayBalance(context.Context, *BalanceReservationCommand) (*BalanceReservationResult, error) {
	return nil, nil
}

func (s *gatewayReservationRepoStub) ReleaseGatewayBalance(context.Context, *BalanceReservationCommand) (*BalanceReservationResult, error) {
	return &BalanceReservationResult{Applied: true, NewBalance: 5}, nil
}

func TestCheckBillingEligibility_RejectsBalanceBelowMinimumReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.005}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestCheckBillingEligibility_AllowsBalanceAtMinimumReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.01}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.NoError(t, err)
}

func TestCheckBillingEligibilityReservesAuthoritativeBalanceWithoutExtraUserLookup(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 5}
	userRepo := &balanceLoadUserRepoStub{balance: 5}
	reservationRepo := &gatewayReservationRepoStub{}
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, &config.Config{}, nil)
	svc.SetBalanceReservationRepository(reservationRepo)
	t.Cleanup(svc.Stop)

	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-hot-path")
	err := svc.CheckBillingEligibility(ctx, &User{ID: 42}, &APIKey{ID: 7}, nil, nil, "")
	require.NoError(t, err)
	require.Equal(t, int64(1), reservationRepo.reserveCalls.Load())
	require.Zero(t, userRepo.calls.Load(), "reservation admission must not perform a separate user balance query")
	require.Zero(t, cache.getCalls.Load(), "authoritative reservation should replace the extra cache balance read")
	require.Equal(t, "req-hot-path", reservationRepo.command.RequestID)
	require.Equal(t, int64(42), reservationRepo.command.UserID)
	require.Equal(t, int64(7), reservationRepo.command.APIKeyID)
	require.True(t, reservationRepo.command.ReserveAvailableBalance)
	require.Zero(t, reservationRepo.command.MinimumBalance)
}

func TestGatewayReservationReleaseRestoresBalanceCacheAfterRequestCancel(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 5}
	reservationRepo := &gatewayReservationRepoStub{}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, &config.Config{}, nil)
	svc.SetBalanceReservationRepository(reservationRepo)
	t.Cleanup(svc.Stop)

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxkey.RequestID, "req-cancel"))
	require.NoError(t, svc.CheckBillingEligibility(ctx, &User{ID: 42}, &APIKey{ID: 7}, nil, nil, ""))
	cancel()
	require.Eventually(t, func() bool {
		return cache.setBalanceCalls.Load() >= 2 && cache.balance == 5
	}, time.Second, 10*time.Millisecond)
}

func TestSyncBalanceCacheAfterDeduction_InvalidatesExhaustedBalance(t *testing.T) {
	cache := &balanceEligibilityCacheStub{
		balance:                  0.50,
		cacheMissAfterInvalidate: true,
	}
	userRepo := &balanceLoadUserRepoStub{balance: -0.25}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	newBalance := -0.25
	syncBalanceCacheAfterDeduction(context.Background(), &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.75},
		User: &User{ID: 1},
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{
		NewBalance:         &newBalance,
		BalanceOverdrafted: true,
	})

	require.Equal(t, int64(1), cache.invalidateCalls.Load())
	require.Equal(t, int64(0), cache.deductCalls.Load())

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(1), userRepo.calls.Load())
}

func TestSyncBalanceCacheAfterDeduction_InvalidatesWhenBalanceFallsBelowReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.50}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	newBalance := 0.005
	syncBalanceCacheAfterDeduction(context.Background(), &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.495},
		User: &User{ID: 1},
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{NewBalance: &newBalance})

	require.Equal(t, int64(1), cache.invalidateCalls.Load())
	require.Equal(t, int64(0), cache.deductCalls.Load())
}

func TestSyncBalanceCacheAfterDeduction_QueuesDeductWhenBalanceStillEligible(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 1}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	newBalance := 0.75
	syncBalanceCacheAfterDeduction(context.Background(), &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.25},
		User: &User{ID: 1},
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{NewBalance: &newBalance})

	require.Equal(t, int64(0), cache.invalidateCalls.Load())
	require.Eventually(t, func() bool {
		return cache.deductCalls.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
}
