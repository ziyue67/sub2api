//go:build unit

package service

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheCreationBillingFlag(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI} {
		for _, typ := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
			for _, flag := range []any{nil, false, true, "true", 1} {
				a := &Account{Platform: platform, Type: typ, Extra: map[string]any{CacheCreationBilling1hKey: flag}}
				require.Equal(t, platform == PlatformAnthropic && typ == AccountTypeAPIKey && flag == true, a.IsCacheCreationBilling1hEnabled())
			}
		}
	}
	var missing *Account
	require.False(t, missing.IsCacheCreationBilling1hEnabled())
}

func TestPrepareCacheCreationBilling(t *testing.T) {
	for _, tt := range []struct {
		name              string
		total, five, hour int
		status            string
	}{
		{"five", 100, 100, 0, "converted"},
		{"mixed", 150, 100, 50, "converted"},
		{"hour", 100, 0, 100, "unchanged"},
		{"read_only", 0, 0, 0, "unchanged"},
		{"unknown", 100, 0, 0, "missing_breakdown"},
		{"mismatch", 100, 100, 20, "inconsistent_usage"},
		{"negative", 100, -1, 101, "invalid_usage"},
		{"overflow", math.MaxInt, math.MaxInt, 1, "invalid_usage"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := &ForwardResult{CacheCreationBilling1h: true, Usage: ClaudeUsage{
				InputTokens: 4, OutputTokens: 503, CacheReadInputTokens: 700,
				CacheCreationInputTokens: tt.total, CacheCreation5mTokens: tt.five, CacheCreation1hTokens: tt.hour,
			}}
			before := *raw
			billed, audit := prepareCacheCreationBilling(raw)
			require.Equal(t, tt.status, audit.Status)
			require.Equal(t, before, *raw)
			require.Equal(t, 700, billed.Usage.CacheReadInputTokens)
			if tt.status == "converted" {
				require.Zero(t, billed.Usage.CacheCreation5mTokens)
				require.Equal(t, tt.total, billed.Usage.CacheCreation1hTokens)
				require.Equal(t, tt.five, audit.ConvertedTokens)
			}
			second, _ := prepareCacheCreationBilling(billed)
			require.Equal(t, billed.Usage, second.Usage)
			raw.CacheCreationBilling1h = false
			unchanged, disabled := prepareCacheCreationBilling(raw)
			require.Nil(t, disabled)
			require.Equal(t, raw.Usage, unchanged.Usage)
		})
	}
}

func TestRecordUsageCacheCreationBilling(t *testing.T) {
	for _, rate := range []float64{0, 0.8, 0.85} {
		for _, subscription := range []bool{false, true} {
			t.Run(fmt.Sprintf("rate=%g/sub=%t", rate, subscription), func(t *testing.T) {
				repo := &openAIRecordUsageLogRepoStub{inserted: true}
				billing := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
				svc := newGatewayRecordUsageServiceWithBillingRepoForTest(repo, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
				groupID := int64(77)
				group := &Group{ID: groupID, RateMultiplier: rate, Platform: PlatformAnthropic}
				var sub *UserSubscription
				if subscription {
					group.SubscriptionType = "subscription"
					sub = &UserSubscription{ID: 4}
				}
				accountRate := 0.7
				account := &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, RateMultiplier: &accountRate, Extra: map[string]any{"quota_limit": float64(100)}}
				result := &ForwardResult{RequestID: "cache-billing", Model: "claude-opus-5-5", CacheCreationBilling1h: true, Usage: ClaudeUsage{InputTokens: 4, OutputTokens: 503, CacheCreationInputTokens: 90761, CacheCreation5mTokens: 90761}}
				original := result.Usage
				input := &RecordUsageInput{Result: result, APIKey: &APIKey{ID: 1, GroupID: &groupID, Group: group}, User: &User{ID: 2}, Account: account, Subscription: sub, PricingAt: time.Now()}
				quote := svc.QuoteUsageCost(context.Background(), input)
				require.NoError(t, svc.RecordUsage(context.Background(), input))
				require.Equal(t, original, result.Usage, "shared result must stay raw")
				log := repo.lastLog
				require.NotNil(t, log.CacheCreationBilling)
				require.Equal(t, "converted", log.CacheCreationBilling.Status)
				require.Equal(t, 90761, log.CacheCreation1hTokens)
				require.Zero(t, log.CacheCreation5mTokens)
				require.False(t, log.CacheTTLOverridden, "legacy flag must not leak conversion to user DTO")
				require.InDelta(t, quote, log.ActualCost, 1e-10)
				require.InDelta(t, (90761*8e-6+4*4e-6+503*20e-6)*rate, log.ActualCost, 1e-10)
				require.InDelta(t, log.TotalCost*rate, log.ActualCost, 1e-10)
				require.Greater(t, log.CacheCreationBilling.BilledCreationCost, log.CacheCreationBilling.OriginalCreationCost)
				require.NotNil(t, log.AccountStatsCost)
				require.Less(t, *log.AccountStatsCost, log.TotalCost)
				cmd := billing.lastCmd
				require.InDelta(t, *log.AccountStatsCost*0.7, cmd.AccountQuotaCost, 1e-8)
				if subscription {
					require.InDelta(t, log.ActualCost, cmd.SubscriptionCost, 1e-8)
					require.Zero(t, cmd.BalanceCost)
				} else {
					require.InDelta(t, log.ActualCost, cmd.BalanceCost, 1e-8)
				}
			})
		}
	}
}

func TestCacheCreationBillingPricingFallbackAndCustomRates(t *testing.T) {
	for _, tt := range []struct {
		name, model, status string
		pricing             *ChannelModelPricing
		wantCreation        float64
	}{
		{name: "unknown_model", model: "unpriced-test-model", status: "unavailable_pricing"},
		{name: "free_creation", model: "claude-opus-5-5", status: "unavailable_pricing", pricing: &ChannelModelPricing{CacheWritePrice: testPtrFloat64(0), CacheWrite1hPrice: testPtrFloat64(0)}},
		{name: "per_request", model: "claude-opus-5-5", status: "unavailable_pricing", pricing: &ChannelModelPricing{BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(0.04)}},
		{name: "custom_rates", model: "claude-opus-5-5", status: "converted", pricing: &ChannelModelPricing{CacheWritePrice: testPtrFloat64(2e-6), CacheWrite1hPrice: testPtrFloat64(9e-6)}, wantCreation: 150 * 9e-6},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &openAIRecordUsageLogRepoStub{inserted: true}
			billing := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newGatewayRecordUsageServiceWithBillingRepoForTest(repo, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
			group := &Group{ID: 77, Platform: PlatformAnthropic, RateMultiplier: 0.85}
			if tt.pricing != nil {
				pricing := *tt.pricing
				pricing.Models = []string{tt.model}
				group.ModelPricing = []ChannelModelPricing{pricing}
			}
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			raw := ClaudeUsage{CacheCreationInputTokens: 150, CacheCreation5mTokens: 100, CacheCreation1hTokens: 50}
			result := &ForwardResult{RequestID: "pricing-test", Model: tt.model, CacheCreationBilling1h: true, Usage: raw}
			input := &RecordUsageInput{Result: result, APIKey: &APIKey{ID: 1, GroupID: &group.ID, Group: group}, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}}
			quote := svc.QuoteUsageCost(context.Background(), input)
			require.NoError(t, svc.RecordUsage(context.Background(), input))
			log := repo.lastLog
			require.Equal(t, raw, result.Usage)
			require.Equal(t, tt.status, log.CacheCreationBilling.Status)
			require.InDelta(t, quote, log.ActualCost, 1e-10)
			if tt.status == "converted" {
				require.Equal(t, 150, log.CacheCreation1hTokens)
				require.Zero(t, log.CacheCreation5mTokens)
				require.InDelta(t, tt.wantCreation, log.CacheCreationCost, 1e-10)
				require.InDelta(t, 100*(9e-6-2e-6)*0.85, log.CacheCreationBilling.ActualCostDelta, 1e-10)
			} else {
				require.Equal(t, 100, log.CacheCreation5mTokens)
				require.Equal(t, 50, log.CacheCreation1hTokens)
				require.Zero(t, log.CacheCreationBilling.ConvertedTokens)
				require.Zero(t, log.CacheCreationBilling.ActualCostDelta)
			}
		})
	}
}
