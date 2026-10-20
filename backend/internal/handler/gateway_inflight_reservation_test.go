package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestMaxOutputTokens(t *testing.T) {
	require.Equal(t, 1024, requestMaxOutputTokens([]byte(`{"max_tokens":1024}`)))
	require.Equal(t, 2048, requestMaxOutputTokens([]byte(`{"max_completion_tokens":2048}`)))
	require.Equal(t, 4096, requestMaxOutputTokens([]byte(`{"max_output_tokens":4096}`)))
	require.Equal(t, 512, requestMaxOutputTokens([]byte(`{"generationConfig":{"maxOutputTokens":512}}`)))
	require.Equal(t, 0, requestMaxOutputTokens([]byte(`{"max_tokens":"x"}`)))
	require.Equal(t, 0, requestMaxOutputTokens([]byte(`{}`)))
}

func TestHostedWebSearchInflightEstimate_DetectsOnlyProviderTools(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "openai", body: `{"tools":[{"type":"web_search_preview"}]}`, want: 1},
		{name: "anthropic", body: `{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, want: 1},
		{name: "client function", body: `{"tools":[{"type":"function","name":"web_search"}]}`, want: 0},
		{name: "no tools", body: `{"input":"hello"}`, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := hostedWebSearchInflightEstimate("gpt-5.5", []byte(tt.body))
			require.Equal(t, tt.want, req.HostedWebSearchCalls)
		})
	}
}

type countingEstimator struct {
	calls    int
	cost     float64
	priced   bool
	fallback float64
}

func (e *countingEstimator) UnpricedReservationFallbackUSD() float64 { return e.fallback }

func (e *countingEstimator) EstimateInflightReservation(context.Context, *service.APIKey, service.InflightEstimateRequest) (float64, bool) {
	e.calls++
	return e.cost, e.priced
}

func newInflightTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c
}

func TestReserveInflightBalance_SkipsWhenDisabledOrSubscription(t *testing.T) {
	cfg := &config.Config{}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	est := &countingEstimator{cost: 1, priced: true}
	apiKey := &service.APIKey{User: &service.User{ID: 1}}

	done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("m", []byte(`{}`)))
	require.NoError(t, err)
	done()
	require.Equal(t, 0, est.calls, "disabled switch must not even estimate")

	cfg.Billing.InflightReservation.Enabled = true
	apiKey.Group = &service.Group{SubscriptionType: service.SubscriptionTypeSubscription}
	done, err = reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, &service.UserSubscription{}, tokenInflightEstimate("m", []byte(`{}`)))
	require.NoError(t, err)
	done()
	require.Equal(t, 0, est.calls, "subscription mode must be unaffected")
}

func TestReserveInflightBalance_UnpricedFailOpenByDefaultFailClosedOptIn(t *testing.T) {
	cache := newHandlerInflightCache(10)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	apiKey := &service.APIKey{User: &service.User{ID: 1}}
	est := &countingEstimator{priced: false}

	done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("unknown", nil))
	require.NoError(t, err)
	done()

	cfg.Billing.InflightReservation.FailClosedOnUnpriced = true
	_, err = reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("unknown", nil))
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
}

// handlerInflightCache 内存版余额缓存 + 在途预留（语义同 Redis Lua）。
type handlerInflightCache struct {
	service.BillingCache
	mu      sync.Mutex
	balance float64
	res     map[string]float64
	// scoped 是新版在途预留账本（scope -> requestID -> amount）。
	scoped map[string]map[string]float64
}

func newHandlerInflightCache(balance float64) *handlerInflightCache {
	return &handlerInflightCache{balance: balance, res: map[string]float64{}, scoped: map[string]map[string]float64{}}
}

func (m *handlerInflightCache) ReserveUserBalance(_ context.Context, scope, id string, amount float64, _ time.Duration) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scoped[scope] == nil {
		m.scoped[scope] = map[string]float64{}
	}
	m.scoped[scope][id] = amount
	total := 0.0
	for _, v := range m.scoped[scope] {
		total += v
	}
	return total, nil
}

func (m *handlerInflightCache) ReleaseUserBalanceReservation(_ context.Context, scope, id string, _ float64, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.scoped[scope], id)
	return nil
}

func (m *handlerInflightCache) scopedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, items := range m.scoped {
		n += len(items)
	}
	return n
}

func (m *handlerInflightCache) GetUserBalance(context.Context, int64) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balance, nil
}

func (m *handlerInflightCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, nil
}

func (m *handlerInflightCache) ReserveInflightBalance(_ context.Context, _ int64, id string, amount, balance float64, _ time.Duration) (bool, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sum := 0.0
	for _, v := range m.res {
		sum += v
	}
	if len(m.res) > 0 && balance-sum < amount {
		return false, sum, nil
	}
	m.res[id] = amount
	return true, sum, nil
}

func (m *handlerInflightCache) ReleaseInflightBalance(_ context.Context, _ int64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.res, id)
	return nil
}

func (m *handlerInflightCache) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.res)
}

func TestWrapUsageRecordTaskContext_HandsReservationToBillingTask(t *testing.T) {
	cache := newHandlerInflightCache(1)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	apiKey := &service.APIKey{User: &service.User{ID: 5}}

	c := newInflightTestGinContext()
	done, err := reserveInflightBalance(c, billing, &countingEstimator{cost: 0.9, priced: true}, apiKey, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	require.Equal(t, 1, cache.count())

	ran := false
	task, abandon := wrapUsageRecordTaskContext(c.Request.Context(), func(context.Context) { ran = true })
	done() // handler returns; billing still pending
	require.Equal(t, 1, cache.count(), "reservation held until the billing task finishes")
	task(context.Background())
	require.True(t, ran)
	require.Equal(t, 0, cache.count())
	abandon() // idempotent with the task's own done

	// Dropped task: the submitter abandons it and the reservation is released.
	c2 := newInflightTestGinContext()
	done2, err := reserveInflightBalance(c2, billing, &countingEstimator{cost: 0.9, priced: true}, apiKey, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	_, abandon2 := wrapUsageRecordTaskContext(c2.Request.Context(), func(context.Context) {})
	done2()
	require.Equal(t, 1, cache.count())
	abandon2()
	require.Equal(t, 0, cache.count())
}

// 新接入的端点（独立 web_search）：在途预留超过余额时拒绝，且不残留预留。
func TestWebSearch_RejectsWhenInflightExceedsBalance(t *testing.T) {
	cache := newHandlerInflightCache(1.5)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	billing := service.NewBillingService(cfg, nil)
	gw := service.NewGatewayService(
		nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, billing, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil,
	)
	h := &GatewayHandler{gatewayService: gw, billingCacheService: billingCache}

	groupID := int64(3)
	perK := 1000.0 // $1 per search
	apiKey := &service.APIKey{
		ID: 9, User: &service.User{ID: 42, Balance: 1.5}, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, RateMultiplier: 1, SearchPricePer1k: &perK},
	}

	// Another in-flight request of this user already holds $1 (same admission path).
	var held service.BillingReservationSlot
	require.NoError(t, billingCache.CheckBillingEligibility(context.Background(), apiKey.User, apiKey, apiKey.Group, nil, "",
		service.WithMaxRequestSpend(1.0), service.WithBalanceReservation(&held)))
	defer held.Release(context.Background())
	require.Equal(t, 1, cache.scopedCount())

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/web_search", bytes.NewBufferString(`{"query":"sub2api"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	h.WebSearch(c)

	require.NotEqual(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "balance")
	require.Equal(t, 1, cache.scopedCount(), "rejected request must not leave a reservation behind")
	require.Equal(t, 0, cache.count(), "legacy wallet-only reservation is no longer used by this endpoint")
}

// WebSocket 按 turn 的预留：同 turn 重新准入替换旧预留；take 后交给结算任务；
// 会话结束时 releaseAll 只归还未移交（未 HandOff）的预留。
func TestOpenAIWSTurnReservations_ReplaceTakeAndReleaseAll(t *testing.T) {
	cache := newHandlerInflightCache(100)
	billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(billingCache.Stop)
	user := &service.User{ID: 7}
	admit := func() *service.BillingReservationSlot {
		slot := &service.BillingReservationSlot{}
		require.NoError(t, billingCache.CheckBillingEligibility(context.Background(), user, nil, nil, nil, "",
			service.WithMaxRequestSpend(1), service.WithBalanceReservation(slot), service.WithoutRPMCheck()))
		return slot
	}
	ctx := context.Background()
	r := newOpenAIWSTurnReservations()

	r.put(ctx, 1, admit())
	r.put(ctx, 1, admit()) // 首帧换号重试：旧预留立即归还
	require.Equal(t, 1, cache.scopedCount())

	r.put(ctx, 2, admit())
	require.Equal(t, 2, cache.scopedCount())

	settling := r.take(1)
	settling.HandOff() // 已提交结算任务
	r.release(ctx, 2)  // turn 2 失败：立即归还
	require.Equal(t, 1, cache.scopedCount())

	r.put(ctx, 3, admit())
	r.releaseAll(ctx) // 会话结束：只归还未移交的 turn 3
	require.Equal(t, 1, cache.scopedCount(), "handed-off reservation stays until its billing task releases it")
	settling.Release(ctx)
	require.Equal(t, 0, cache.scopedCount())
}

// 审计 H3：两套估算都无法定价时按配置的保守金额预留，而不是 0（0 会跳过全部预留）。
func TestAdmissionWorstSpend_UnpricedUsesConfiguredFallback(t *testing.T) {
	apiKey := &service.APIKey{User: &service.User{ID: 1}}
	req := tokenInflightEstimate("unpriced-alias", nil)
	require.Zero(t, admissionWorstSpend(context.Background(), &countingEstimator{}, apiKey, 0, req),
		"no fallback configured keeps the legacy behavior")
	require.InDelta(t, 2.5, admissionWorstSpend(context.Background(), &countingEstimator{fallback: 2.5}, apiKey, 0, req), 1e-12)
	// 任一估算能定价时不使用兜底金额。
	require.InDelta(t, 0.3, admissionWorstSpend(context.Background(), &countingEstimator{fallback: 2.5}, apiKey, 0.3, req), 1e-12)
	require.InDelta(t, 0.7, admissionWorstSpend(context.Background(), &countingEstimator{cost: 0.7, priced: true, fallback: 2.5}, apiKey, 0.3, req), 1e-12)
}
