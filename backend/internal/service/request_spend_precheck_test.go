//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// requestSpendTestCatalogJSON 预检测试用的目录定价：输入 $2/M、输出 $10/M。
const requestSpendTestCatalogJSON = `{
	"spend-test-model": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 2e-06, "output_cost_per_token": 1e-05}
}`

func newRequestSpendTestEnv(t *testing.T) *GatewayService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	bs := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, requestSpendTestCatalogJSON))
	resolver := NewModelPricingResolver(nil, bs)
	return &GatewayService{cfg: cfg, billingService: bs, resolver: resolver}
}

func newRequestSpendTestKey() (*User, *APIKey) {
	group := &Group{Platform: PlatformOpenAI}
	return &User{ID: 1}, &APIKey{ID: 1, Group: group}
}

func TestExtractRequestMaxOutputTokens(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		want   int
		wantOK bool
	}{
		{name: "max_tokens", body: `{"max_tokens": 123}`, want: 123, wantOK: true},
		{name: "max_completion_tokens", body: `{"max_completion_tokens": 456}`, want: 456, wantOK: true},
		{name: "max_output_tokens", body: `{"max_output_tokens": 789}`, want: 789, wantOK: true},
		{name: "largest_of_multiple", body: `{"max_tokens": 10, "max_output_tokens": 20}`, want: 20, wantOK: true},
		{name: "missing", body: `{"model": "m"}`, want: 0, wantOK: false},
		{name: "zero_ignored", body: `{"max_tokens": 0}`, want: 0, wantOK: false},
		{name: "negative_ignored", body: `{"max_tokens": -5}`, want: 0, wantOK: false},
		{name: "string_ignored", body: `{"max_tokens": "100"}`, want: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExtractRequestMaxOutputTokens([]byte(tt.body))
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEstimateRequestInputTokensUpperBound(t *testing.T) {
	require.Equal(t, requestSpendInputOverheadTokens, EstimateRequestInputTokensUpperBound(nil))
	body := []byte(strings.Repeat("ab", 500)) // 1000 字节
	require.Equal(t, 500+requestSpendInputOverheadTokens, EstimateRequestInputTokensUpperBound(body))
}

// TestEstimateRequestSpendUpperBound_MatchesSettlementCost 锁死"预检与结算同源"：
// 相同 token 上界/倍率/定价时刻下，预检数字必须等于结算费用函数给出的费用。
func TestEstimateRequestSpendUpperBound_MatchesSettlementCost(t *testing.T) {
	svc := newRequestSpendTestEnv(t)
	user, apiKey := newRequestSpendTestKey()
	body := []byte(`{"model":"spend-test-model","max_tokens":1000}`)

	ctx, pricingAt := WithGatewayTokenRequestPricing(context.Background())
	got := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body)
	require.Greater(t, got, 0.0)

	resolved := svc.resolver.Resolve(ctx, PricingInput{Model: "spend-test-model", Group: apiKey.Group})
	want, err := svc.billingService.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx:            ctx,
		Model:          "spend-test-model",
		Group:          apiKey.Group,
		Tokens:         UsageTokens{InputTokens: EstimateRequestInputTokensUpperBound(body), OutputTokens: 1000},
		RateMultiplier: 1,
		PricingAt:      pricingAt,
		Resolver:       svc.resolver,
		Resolved:       resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, want.ActualCost, got, 1e-12)

	// 输出上界 1000 token × $10/M = $0.01 必须被覆盖。
	require.GreaterOrEqual(t, got, 0.01)
}

func TestEstimateRequestSpendUpperBound_AppliesSafetyMultiplier(t *testing.T) {
	svc := newRequestSpendTestEnv(t)
	user, apiKey := newRequestSpendTestKey()
	body := []byte(`{"model":"spend-test-model","max_tokens":1000}`)
	ctx, _ := WithGatewayTokenRequestPricing(context.Background())

	base := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body)
	require.Greater(t, base, 0.0)

	svc.cfg.Billing.RequestSpendSafetyMultiplier = 2
	doubled := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body)
	require.InDelta(t, base*2, doubled, 1e-9)
}

func TestEstimateRequestSpendUpperBound_UsesDefaultOutputTokensWhenUndeclared(t *testing.T) {
	svc := newRequestSpendTestEnv(t)
	user, apiKey := newRequestSpendTestKey()
	ctx, _ := WithGatewayTokenRequestPricing(context.Background())

	// 未声明输出上限 → 使用缺省 8192 输出 token：仅输出成本就 >= 8192 × $10/M。
	undeclared := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", []byte(`{"model":"spend-test-model"}`))
	require.GreaterOrEqual(t, undeclared, 8192*1e-5)

	// 显式小上限 → 明显更低。
	declared := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", []byte(`{"model":"spend-test-model","max_tokens":100}`))
	require.Greater(t, undeclared, declared)
}

func TestEstimateRequestSpendUpperBound_DisabledAndMissingDeps(t *testing.T) {
	svc := newRequestSpendTestEnv(t)
	user, apiKey := newRequestSpendTestKey()
	body := []byte(`{"model":"spend-test-model","max_tokens":1000}`)
	ctx, _ := WithGatewayTokenRequestPricing(context.Background())

	svc.cfg.Billing.RequestSpendPrecheckDisabled = true
	require.Zero(t, svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body))

	svc.cfg.Billing.RequestSpendPrecheckDisabled = false
	require.Greater(t, svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body), 0.0)

	// 依赖缺失（无计费服务）→ 返回 0，闸门不生效。
	svc.billingService = nil
	require.Zero(t, svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body))

	// nil 接收者与 nil 依赖均安全。
	var nilSvc *GatewayService
	require.Zero(t, nilSvc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body))
	var nilOpenAISvc *OpenAIGatewayService
	require.Zero(t, nilOpenAISvc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", body))
}

// TestCheckBillingEligibility_RejectsWhenBalanceCannotCoverWorstSpend 锁死"零超发"
// 闸门：余额覆盖不了「封底线 + 最坏费用」时必须在转发前 403，且不得打"钱包耗尽"
// 标记（小额请求仍应照常放行）。
func TestCheckBillingEligibility_RejectsWhenBalanceCannotCoverWorstSpend(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 0.20
	userRepo := &balanceLoadUserRepoStub{balance: 0.20}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	// 封底 0.1 + 最坏费用 0.335 = 0.435 > 0.2 → 403。
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", WithMaxRequestSpend(0.335))
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(0), cache.markCalls.Load(),
		"付不满最坏费用的拒绝不得打耗尽标记：小额请求仍可能付得起")

	// 同一余额下小额最坏费用（0.05 < 可花余额 0.1）照常放行。
	err = svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", WithMaxRequestSpend(0.05))
	require.NoError(t, err)
	require.Equal(t, int64(0), cache.markCalls.Load())
}

// TestCheckBillingEligibility_WorstSpendGateUsesDBTruth 验证闸门使用 DB 真值：
// 缓存旧快照仍偏高，但扣掉最坏费用后进入危险带 → 必须回源复核，按真值拒绝。
func TestCheckBillingEligibility_WorstSpendGateUsesDBTruth(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 0.50 // 旧快照偏高
	userRepo := &balanceLoadUserRepoStub{balance: 0.12}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	// 0.1 + 0.335 = 0.435 > DB 真值 0.12 → 403，并纠正缓存。
	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", WithMaxRequestSpend(0.335))
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(1), userRepo.calls.Load())
	require.Equal(t, 0.12, cache.balanceSet.Load())
	require.Equal(t, int64(0), cache.markCalls.Load())

	// 真值 0.12 仍能覆盖小额最坏费用（0.1 + 0.01 = 0.11 <= 0.12）→ 放行。
	err = svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", WithMaxRequestSpend(0.01))
	require.NoError(t, err)
}

// TestCheckBillingEligibility_WorstSpendGateCacheOnly 覆盖无 userRepo 的降级装配：
// 只能依据缓存值时同样拒绝付不满最坏费用的请求。
func TestCheckBillingEligibility_WorstSpendGateCacheOnly(t *testing.T) {
	cache := &balanceExhaustionCacheStub{}
	cache.balance = 0.20
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", WithMaxRequestSpend(0.335))
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(0), cache.markCalls.Load())

	// WithMaxRequestSpend(0) 与不传选项都保持既有语义（仅阈值判断）。
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, "", WithMaxRequestSpend(0)))
	require.NoError(t, svc.CheckBillingEligibility(context.Background(), &User{ID: 1}, nil, nil, nil, ""))
}

// TestEstimateRequestSpendUpperBound_MinOutputTokensFloor 锁死“输出上界下限钳制”：
// 上游桥不执行请求声明的小上限（实测声明 64/190 仍产出 999 token）时，配置
// billing.request_spend_min_output_tokens 后预检必须按 max(声明值, 下限) 估计，
// 否则贴底时结算仍会封顶产生坏账；0/负数 = 关闭钳制（保持信任声明值）。
func TestEstimateRequestSpendUpperBound_MinOutputTokensFloor(t *testing.T) {
	svc := newRequestSpendTestEnv(t)
	user, apiKey := newRequestSpendTestKey()
	ctx, _ := WithGatewayTokenRequestPricing(context.Background())
	declaredBody := []byte(`{"model":"spend-test-model","max_tokens":64}`)

	trusted := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", declaredBody)
	require.Greater(t, trusted, 0.0)
	require.Less(t, trusted, 100*1e-5, "只按 64 token 估：必须明显低于 100 token 的输出成本")

	// 下限 5000 → 即使声明 64，也按 5000 × $10/M = $0.05 的输出成本预检。
	svc.cfg.Billing.RequestSpendMinOutputTokens = 5000
	floored := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", declaredBody)
	require.GreaterOrEqual(t, floored, 5000*1e-5)
	require.Greater(t, floored, trusted)

	// 声明值高于下限 → 仍以声明值为准（10000 × $10/M = $0.1）。
	large := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", []byte(`{"model":"spend-test-model","max_tokens":10000}`))
	require.GreaterOrEqual(t, large, 10000*1e-5)

	// 大声明值"逐位不受下限影响"：声明 max_tokens >= 下限 时，开着下限与关着下限的
	// 预检结果必须完全一致 —— 这是"下限不会让大 max_tokens 请求更早被 403"的权威
	// 口径（作用范围只限抬高偏小的声明值）。
	svc.cfg.Billing.RequestSpendMinOutputTokens = 0
	largeBody := []byte(`{"model":"spend-test-model","max_tokens":64000}`)
	withoutFloor := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", largeBody)
	require.Greater(t, withoutFloor, 0.0)
	svc.cfg.Billing.RequestSpendMinOutputTokens = 8192
	withFloor := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", largeBody)
	require.InDelta(t, withoutFloor, withFloor, 1e-12, "声明值 >= 下限时，下限不得改变预检结果")

	// 未声明 → 取 max(缺省 8192, 下限 9000) = 9000。
	svc.cfg.Billing.RequestSpendMinOutputTokens = 9000
	undeclared := svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", []byte(`{"model":"spend-test-model"}`))
	require.GreaterOrEqual(t, undeclared, 9000*1e-5)

	// 负数（配置校验会拦截，但手工装配也必须安全）= 关闭钳制。
	svc.cfg.Billing.RequestSpendMinOutputTokens = -1
	require.InDelta(t, trusted, svc.EstimateRequestSpendUpperBound(ctx, user, apiKey, "spend-test-model", declaredBody), 1e-12)
}
