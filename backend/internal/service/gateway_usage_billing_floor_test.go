//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// settleUsageLogBalance 只在钱包被扣到 reserve 保留线（BalanceShortfall > 0）时
// 介入：把 usage_log.ActualCost 改写为实收金额，保证账本一致
// （sum(actual_cost) == 实际扣减）；全额收取、订阅计费、无结果时一律不动。
func TestSettleUsageLogBalance(t *testing.T) {
	newBalance := 0.10
	base := func() (*UsageLog, *postUsageBillingParams) {
		return &UsageLog{ActualCost: 0.75}, &postUsageBillingParams{
			Cost: &CostBreakdown{ActualCost: 0.75},
			User: &User{ID: 7},
		}
	}

	t.Run("partial collection rewrites ActualCost to collected", func(t *testing.T) {
		usageLog, p := base()
		settleUsageLogBalance("req-1", usageLog, p, &UsageBillingApplyResult{
			Applied:          true,
			NewBalance:       &newBalance,
			BalanceCollected: 0.20,
			BalanceShortfall: 0.55,
		})
		require.InDelta(t, 0.20, usageLog.ActualCost, 1e-9)
	})

	t.Run("full collection leaves ActualCost untouched", func(t *testing.T) {
		usageLog, p := base()
		settleUsageLogBalance("req-2", usageLog, p, &UsageBillingApplyResult{
			Applied:          true,
			NewBalance:       &newBalance,
			BalanceCollected: 0.75,
		})
		require.InDelta(t, 0.75, usageLog.ActualCost, 1e-9)
	})

	t.Run("legacy result without collection detail leaves ActualCost untouched", func(t *testing.T) {
		usageLog, p := base()
		settleUsageLogBalance("req-3", usageLog, p, &UsageBillingApplyResult{Applied: true})
		require.InDelta(t, 0.75, usageLog.ActualCost, 1e-9)
	})

	t.Run("subscription billing never rewrites ActualCost", func(t *testing.T) {
		usageLog, p := base()
		p.IsSubscriptionBill = true
		settleUsageLogBalance("req-4", usageLog, p, &UsageBillingApplyResult{
			Applied:          true,
			BalanceCollected: 0.20,
			BalanceShortfall: 0.55,
		})
		require.InDelta(t, 0.75, usageLog.ActualCost, 1e-9)
	})

	t.Run("nil result / nil usage log are safe", func(t *testing.T) {
		_, p := base()
		settleUsageLogBalance("req-5", nil, p, nil)
		settleUsageLogBalance("req-6", nil, p, &UsageBillingApplyResult{BalanceShortfall: 0.1})
	})
}

// 低余额邮件用 old - collected 还原扣费前后的余额：钱包被扣到保留线时
// old 必须是 floor + 实收，而不是 floor + 全额成本（否则会高估旧余额、
// 并把新余额算到保留线以下）。
func TestResolveOldBalance_UsesCollectedAmountWhenDrainedToFloor(t *testing.T) {
	floor := 0.10
	p := &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: 0.75},
		User: &User{ID: 7, Balance: 9.99}, // 请求上下文里的过期快照，不应被使用
	}

	drained := &UsageBillingApplyResult{
		NewBalance:       &floor,
		BalanceCollected: 0.20,
		BalanceShortfall: 0.55,
	}
	require.InDelta(t, 0.30, resolveOldBalance(p, drained), 1e-9)
	require.InDelta(t, 0.20, collectedBalanceCost(p, drained), 1e-9)
	// old - collected 落在保留线上，正好触发 “最后可用额度” 提醒判定。
	require.InDelta(t, floor, resolveOldBalance(p, drained)-collectedBalanceCost(p, drained), 1e-9)

	full := &UsageBillingApplyResult{NewBalance: &floor, BalanceCollected: 0.75}
	require.InDelta(t, 0.85, resolveOldBalance(p, full), 1e-9)
	require.InDelta(t, 0.75, collectedBalanceCost(p, full), 1e-9)

	// 旧式结果（没有 collected 信息）按全额收取处理，保持既有行为。
	legacy := &UsageBillingApplyResult{NewBalance: &floor}
	require.InDelta(t, 0.85, resolveOldBalance(p, legacy), 1e-9)
	require.InDelta(t, 0.75, collectedBalanceCost(p, legacy), 1e-9)

	// 没有事务结果 → 回退请求快照。
	require.InDelta(t, 9.99, resolveOldBalance(p, nil), 1e-9)
	require.InDelta(t, 0.75, collectedBalanceCost(p, nil), 1e-9)
}

// balanceLowNotifyDecision 与 floor 语义配合：扣到保留线（new == reserve）
// 且扣费前在保留线之上时，即使用户没配阈值也要补发 “最后可用额度” 邮件。
func TestBalanceLowNotifyDecision_FiresWhenDrainedExactlyToReserve(t *testing.T) {
	threshold, send := balanceLowNotifyDecision(false, 0, 0.10, 0.30, 0.10)
	require.True(t, send)
	require.InDelta(t, 0.10, threshold, 1e-9)

	// 已经在保留线上、再次被拒（old == new == reserve）不重复发。
	_, send = balanceLowNotifyDecision(false, 0, 0.10, 0.10, 0.10)
	require.False(t, send)
}
