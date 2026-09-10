//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// balanceExhaustionCacheStub 在余额缓存桩之上增加"钱包已耗尽"标记能力，
// 用来验证预检不再只依赖可能被旧回源值污染的余额缓存。
type balanceExhaustionCacheStub struct {
	balanceEligibilityCacheStub

	exhausted  atomic.Bool
	markCalls  atomic.Int64
	clearCalls atomic.Int64
}

func (s *balanceExhaustionCacheStub) MarkUserBalanceExhausted(context.Context, int64) error {
	s.markCalls.Add(1)
	s.exhausted.Store(true)
	return nil
}

func (s *balanceExhaustionCacheStub) ClearUserBalanceExhausted(context.Context, int64) error {
	s.clearCalls.Add(1)
	s.exhausted.Store(false)
	return nil
}

func (s *balanceExhaustionCacheStub) IsUserBalanceExhausted(context.Context, int64) (bool, error) {
	return s.exhausted.Load(), nil
}

// TestCheckBillingEligibility_RejectsWhenBalanceCacheRevivedStaleHigherValue 复现并锁死
// 线上异常：钱包已被扣到 reserve 底线后，余额缓存里仍留着一个偏高的旧快照
// （"未命中回源 + 异步写回"在 InvalidateUserBalance(DEL) 之后才落盘，把它复活）。
//
// 旧行为：预检只看缓存余额（0.50 > reserve 0.10）→ 放行 → 上游被调用（成本已经发生）
// → 结算必然失败（DB 余额 == floor）→ usage_log.actual_cost 记 0、余额不再下降，
// 表现为"余额扣不动、token 照统计、请求不阻断"。
//
// 修复后："钱包已耗尽"标记优先于缓存余额，命中即 fail-closed。
func TestCheckBillingEligibility_RejectsWhenBalanceCacheRevivedStaleHigherValue(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 0.50        // 被旧回源值污染的缓存余额，远高于底线
	cache.exhausted.Store(true) // 结算已经判定钱包无钱可扣

	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, &balanceLoadUserRepoStub{balance: 0.10}, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)

	// 同时要顺手失效被污染的余额缓存，让标记过期后立即恢复真实判断。
	require.Equal(t, int64(1), cache.invalidateCalls.Load())
}

// TestCheckBillingEligibility_RechecksDBWhenCachedBalanceNearReserve 覆盖没有标记、
// 但缓存余额贴近底线的场景：此时必须用 DB 真值复核，否则"预检放行 ⇒ 结算必然失败"。
func TestCheckBillingEligibility_RechecksDBWhenCachedBalanceNearReserve(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 0.108227 // 扣到底线那一刻之前的余额快照

	userRepo := &balanceLoadUserRepoStub{balance: 0.10} // DB 真值：已在底线
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(1), userRepo.calls.Load())
	// 复核判定为耗尽后同样要打标记，避免下一次又只信缓存。
	require.Equal(t, int64(1), cache.markCalls.Load())
}

// TestCheckBillingEligibility_RefreshesCacheWhenDBStillSpendable 确认复核不会误伤
// 真正还有余额的用户，并把缓存纠正为 DB 真值。
func TestCheckBillingEligibility_RefreshesCacheWhenDBStillSpendable(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 0.11

	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, &balanceLoadUserRepoStub{balance: 0.11}, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.NoError(t, err)
	require.Equal(t, int64(0), cache.markCalls.Load())
	require.Equal(t, int64(1), cache.setCalls.Load())
	require.Equal(t, 0.11, cache.balanceSet.Load())
}

// TestCheckBillingEligibility_DoesNotRecheckWhenBalanceComfortablyAboveReserve
// 保证热路径不为正常用户增加 DB 查询。
func TestCheckBillingEligibility_DoesNotRecheckWhenBalanceComfortablyAboveReserve(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 5

	userRepo := &balanceLoadUserRepoStub{balance: 5}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.NoError(t, err)
	require.Equal(t, int64(0), userRepo.calls.Load())
}

// TestMarkBalanceExhaustedAfterSettlement_MarksOnShortfall 验证"扣到底线为止"的结算
// 会立刻打上耗尽标记——这是"钱花完就停止放行"的闭环关键，不再依赖缓存失效是否及时。
func TestMarkBalanceExhaustedAfterSettlement_MarksOnShortfall(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	deps := &billingDeps{billingCacheService: svc}
	params := &postUsageBillingParams{Cost: &CostBreakdown{ActualCost: 0.75}, User: &User{ID: 1}}

	markBalanceExhaustedAfterSettlement(context.Background(), params, deps, &UsageBillingApplyResult{
		BalanceCollected: 0.10,
		BalanceShortfall: 0.65,
	})
	require.Equal(t, int64(1), cache.markCalls.Load())
	// 余额缓存的失效由结算成功路径的 syncBalanceCacheAfterDeduction 负责，
	// 打标记这一步不重复失效。
	require.Equal(t, int64(0), cache.invalidateCalls.Load())
}

// TestMarkBalanceExhaustedAfterSettlement_SkipsFullCollection 验证全额扣费不打标记。
func TestMarkBalanceExhaustedAfterSettlement_SkipsFullCollection(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	newBalance := 1.25
	markBalanceExhaustedAfterSettlement(context.Background(), &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.75},
		User: &User{ID: 1},
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{
		NewBalance:       &newBalance,
		BalanceCollected: 0.75,
	})

	require.Equal(t, int64(0), cache.markCalls.Load())
	require.Equal(t, int64(0), cache.invalidateCalls.Load())
}

// TestMarkBalanceExhaustedAfterSettlement_SkipsSubscriptionBilling 验证订阅计费不打标记。
func TestMarkBalanceExhaustedAfterSettlement_SkipsSubscriptionBilling(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	markBalanceExhaustedAfterSettlement(context.Background(), &postUsageBillingParams{
		Cost:               &CostBreakdown{ActualCost: 0.75},
		User:               &User{ID: 1},
		IsSubscriptionBill: true,
	}, &billingDeps{billingCacheService: svc}, &UsageBillingApplyResult{BalanceShortfall: 0.65})

	require.Equal(t, int64(0), cache.markCalls.Load())
}

// TestInvalidateUserBalanceAfterCredit_ClearsExhaustedMarker 验证任何加款路径都会
// 清掉标记，充值后的用户不会被继续拦截。
func TestInvalidateUserBalanceAfterCredit_ClearsExhaustedMarker(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 0.60 // 充值后的余额
	cache.exhausted.Store(true)

	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, &balanceLoadUserRepoStub{balance: 0.60}, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	// 充值前：标记生效，即使缓存余额偏高也拒绝。
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)

	require.NoError(t, svc.InvalidateUserBalanceAfterCredit(context.Background(), 1))
	require.Equal(t, int64(1), cache.clearCalls.Load())

	// 充值后：标记已清除，余额充足 → 放行。
	err = svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "")
	require.NoError(t, err)
}

// TestClearBalanceExhaustedMarker_NoopForPlainCache 验证底层实现不支持标记时
// 清除操作安全降级（no-op），不会 panic。
func TestClearBalanceExhaustedMarker_NoopForPlainCache(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 1}
	require.NotPanics(t, func() {
		ClearBalanceExhaustedMarker(context.Background(), cache, 1)
	})
}
