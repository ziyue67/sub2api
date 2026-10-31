package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTKSecurityFlexSettlement(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		logs := &openAIRecordUsageLogRepoStub{inserted: true}
		users := &openAIRecordUsageUserRepoStub{}
		svc := newOpenAIRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{}, nil)
		svc.resolver = NewModelPricingResolver(nil, svc.billingService)
		input, output, flex := 0.01, 0.02, 0.4
		price := ChannelModelPricing{Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken, InputPrice: &input, OutputPrice: &output}
		expected, tier := 30.0, "default"
		if explicit {
			price.FlexMultiplier = &flex
			expected, tier = 12, "flex"
		}
		requested := "flex"
		err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: &OpenAIForwardResult{RequestID: "fixture-flex-settlement", Model: "gpt-5.4", ServiceTier: &requested, Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 1000}},
			APIKey: &APIKey{ID: 2, GroupID: i64p(7), Group: &Group{ID: 7, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{price}}},
			User:   &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		})
		require.NoError(t, err)
		require.Equal(t, 1, logs.calls)
		require.Equal(t, 1, users.deductCalls)
		require.Equal(t, tier, *logs.lastLog.ServiceTier)
		require.InDelta(t, expected, logs.lastLog.ActualCost, 1e-9)
		require.InDelta(t, expected, users.lastAmount, 1e-9)
	}
}

func TestTKSecurityUnconfirmedFlex(t *testing.T) {
	for _, observed := range []string{"", "default", "unknown"} {
		t.Run(observed, func(t *testing.T) {
			result := ResolveOpenAIServiceTierBilling(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "flex", observed)
			require.NotEqual(t, "flex", result.Billing, "an unconfirmed client-selected discount must not become a billable flex tier")
		})
	}
	require.Equal(t, "flex", ResolveOpenAIServiceTierBilling(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "flex", "flex").Billing)
}

func TestTKSecurityResponseModelUsesItsOwnFlexPrice(t *testing.T) {
	for _, responseExplicit := range []bool{false, true} {
		logs := &openAIRecordUsageLogRepoStub{inserted: true}
		users := &openAIRecordUsageUserRepoStub{}
		svc := newOpenAIRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{}, nil)
		svc.resolver = NewModelPricingResolver(nil, svc.billingService)
		baseInput, baseOutput, responseInput, responseOutput, flex := 0.1, 0.2, 0.01, 0.02, 0.4
		baseline := ChannelModelPricing{Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken, InputPrice: &baseInput, OutputPrice: &baseOutput}
		response := ChannelModelPricing{Models: []string{"gpt-5.1"}, BillingMode: BillingModeToken, InputPrice: &responseInput, OutputPrice: &responseOutput}
		expected, tier := 30.0, "default"
		if responseExplicit {
			response.FlexMultiplier = &flex
			expected, tier = 12, "flex"
		} else {
			baseline.FlexMultiplier = &flex
		}
		requested := "flex"
		err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: &OpenAIForwardResult{RequestID: "fixture-response-flex", Model: "gpt-5.4", UpstreamResponseModel: "gpt-5.1", ServiceTier: &requested, Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 1000}},
			APIKey: &APIKey{ID: 2, GroupID: i64p(7), Group: &Group{ID: 7, RateMultiplier: 1, ModelPricing: []ChannelModelPricing{baseline, response}}},
			User:   &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			ChannelUsageFields: ChannelUsageFields{OriginalModel: "gpt-5.4", ChannelMappedModel: "gpt-5.4", BillingModelSource: BillingModelSourceResponse},
		})
		require.NoError(t, err)
		require.Equal(t, tier, *logs.lastLog.ServiceTier)
		require.InDelta(t, expected, logs.lastLog.ActualCost, 1e-9)
		require.InDelta(t, expected, users.lastAmount, 1e-9)
	}
}

func TestTKSecurityFlexPricingAndSettledTier(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		requested := "flex"
		result := &OpenAIForwardResult{ServiceTier: &requested}
		pricing := &ModelPricing{InputPricePerToken: 0.01, OutputPricePerToken: 0.02}
		if explicit {
			v := 0.4
			pricing.FlexMultiplier = &v
		}
		resolution := ApplyOpenAIServiceTierBillingResolution(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, result, pricing)
		cost := (&BillingService{}).computeTokenBreakdown(pricing, UsageTokens{InputTokens: 1000, OutputTokens: 1000}, 1, *result.ServiceTier, false)
		if explicit {
			require.Equal(t, "flex", resolution.Billing)
			require.InDelta(t, 12, cost.ActualCost, 1e-9)
		} else {
			require.Equal(t, "default", resolution.Billing)
			require.InDelta(t, 30, cost.ActualCost, 1e-9)
		}
	}
}
