//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUnifiedRoutingActualBillingAfterFailover(t *testing.T) {
	ctx := context.Background()
	router, key := unifiedRoutingFixture()
	selected, err := router.SelectRequestGroup(ctx, key, []string{"gpt-5.4"}, "token", nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, int64(3), *selected.GroupID)
	ok, err := router.ApplyNextGroupRoute(ctx, selected, map[int64]struct{}{3: {}})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(2), *selected.GroupID)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	billing := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	expected, err := billing.billingService.CalculateCost("gpt-5.4", UsageTokens{InputTokens: 100, OutputTokens: 20}, 0.5)
	require.NoError(t, err)
	input := &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "unified-billing", Model: "gpt-5.4", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20}, Duration: time.Second},
		APIKey: selected, User: selected.User, Account: &Account{ID: 30},
	}
	require.NoError(t, billing.RecordUsage(ctx, input))
	require.Equal(t, int64(2), *usageRepo.lastLog.GroupID)
	require.Equal(t, 0.5, usageRepo.lastLog.RateMultiplier)
	require.Equal(t, QuantizeUsageBillingAmount(expected.ActualCost), usageRepo.lastLog.ActualCost)
	require.Equal(t, usageRepo.lastLog.ActualCost, userRepo.lastAmount)
	require.Equal(t, 1, userRepo.deductCalls)
	// Production uses the atomic billing repository, not the legacy fallback
	// above. Verify routed requests retain that repository's duplicate boundary.
	ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	billing.usageBillingRepo = ledger
	require.NoError(t, billing.RecordUsage(ctx, input))
	require.Equal(t, QuantizeUsageBillingAmount(expected.ActualCost), ledger.lastCmd.BalanceCost)
	ledger.result = &UsageBillingApplyResult{Applied: false}
	require.NoError(t, billing.RecordUsage(ctx, input))
	require.Equal(t, 2, ledger.calls)
	require.Equal(t, 1, userRepo.deductCalls, "atomic billing must not invoke the legacy balance debit")
}
