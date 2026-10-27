package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestModelBillingGroupScopeMatching(t *testing.T) {
	c := ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{
		{Model: "gpt-*", Multiplier: 2},
		{Model: "gpt-6-luna*", Multiplier: 10, GroupIDs: []int64{5, 6}},
		{Model: "gpt-6-luna-low", Multiplier: 3, GroupIDs: []int64{7}},
	}}
	for _, tc := range []struct {
		group int64
		want  float64
	}{{5, 10}, {6, 10}, {7, 3}, {8, 2}, {0, 2}, {-1, 2}} {
		require.Equal(t, tc.want, c.multiplier(" GPT-6-LUNA-LOW ", &tc.group))
	}
	require.Equal(t, 2.0, c.multiplier("gpt-6-luna-low", nil))
	c.Rules = c.Rules[1:]
	require.Equal(t, 1.0, c.multiplier("gpt-6-luna-low", nil))
	group := int64(8)
	require.Equal(t, 1.0, c.multiplier("gpt-6-luna-low", &group))
	c.Enabled = false
	group = 5
	require.Equal(t, 1.0, c.multiplier("gpt-6-luna-low", &group))
}

func TestModelBillingGroupJSONCompatibility(t *testing.T) {
	c := ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{
		{Model: "gpt-*", Multiplier: 2, GroupIDs: []int64{5}},
		{Model: "luna", Multiplier: 10, GroupIDs: []int64{6}},
	}}
	// Older clients may reorder rules. Scope follows model identity, never array index.
	require.NoError(t, json.Unmarshal([]byte(`{"rules":[{"model":" LUNA ","multiplier":3},{"model":"new","multiplier":2,"group_ids":[]},{"model":"gpt-*","multiplier":4}]}`), &c))
	require.Error(t, json.Unmarshal([]byte(`{"rules":[{"model":"renamed","multiplier":3}]}`), &c))
	require.True(t, c.Enabled)
	require.Equal(t, []int64{6}, c.Rules[0].GroupIDs)
	require.Empty(t, c.Rules[1].GroupIDs)
	require.Equal(t, []int64{5}, c.Rules[2].GroupIDs)
	require.NoError(t, json.Unmarshal([]byte(`{"rules":[{"model":"luna","multiplier":3,"group_ids":[]}]}`), &c))
	require.Empty(t, c.Rules[0].GroupIDs)
	require.NoError(t, json.Unmarshal([]byte(`{"rules":[{"model":"luna","multiplier":3,"group_ids":[5,6,5]}]}`), &c))
	require.Equal(t, []int64{5, 6}, c.Rules[0].GroupIDs)
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	var loaded ModelBillingConfig
	require.NoError(t, json.Unmarshal(raw, &loaded))
	require.Equal(t, c, loaded)
	for _, ids := range []string{`[0]`, `[-1]`, `[1.5]`, `["5"]`, `[9007199254740992]`, `{}`} {
		var invalid ModelBillingConfig
		err := json.Unmarshal([]byte(`{"rules":[{"model":"luna","multiplier":3,"group_ids":`+ids+`}]}`), &invalid)
		if err == nil {
			err = validateModelBillingConfig(invalid)
		}
		require.Error(t, err, ids)
	}
	for _, field := range []string{"", `,"group_ids":[]`, `,"group_ids":null`} {
		var legacy ModelBillingConfig
		require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"rules":[{"model":"luna","multiplier":3`+field+`}]}`), &legacy))
		require.Equal(t, 3.0, legacy.multiplier("luna", nil))
	}
}

func TestModelBillingGroupCacheAndSnapshot(t *testing.T) {
	repo := &modelBillingRepoStub{raw: `{"model_billing":{"enabled":true,"rules":[{"model":"luna","multiplier":10,"group_ids":[5]}]}}`}
	svc := NewSettingService(repo, nil)
	before := time.Now()
	ctx := withModelBillingConfig(t.Context(), svc)
	require.WithinDuration(t, before.Add(15*time.Second), svc.modelBillingCache.expires, time.Second)
	group5, group6 := int64(5), int64(6)
	repo.raw = `{"model_billing":{"enabled":true,"rules":[{"model":"luna","multiplier":10,"group_ids":[6]}]}}`
	require.Equal(t, 10.0, svc.modelBillingConfigForUsage(t.Context()).multiplier("luna", &group5))
	require.Equal(t, 1, repo.calls)
	svc.modelBillingCache.expires = time.Time{}
	require.Equal(t, 10.0, svc.modelBillingConfigForUsage(t.Context()).multiplier("luna", &group6))
	require.Equal(t, 1.0, svc.modelBillingConfigForUsage(t.Context()).multiplier("luna", &group5))
	require.Equal(t, 10.0, svc.modelBillingConfigForUsage(ctx).multiplier("luna", &group5))
	require.Equal(t, 1.0, svc.modelBillingConfigForUsage(ctx).multiplier("luna", &group6))
	require.Equal(t, 2, repo.calls)
	repo.raw = `{"model_billing":{"enabled":true,"rules":[{"model":"luna","multiplier":10,"group_ids":[0]}]}}`
	svc.modelBillingCache.expires = time.Time{}
	require.Equal(t, 10.0, svc.modelBillingConfigForUsage(t.Context()).multiplier("luna", &group6))
}

func TestModelBillingGroupOpenAIRecordUsage(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		for _, groupID := range []int64{5, 6, 0} {
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			users := &openAIRecordUsageUserRepoStub{}
			subs := &modelBillingSubscriptionRepo{}
			svc := newOpenAIRecordUsageServiceForTest(logs, users, subs, nil)
			svc.StopOpenAICodexTicketHarvester()
			svc.settingService = modelBillingSettings(t, ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{{Model: "gpt-6-luna*", Multiplier: 10, GroupIDs: []int64{5}}}})
			accountRate, accountGroupRate := .3, 2.0
			group := &Group{ID: groupID, RateMultiplier: .2}
			key := &APIKey{ID: 1, GroupID: &group.ID, Group: group}
			if groupID == 0 {
				key.GroupID, key.Group = nil, nil
			}
			input := &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{Model: "gpt-6-luna", Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 200}, SearchCount: 2, Duration: time.Second},
				APIKey: key, User: &User{ID: 3},
				Account: &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: []int64{5, 6}, RateMultiplier: &accountRate, GroupRateMultiplier: &accountGroupRate},
			}
			if subscription {
				group.SubscriptionType = SubscriptionTypeSubscription
				input.Subscription = &UserSubscription{ID: 8}
			}
			rate := .2 * accountGroupRate
			if groupID == 0 {
				rate = svc.cfg.Default.RateMultiplier * accountGroupRate
			}
			base := expectedOpenAICost(t, svc, "gpt-6-luna", input.Result.Usage, rate)
			search := svc.billingService.CalculateSearchCost(2, nil, rate)
			surcharge := 1.0
			if groupID == 5 {
				surcharge = 10
			}
			require.NoError(t, svc.RecordUsage(t.Context(), input))
			want := base.ActualCost*surcharge + search.ActualCost
			require.InDelta(t, want, logs.lastLog.ActualCost, 1e-12)
			require.InDelta(t, base.TotalCost+search.TotalCost, logs.lastLog.TotalCost, 1e-12)
			require.Equal(t, accountRate, *logs.lastLog.AccountRateMultiplier)
			require.Equal(t, key.GroupID, logs.lastLog.GroupID)
			require.InDelta(t, rate*surcharge, logs.lastLog.RateMultiplier, 1e-12)
			if subscription && groupID != 0 {
				require.InDelta(t, want, subs.lastAmount, 1e-12)
			} else {
				require.InDelta(t, want, users.lastAmount, 1e-12)
			}
		}
	}
}

func TestModelBillingGroupGenericSearchAndPerRequest(t *testing.T) {
	svc := &GatewayService{billingService: NewBillingService(nil, nil)}
	result := &ForwardResult{Model: "claude-sonnet-4-5", Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 100}, SearchCount: 2}
	for _, groupID := range []int64{5, 6, 0} {
		key := &APIKey{ID: 1}
		if groupID != 0 {
			key.GroupID = &groupID
			key.Group = &Group{ID: groupID}
		}
		svc.settingService = nil
		base := svc.calculateRecordUsageCost(t.Context(), result, key, result.Model, .2, .2, time.Time{})
		svc.settingService = modelBillingSettings(t, ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{{Model: "claude-sonnet-4-5*", Multiplier: 10, GroupIDs: []int64{5}}}})
		cost := svc.calculateRecordUsageCost(t.Context(), result, key, result.Model, .2, .2, time.Time{})
		search := svc.billingService.CalculateSearchCost(2, nil, .2)
		multiplier := 1.0
		if groupID == 5 {
			multiplier = 10
		}
		require.InDelta(t, (base.ActualCost-search.ActualCost)*multiplier+search.ActualCost, cost.ActualCost, 1e-12)
		require.Equal(t, base.TotalCost, cost.TotalCost)
		perRequest := &CostBreakdown{ActualCost: 1, TotalCost: 1, BillingMode: string(BillingModePerRequest)}
		applyModelBillingMultiplier(perRequest, svc.settingService.modelBillingConfigForUsage(t.Context()), result.Model, key.GroupID)
		require.Equal(t, 1.0, perRequest.ActualCost)
	}
}

func TestModelBillingGroupSavePersistenceAndExistence(t *testing.T) {
	repo := &accountOpsSettingsStub{raw: "{}"}
	svc := NewAccountOpsService(repo, nil, nil)
	svc.autoGroups = autoConfigGroups{group: &Group{ID: 5, Status: StatusActive}}
	cfg := DefaultOAuthAutoConfig()
	cfg.ModelBilling.Enabled = true
	cfg.ModelBilling.Rules[0].GroupIDs = []int64{5, 5}
	saved, err := svc.SaveOAuthAutoConfig(t.Context(), cfg)
	require.NoError(t, err)
	require.Equal(t, []int64{5}, saved.ModelBilling.Rules[0].GroupIDs)
	require.Equal(t, []int64{5, 5}, cfg.ModelBilling.Rules[0].GroupIDs, "normalization must not mutate caller snapshots")
	loaded, err := GetOAuthAutoConfig(t.Context(), repo)
	require.NoError(t, err)
	require.Equal(t, saved.ModelBilling, loaded.ModelBilling)
	svc.autoGroups = autoConfigGroups{err: ErrGroupNotFound}
	loaded.Concurrency = 9
	_, err = svc.SaveOAuthAutoConfig(t.Context(), loaded)
	require.NoError(t, err, "deleted selected IDs are retained when saving unrelated configuration")
	loaded.ModelBilling.Rules[0].GroupIDs = []int64{6}
	_, err = svc.SaveOAuthAutoConfig(t.Context(), loaded)
	require.Error(t, err, "new IDs must exist")
	svc.autoGroups = autoConfigGroups{group: &Group{ID: 6, Status: StatusDisabled}}
	_, err = svc.SaveOAuthAutoConfig(t.Context(), loaded)
	require.Error(t, err, "new IDs must be active")
}

func TestModelBillingGroupGenericRecordUsageCostsAndIdempotency(t *testing.T) {
	for _, groupID := range []int64{5, 6} {
		logs := &openAIRecordUsageLogRepoStub{inserted: true}
		billing := &simpleModeUsageBillingRepoStub{}
		cfg := &config.Config{}
		svc := &GatewayService{cfg: cfg, billingService: NewBillingService(cfg, nil), usageLogRepo: logs, usageBillingRepo: billing, deferredService: &DeferredService{}, billingCacheService: &BillingCacheService{}}
		svc.settingService = modelBillingSettings(t, ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{{Model: "claude-sonnet-4-5*", Multiplier: 10, GroupIDs: []int64{5}}}})
		group := newPeakGroup(true, "14:00", "18:00", 3)
		group.ID, group.RateMultiplier = groupID, .2
		accountRate, accountGroupRate := .3, 2.0
		key := &APIKey{ID: 1, GroupID: &group.ID, Group: group, Quota: 100}
		input := &RecordUsageInput{
			Result: &ForwardResult{RequestID: "scoped-generic-request", Model: "claude-sonnet-4-5", Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 100}, SearchCount: 2},
			APIKey: key, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, GroupIDs: []int64{5, 6}, RateMultiplier: &accountRate, GroupRateMultiplier: &accountGroupRate, Extra: map[string]any{"quota_limit": 100.0}},
			Subscription: &UserSubscription{ID: 8}, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, PricingAt: at(15, 30),
		}
		rate := .2 * accountGroupRate * 3
		base, err := svc.billingService.CalculateCost(input.Result.Model, UsageTokens{InputTokens: 1000, OutputTokens: 100}, rate)
		require.NoError(t, err)
		search := svc.billingService.CalculateSearchCost(2, nil, rate)
		modelRate := 1.0
		if groupID == 5 {
			modelRate = 10
		}
		want := QuantizeUsageBillingAmount(base.ActualCost*modelRate + search.ActualCost)
		for range 2 {
			require.NoError(t, svc.RecordUsage(t.Context(), input))
		}
		require.Len(t, billing.seen, 1, "replayed request keeps the existing billing identity")
		require.Len(t, billing.cmds, 2)
		for _, cmd := range billing.cmds {
			require.InDelta(t, want, cmd.SubscriptionCost, 1e-12)
			require.InDelta(t, want, cmd.APIKeyQuotaCost, 1e-12)
			require.InDelta(t, (base.TotalCost+search.TotalCost)*accountRate, cmd.AccountQuotaCost, 1e-8)
			require.Zero(t, cmd.BalanceCost)
		}
		require.InDelta(t, want, logs.lastLog.ActualCost, 1e-12)
		require.InDelta(t, base.TotalCost+search.TotalCost, logs.lastLog.TotalCost, 1e-12)
		require.InDelta(t, rate*modelRate, logs.lastLog.RateMultiplier, 1e-12)
		require.Equal(t, &group.ID, logs.lastLog.GroupID)
	}
}

func TestModelBillingGroupOpenAIPricedFallbackAndPerRequest(t *testing.T) {
	svc := newOpenAIRecordUsageServiceForTest(nil, nil, nil, nil)
	svc.StopOpenAICodexTicketHarvester()
	svc.settingService = modelBillingSettings(t, ModelBillingConfig{Enabled: true, Rules: []ModelBillingRule{{Model: "gpt-6-luna*", Multiplier: 10, GroupIDs: []int64{5}}}})
	for _, groupID := range []int64{5, 6} {
		key := &APIKey{ID: 1, GroupID: &groupID, Group: &Group{ID: groupID, Hydrated: true}}
		tokens := UsageTokens{InputTokens: 1000, OutputTokens: 100}
		result := &OpenAIForwardResult{Model: "alias", SearchCount: 2}
		cost, err := svc.calculateOpenAIRecordUsageCost(t.Context(), result, key, []string{"no-price-test-model", "gpt-6-luna"}, .2, .2, .2, .2, tokens, "", nil, false, time.Time{})
		require.NoError(t, err)
		base, err := svc.billingService.CalculateCost("gpt-6-luna", tokens, .2)
		require.NoError(t, err)
		search := svc.billingService.CalculateSearchCost(2, nil, .2)
		modelRate := 1.0
		if groupID == 5 {
			modelRate = 10
		}
		require.InDelta(t, base.ActualCost*modelRate+search.ActualCost, cost.ActualCost, 1e-12)
		require.InDelta(t, base.TotalCost+search.TotalCost, cost.TotalCost, 1e-12)
		web := &OpenAIForwardResult{Model: "gpt-6-luna", WebSearchCalls: 2}
		webCost, err := svc.calculateOpenAIRecordUsageCost(t.Context(), web, key, []string{"gpt-6-luna"}, .2, .2, .2, .2, tokens, "", nil, false, time.Time{})
		require.NoError(t, err)
		// 本 fork 把托管网页搜索改为 token 计费之上按次叠加（见 addToolSurchargeCost），
		// 只有无 token 的独立 alpha/search 请求才单独结算搜索费。
		webSearch := svc.billingService.CalculateWebSearchCost(2, nil, .2)
		require.InDelta(t, base.ActualCost*modelRate+webSearch.ActualCost, webCost.ActualCost, 1e-12)
		require.InDelta(t, base.TotalCost+webSearch.TotalCost, webCost.TotalCost, 1e-12)
		price := .5
		key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"gpt-6-luna"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}}
		svc.resolver = NewModelPricingResolver(nil, svc.billingService)
		perRequest, err := svc.calculateOpenAIRecordUsageCost(t.Context(), &OpenAIForwardResult{Model: "gpt-6-luna"}, key, []string{"gpt-6-luna"}, .2, .2, .2, .2, tokens, "", nil, false, time.Time{})
		require.NoError(t, err)
		require.Equal(t, string(BillingModePerRequest), perRequest.BillingMode)
		require.InDelta(t, price*.2, perRequest.ActualCost, 1e-12)
		require.InDelta(t, price, perRequest.TotalCost, 1e-12)
		require.Equal(t, 1.0, costModelBillingMultiplier(perRequest))
		svc.resolver = nil
	}
}
