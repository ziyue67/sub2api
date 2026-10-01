package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func modelBillingSettings(t *testing.T, cfg ModelBillingConfig) *SettingService {
	t.Helper()
	c := DefaultOAuthAutoConfig()
	c.ModelBilling = cfg
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	return NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil)
}

func TestModelBillingDefaultsPersistenceAndLegacy(t *testing.T) {
	repo := &accountOpsSettingsStub{raw: "{}"}
	c, err := GetOAuthAutoConfig(t.Context(), repo)
	require.NoError(t, err)
	require.Equal(t, DefaultModelBillingConfig(), c.ModelBilling)
	require.False(t, c.ModelBilling.Enabled)
	require.Equal(t, 10.0, c.ModelBilling.Rules[0].Multiplier)
	c.ModelBilling.Enabled = true
	c.ModelMappings = []OAuthModelMappingRule{{From: "gpt-5.4", To: "gpt-6-luna"}}
	c.ModelBilling.Rules = append(c.ModelBilling.Rules, ModelBillingRule{Model: "custom-mini", Multiplier: 2.5})
	svc := NewAccountOpsService(repo, nil, nil)
	svc.autoGroups = autoConfigGroups{}
	_, err = svc.SaveOAuthAutoConfig(t.Context(), c)
	require.NoError(t, err)
	loaded, err := GetOAuthAutoConfig(t.Context(), repo)
	require.NoError(t, err)
	require.Equal(t, c.ModelBilling, loaded.ModelBilling)
	require.Equal(t, c.ModelMappings, loaded.ModelMappings)
	require.False(t, loaded.Enabled)
	require.False(t, loaded.UpgradeEnabled)
}

func TestModelBillingValidation(t *testing.T) {
	for _, rule := range []ModelBillingRule{
		{"", 10}, {"*", 10}, {"lu*na", 10}, {"luna?", 10}, {"luna x", 10},
		{strings.Repeat("a", 201), 10}, {"luna", 0}, {"luna", -1}, {"luna", 0.9},
		{"luna", 1001}, {"luna", math.Inf(1)}, {"luna", math.NaN()},
	} {
		require.Error(t, validateModelBillingConfig(ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{rule}}))
	}
	require.Error(t, validateModelBillingConfig(ModelBillingConfig{Enabled: true}))
	require.Error(t, validateModelBillingConfig(ModelBillingConfig{Rules: make([]ModelBillingRule, 101)}))
	require.Error(t, validateModelBillingConfig(ModelBillingConfig{Rules: []ModelBillingRule{{"Luna", 10}, {" luna ", 2}}}))
	require.NoError(t, validateModelBillingConfig(ModelBillingConfig{}))
	require.NoError(t, validateModelBillingConfig(ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{{"vendor/luna*", 1.5}, {"luna", 1000}}}))
}

func TestModelBillingMatching(t *testing.T) {
	c := ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{{"gpt-*", 2}, {"gpt-6-luna*", 10}, {"gpt-6-luna-low", 3}}}
	for model, want := range map[string]float64{"gpt-6-luna": 10, " GPT-6-LUNA-HIGH ": 10, "gpt-6-luna-low": 3, "gpt-6-astra": 2, "claude-sonnet": 1, "alias-luna": 1} {
		require.Equal(t, want, c.multiplier(model), model)
	}
	c.Enabled = false
	require.Equal(t, 1.0, c.multiplier("gpt-6-luna"))
}

type modelBillingRepoStub struct {
	SettingRepository
	raw   string
	err   error
	calls int
}

func (s *modelBillingRepoStub) GetValue(context.Context, string) (string, error) {
	s.calls++
	return s.raw, s.err
}

func TestModelBillingCacheRefreshFailuresAndRequestSnapshot(t *testing.T) {
	repo := &modelBillingRepoStub{raw: "{\"model_billing\":{\"enabled\":true,\"rules\":[{\"model\":\"gpt-6-luna*\",\"multiplier\":10}]}}"}
	svc := NewSettingService(repo, nil)
	ctx := withModelBillingConfig(t.Context(), svc)
	require.Equal(t, 10.0, svc.modelBillingConfigForUsage(ctx).multiplier("gpt-6-luna"))
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { _ = svc.modelBillingConfigForUsage(t.Context()) })
	}
	wg.Wait()
	require.Equal(t, 1, repo.calls)
	repo.err = errors.New("unavailable")
	svc.modelBillingCache.expires = time.Time{}
	require.Equal(t, 10.0, svc.modelBillingConfigForUsage(t.Context()).multiplier("gpt-6-luna"))
	require.Equal(t, 2, repo.calls)
	repo.err, repo.raw = nil, "{}"
	svc.modelBillingCache.expires = time.Time{}
	require.Equal(t, 1.0, svc.modelBillingConfigForUsage(t.Context()).multiplier("gpt-6-luna"))
	require.Equal(t, 10.0, svc.modelBillingConfigForUsage(ctx).multiplier("gpt-6-luna"))
	repo.err = errors.New("unavailable")
	cold := NewSettingService(repo, nil)
	require.Equal(t, 1.0, cold.modelBillingConfigForUsage(t.Context()).multiplier("gpt-6-luna"))
}

func TestModelBillingCustomerCostOnly(t *testing.T) {
	cfg := DefaultModelBillingConfig()
	cfg.Enabled = true
	for _, amount := range []float64{0, .2} {
		cost := &CostBreakdown{InputCost: .4, OutputCost: .5, CacheReadCost: .1, TotalCost: 1, ActualCost: amount, BillingMode: string(BillingModeToken)}
		applyModelBillingMultiplier(cost, cfg, "gpt-6-luna")
		require.Equal(t, amount*10, cost.ActualCost)
		require.Equal(t, 1.0, cost.TotalCost)
		require.Equal(t, .1, cost.CacheReadCost)
		require.Equal(t, .4, cost.InputCost)
		require.Equal(t, 10.0, costModelBillingMultiplier(cost))
	}
	for _, mode := range []BillingMode{BillingModeImage, BillingModePerRequest, BillingModeVideo} {
		cost := &CostBreakdown{ActualCost: 1, BillingMode: string(mode)}
		applyModelBillingMultiplier(cost, cfg, "gpt-6-luna")
		require.Equal(t, 1.0, cost.ActualCost)
		require.Equal(t, 1.0, costModelBillingMultiplier(cost))
	}
	require.False(t, responseModelBillingAdoptable(&CostBreakdown{TotalCost: 1, ActualCost: .2}, &CostBreakdown{TotalCost: .5, ActualCost: 1}, false, false))
}

type modelBillingSubscriptionRepo struct {
	openAIRecordUsageSubRepoStub
	lastAmount float64
}

func (s *modelBillingSubscriptionRepo) IncrementUsage(ctx context.Context, id int64, amount float64) error {
	s.lastAmount = amount
	return s.openAIRecordUsageSubRepoStub.IncrementUsage(ctx, id, amount)
}

func TestModelBillingOpenAIRecordUsageBalanceAndSubscription(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		for _, multiplier := range []float64{0, .2} {
			logRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &modelBillingSubscriptionRepo{}
			svc := newOpenAIRecordUsageServiceForTest(logRepo, userRepo, subRepo, nil)
			svc.StopOpenAICodexTicketHarvester() // Billing fixtures must not run background ticket work.
			cfg := DefaultModelBillingConfig()
			cfg.Enabled = true
			svc.settingService = modelBillingSettings(t, cfg)
			accountRate, accountGroupRate := .3, 2.0
			group := &Group{ID: 2, RateMultiplier: multiplier}
			input := &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{Model: "gpt-6-luna", Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 200, CacheReadInputTokens: 100}, Duration: time.Second},
				APIKey: &APIKey{ID: 1, GroupID: &group.ID, Group: group}, User: &User{ID: 3},
				Account: &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, RateMultiplier: &accountRate, GroupRateMultiplier: &accountGroupRate},
			}
			if subscription {
				group.SubscriptionType = SubscriptionTypeSubscription
				input.Subscription = &UserSubscription{ID: 8}
			}
			base := expectedOpenAICost(t, svc, "gpt-6-luna", input.Result.Usage, multiplier*accountGroupRate)
			require.NoError(t, svc.RecordUsage(t.Context(), input))
			require.NotNil(t, logRepo.lastLog)
			require.InDelta(t, base.ActualCost*10, logRepo.lastLog.ActualCost, 1e-12)
			require.InDelta(t, base.TotalCost, logRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, multiplier*accountGroupRate*10, logRepo.lastLog.RateMultiplier, 1e-12)
			require.Equal(t, accountRate, *logRepo.lastLog.AccountRateMultiplier)
			if subscription {
				require.InDelta(t, base.ActualCost*10, subRepo.lastAmount, 1e-12)
			} else {
				require.InDelta(t, base.ActualCost*10, userRepo.lastAmount, 1e-12)
			}
		}
	}
}

func TestModelBillingGenericTokenAndSearchCharges(t *testing.T) {
	cfg := ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{{"claude-sonnet-4-5*", 10}}}
	svc := &GatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &ForwardResult{Model: "claude-sonnet-4-5", Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 100}, SearchCount: 2}
	key := &APIKey{ID: 1}
	base := svc.calculateRecordUsageCost(t.Context(), result, key, result.Model, .2, .2, time.Time{})
	svc.settingService = modelBillingSettings(t, cfg)
	cost := svc.calculateRecordUsageCost(t.Context(), result, key, result.Model, .2, .2, time.Time{})
	search := svc.billingService.CalculateSearchCost(2, nil, .2)
	require.InDelta(t, (base.ActualCost-search.ActualCost)*10+search.ActualCost, cost.ActualCost, 1e-12)
	require.InDelta(t, base.TotalCost, cost.TotalCost, 1e-12)
	require.Equal(t, 10.0, costModelBillingMultiplier(cost))
}

func TestModelBillingUsesPricedFallbackAndPreservesSearchSurcharge(t *testing.T) {
	svc := newOpenAIRecordUsageServiceForTest(nil, nil, nil, nil)
	svc.StopOpenAICodexTicketHarvester() // Billing fixtures must not run background ticket work.
	cfg := DefaultModelBillingConfig()
	cfg.Enabled = true
	svc.settingService = modelBillingSettings(t, cfg)
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 100}
	result := &OpenAIForwardResult{Model: "custom-alias", SearchCount: 2}
	key := &APIKey{ID: 1}
	base, err := svc.billingService.CalculateCost("gpt-6-luna", tokens, .2)
	require.NoError(t, err)
	search := svc.billingService.CalculateSearchCost(2, nil, .2)
	cost, err := svc.calculateOpenAIRecordUsageCost(t.Context(), result, key,
		[]string{"no-price-test-model", "gpt-6-luna"}, .2, .2, .2, .2, tokens, "", nil, time.Time{})
	require.NoError(t, err)
	require.InDelta(t, base.ActualCost*10+search.ActualCost, cost.ActualCost, 1e-12)
	require.InDelta(t, base.TotalCost+search.TotalCost, cost.TotalCost, 1e-12)
	require.Equal(t, 10.0, costModelBillingMultiplier(cost))
}

func TestModelBillingFreeFastChargesOnceAndBillsAtomicCommand(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		logs := &openAIRecordUsageLogRepoStub{inserted: true}
		billing := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
		svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
		svc.StopOpenAICodexTicketHarvester() // Billing fixtures must not run background ticket work.
		cfg := DefaultModelBillingConfig()
		cfg.Enabled = true
		svc.settingService = modelBillingSettings(t, cfg)
		svc.resolver = NewModelPricingResolver(nil, svc.billingService)
		inputPrice, outputPrice, fastMultiplier := .001, .002, 3.0
		groupID := int64(77)
		key := &APIKey{ID: 1, GroupID: &groupID, Quota: 100, Group: &Group{
			ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
			RateMultiplier: .2, FreeOpenAIFast: true,
			ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-6-luna"}, BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice, FastMultiplier: &fastMultiplier}},
		}}
		tier := "priority"
		input := &OpenAIRecordUsageInput{
			Result: &OpenAIForwardResult{RequestID: "model-billing-free-fast", Model: "gpt-6-luna", ServiceTier: &tier, Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 50}, Duration: time.Second},
			APIKey: key, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
			APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
		}
		if subscription {
			key.Group.SubscriptionType = SubscriptionTypeSubscription
			input.Subscription = &UserSubscription{ID: 9}
		}
		require.NoError(t, svc.RecordUsage(t.Context(), input))
		standard := 100*inputPrice + 50*outputPrice
		require.InDelta(t, standard*fastMultiplier, logs.lastLog.TotalCost, 1e-12)
		require.InDelta(t, standard*.2*10, logs.lastLog.ActualCost, 1e-12)
		require.Equal(t, 2.0, logs.lastLog.RateMultiplier)
		require.NotNil(t, billing.lastCmd)
		require.InDelta(t, standard*.2*10, billing.lastCmd.APIKeyQuotaCost, 1e-12)
		if subscription {
			require.InDelta(t, standard*.2*10, billing.lastCmd.SubscriptionCost, 1e-12)
			require.Zero(t, billing.lastCmd.BalanceCost)
		} else {
			require.InDelta(t, standard*.2*10, billing.lastCmd.BalanceCost, 1e-12)
			require.Zero(t, billing.lastCmd.SubscriptionCost)
		}
	}
}
