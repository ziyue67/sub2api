//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// fakeQuotaLedger 是 APIKeyQuotaUsedLedger 的最小实现（内存高水位，只增不减）。
type fakeQuotaLedger struct {
	used       map[int64]float64
	getErr     error
	setErr     error
	clearCalls []int64
}

func (f *fakeQuotaLedger) GetAPIKeyQuotaUsedLedger(_ context.Context, apiKeyID int64) (float64, error) {
	if f.getErr != nil {
		return 0, f.getErr
	}
	return f.used[apiKeyID], nil
}

func (f *fakeQuotaLedger) SetAPIKeyQuotaUsedLedger(_ context.Context, apiKeyID int64, quotaUsed float64) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.used == nil {
		f.used = map[int64]float64{}
	}
	if quotaUsed > f.used[apiKeyID] {
		f.used[apiKeyID] = quotaUsed
	}
	return nil
}

func (f *fakeQuotaLedger) ClearAPIKeyQuotaUsedLedger(_ context.Context, apiKeyID int64) error {
	f.clearCalls = append(f.clearCalls, apiKeyID)
	delete(f.used, apiKeyID)
	return nil
}

func setQuotaLedgerForTest(t *testing.T, ledger APIKeyQuotaUsedLedger) {
	t.Helper()
	original := APIKeyQuotaUsedLedgerReader()
	SetAPIKeyQuotaUsedLedger(ledger)
	t.Cleanup(func() { SetAPIKeyQuotaUsedLedger(original) })
}

// TestAPIKeyQuotaReservation_StaleAuthSnapshotRescuedByLedger 锁死"快照冻结"缺口的修复：
// 鉴权快照里的 quota_used 仍是旧值（结算只在 DB 递增），但共享账本已经发布结算后的真值。
// 准入必须取 max(快照, 账本)，否则同一个 key 会在快照 TTL 内把剩余额度重复放行一遍。
func TestAPIKeyQuotaReservation_StaleAuthSnapshotRescuedByLedger(t *testing.T) {
	ledger := &fakeQuotaLedger{used: map[int64]float64{7: 0.25}}
	setQuotaLedgerForTest(t, ledger)

	cache := &reservationCacheStub{balance: 10.0}
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	// 快照说"已用 0"，账本说"已用 0.25"，quota=0.30：按快照还能放 0.30，
	// 按账本只剩 0.05 —— 单笔最坏费用 0.10 必须被拒（保守方向）。
	apiKey := &APIKey{ID: 7, UserID: 1, Quota: 0.30, QuotaUsed: 0}
	var slot BillingReservationSlot
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, apiKey, nil, nil, "",
		reservationEligibilityOpts(&slot, 0.10)...)
	require.ErrorIs(t, err, ErrAPIKeyQuotaExhausted,
		"账本高水位必须压住冻结的鉴权快照（否则额度被重复放行）")
	require.InDelta(t, 0.0, cache.reservedAmountForScope(apiKeyQuotaReservationScope(apiKey.ID)), 1e-9,
		"被拒请求不得留下 key 额度预留")
}

// TestAPIKeyQuotaReservation_LedgerBelowSnapshotKeepsAdmission 确认账本只做"保守化"：
// 账本值不高于快照时，准入仍按快照判定（不会误拦）。
func TestAPIKeyQuotaReservation_LedgerBelowSnapshotKeepsAdmission(t *testing.T) {
	ledger := &fakeQuotaLedger{used: map[int64]float64{7: 0.05}}
	setQuotaLedgerForTest(t, ledger)

	cache := &reservationCacheStub{balance: 10.0}
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	apiKey := &APIKey{ID: 7, UserID: 1, Quota: 0.30, QuotaUsed: 0.20}
	var slot BillingReservationSlot
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, apiKey, nil, nil, "",
		reservationEligibilityOpts(&slot, 0.05)...)
	require.NoError(t, err, "剩余 0.10 覆盖 0.05 的最坏费用，应放行")
	require.InDelta(t, 0.05, cache.reservedAmountForScope(apiKeyQuotaReservationScope(apiKey.ID)), 1e-9)
}

// TestAPIKeyQuotaReservation_LedgerReadFailureFallsBackToSnapshot 确认账本读失败时
// 退回"仅快照"语义（方向偏松但不放大 Redis 抖动为全量 503），并计入可观测计数器。
func TestAPIKeyQuotaReservation_LedgerReadFailureFallsBackToSnapshot(t *testing.T) {
	ledger := &fakeQuotaLedger{getErr: errors.New("redis down")}
	setQuotaLedgerForTest(t, ledger)

	before := BillingGuardStatsSnapshot().APIKeyQuotaLedgerReadError

	cache := &reservationCacheStub{balance: 10.0}
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	apiKey := &APIKey{ID: 7, UserID: 1, Quota: 0.30, QuotaUsed: 0.20}
	var slot BillingReservationSlot
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, apiKey, nil, nil, "",
		reservationEligibilityOpts(&slot, 0.05)...)
	require.NoError(t, err)
	require.Equal(t, before+1, BillingGuardStatsSnapshot().APIKeyQuotaLedgerReadError,
		"账本读失败必须计数（护栏降级信号）")
}

// TestSyncSubscriptionUsageAfterSettlement_InvalidatesCacheOnSkippedIncrement 锁死
// "预留已归还、usage 仍是旧值"的补救方向：累加被跳过（缓存键缺失）时必须失效订阅缓存，
// 让下一次预检回源 DB 真值，而不是继续用偏小的快照放行。
func TestSyncSubscriptionUsageAfterSettlement_InvalidatesCacheOnSkippedIncrement(t *testing.T) {
	cache := &skippingUsageCache{}
	cfg := &config.Config{}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	before := BillingGuardStatsSnapshot().SubscriptionUsageSyncError
	svc.SyncSubscriptionUsageAfterSettlement(context.Background(), 1, 2, 1.5)

	require.Equal(t, 1, cache.invalidateCalls, "累加被跳过时必须失效订阅缓存")
	require.Equal(t, before+1, BillingGuardStatsSnapshot().SubscriptionUsageSyncError)
}

// skippingUsageCache 模拟"缓存键缺失 ⇒ 累加被跳过（applied=false）"的生产缓存语义。
type skippingUsageCache struct {
	BillingCache
	invalidateCalls int
}

func (s *skippingUsageCache) UpdateSubscriptionUsageApplied(context.Context, int64, int64, float64) (bool, error) {
	return false, nil
}

func (s *skippingUsageCache) InvalidateSubscriptionCache(context.Context, int64, int64) error {
	s.invalidateCalls++
	return nil
}
