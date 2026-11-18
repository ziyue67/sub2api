//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompatibleGroupFailoverBillsEffectiveGroup(t *testing.T) {
	keys, original := unifiedRoutingFixture()
	keys.groupRepo.(multiGroupRepoStub).groups[3].Platform = PlatformDeepseek
	selected, err := keys.SelectRequestGroup(context.Background(), original, []string{"gpt-5.2"}, "token", nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, int64(3), *selected.GroupID)
	ctx := WithOpenAITextGroupFailover(context.Background(), []byte(`{"input":"hello"}`))
	switched, err := keys.ApplyNextGroupRoute(ctx, selected, map[int64]struct{}{3: {}})
	require.NoError(t, err)
	require.True(t, switched)
	require.Equal(t, int64(2), *selected.GroupID)

	usage := &openAIRecordUsageLogRepoStub{inserted: true}
	users := &openAIRecordUsageUserRepoStub{}
	accounting := newOpenAIRecordUsageServiceForTest(usage, users, &openAIRecordUsageSubRepoStub{}, nil)
	expected, err := accounting.billingService.CalculateCost("gpt-5.2", UsageTokens{InputTokens: 100, OutputTokens: 20}, 0.5)
	require.NoError(t, err)
	err = accounting.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "mixed-provider-billing", Model: "gpt-5.2", UpstreamModel: "gpt-5.2", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20}, Duration: time.Second},
		APIKey: selected, User: selected.User, Account: &Account{ID: 102, Platform: PlatformOpenAI},
	})
	require.NoError(t, err)
	require.Equal(t, 1, users.deductCalls)
	require.InDelta(t, QuantizeUsageBillingAmount(expected.ActualCost), users.lastAmount, 1e-10)
	require.NotNil(t, usage.lastLog)
	require.Equal(t, int64(2), *usage.lastLog.GroupID)
	require.Equal(t, int64(102), usage.lastLog.AccountID)
	require.Equal(t, 0.5, usage.lastLog.RateMultiplier, "must not use original DeepSeek group's 0.2 multiplier")
	require.Equal(t, QuantizeUsageBillingAmount(expected.ActualCost), usage.lastLog.ActualCost)
	require.Equal(t, int64(1), *original.GroupID, "shared authentication snapshot remains unchanged")
}
