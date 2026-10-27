//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPoolCapacityExhaustionPreservesClassificationOnWire(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		failure := &service.UpstreamFailoverError{
			StatusCode:             http.StatusTooManyRequests,
			ResponseBody:           []byte(`{"error":{"message":"Selected model is at capacity. Please try a different model."}}`),
			ResponseHeaders:        http.Header{"Retry-After": {"2"}},
			RequestScopedTransient: true, RetryableOnSameAccount: true,
			ClientStatusCode: http.StatusServiceUnavailable,
			ClientMessage:    "Selected model is at capacity. Please try a different model.",
		}
		(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failure, streaming)
		if streaming {
			require.Contains(t, rec.Body.String(), `"code":"upstream_capacity_exhausted"`)
			require.Contains(t, rec.Body.String(), "event: response.failed")
		} else {
			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.Equal(t, "upstream_capacity_exhausted", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
		}
		require.Equal(t, "2", rec.Header().Get("Retry-After"))
	}
}

// Both layers run the real scheduler, Forward and exhaustion mapping. The
// transport calls the first router in memory; no sockets or paid requests exist.
type poolCapacityRepo struct {
	openAIWSFailoverHandlerAccountRepoStub
	modelWrites int
}

func (r *poolCapacityRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return r.accounts, nil
}

func (r *poolCapacityRepo) SetModelRateLimit(_ context.Context, id int64, model string, until time.Time, reason ...string) error {
	r.modelWrites++
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			entry := map[string]any{"rate_limit_reset_at": until.UTC().Format(time.RFC3339)}
			if len(reason) > 0 {
				entry["reason"] = reason[0]
			}
			r.accounts[i].Extra["model_rate_limits"] = map[string]any{model: entry}
		}
	}
	return nil
}

// Unexpected account-wide mutations fail immediately instead of being silently
// swallowed by the mock. The pool tests expect none of them.
func (*poolCapacityRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	panic("unexpected account-wide temporary ban")
}

func (*poolCapacityRepo) SetError(context.Context, int64, string) error {
	panic("unexpected credential mutation")
}

type poolCapacityTransport struct {
	service.HTTPUpstream
	hits  []int64
	reply func(*http.Request, int64) *http.Response
}

func (u *poolCapacityTransport) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.hits = append(u.hits, id)
	return u.reply(req, id), nil
}

func poolCapacityResponse(status int, contentType, body, retryAfter string) *http.Response {
	headers := http.Header{"Content-Type": {contentType}}
	if retryAfter != "" {
		headers.Set("Retry-After", retryAfter)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
}

func poolCapacityAccount(id int64, priority int, pool bool) service.Account {
	return service.Account{
		ID: id, Name: "fictional-relay", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Priority: priority,
		Credentials: map[string]any{
			"api_key": "fictional-key", "base_url": "https://relay.example.test", "pool_mode": pool,
			"pool_mode_retry_count": float64(0), "temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{
				map[string]any{"error_code": float64(429), "keywords": []any{"capacity", "rate limit"}, "duration_minutes": float64(30), "scope": "model"},
				map[string]any{"error_code": float64(503), "keywords": []any{"capacity", "unavailable"}, "duration_minutes": float64(30), "scope": "model"},
			},
		},
		Extra: map[string]any{"openai_passthrough": true},
	}
}

func newPoolCapacityRouter(t *testing.T, repo *poolCapacityRepo, upstream service.HTTPUpstream) *gin.Engine {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	rateLimits := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), rateLimits, billingCache, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	groupID := int64(99)
	apiKey := &service.APIKey{ID: 100, GroupID: &groupID, User: &service.User{ID: 101, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID})
	})
	router.POST("/v1/responses", h.Responses)
	return router
}

func servePoolCapacity(router *gin.Engine, stream bool) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(
		`{"model":"gpt-6.1-sol","input":"hello","stream":%t}`, stream)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

const poolCapacityCompleted = `{"id":"resp_mock","object":"response","status":"completed","model":"gpt-6.1-sol","output":[],"usage":{"input_tokens":0,"output_tokens":0}}`

func TestPoolCapacityTwoLayersKeepHealthyCandidatesAndBoundAllBusy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{429, 503, 200} {
		for _, retryAfter := range []string{"", "2"} {
			for _, healthy := range []bool{false, true} {
				t.Run(fmt.Sprintf("status=%d/retry=%s/healthy=%t", status, retryAfter, healthy), func(t *testing.T) {
					firstRepo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
						accounts: []service.Account{poolCapacityAccount(11, 1, true), poolCapacityAccount(12, 2, true)}}}
					provider := &poolCapacityTransport{reply: func(*http.Request, int64) *http.Response {
						if status == 200 {
							return poolCapacityResponse(200, "text/event-stream",
								"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"invalid_request_error\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}}\n\n", retryAfter)
						}
						return poolCapacityResponse(status, "application/json", `{"error":{"type":"rate_limit_error","message":"Selected model is at capacity. Please try a different model."}}`, retryAfter)
					}}
					first := newPoolCapacityRouter(t, firstRepo, provider)
					secondRepo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
						accounts: []service.Account{poolCapacityAccount(21, 1, true), poolCapacityAccount(22, 2, true)}}}
					relay := &poolCapacityTransport{reply: func(req *http.Request, id int64) *http.Response {
						if healthy && id == 22 {
							return poolCapacityResponse(200, "application/json", poolCapacityCompleted, "")
						}
						rec := httptest.NewRecorder()
						// Forward's outbound body/headers go through another Sub2API.
						forwarded := req.Clone(req.Context())
						forwarded.URL.Path = "/v1/responses"
						first.ServeHTTP(rec, forwarded)
						response := rec.Result()
						if response.StatusCode != 503 || gjson.GetBytes(rec.Body.Bytes(), "error.code").String() != "upstream_capacity_exhausted" {
							t.Errorf("first layer lost capacity semantics: %d %s", rec.Code, rec.Body.String())
						}
						return response
					}}
					second := newPoolCapacityRouter(t, secondRepo, relay)
					for request := 0; request < 2; request++ {
						rec := servePoolCapacity(second, false)
						if healthy {
							require.Equal(t, 200, rec.Code, rec.Body.String())
						} else {
							require.Equal(t, 503, rec.Code, rec.Body.String())
							require.Equal(t, "upstream_capacity_exhausted", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
							require.Equal(t, retryAfter, rec.Header().Get("Retry-After"))
						}
					}
					require.Equal(t, []int64{21, 22, 21, 22}, relay.hits, "bounded switches and subsequent requests must retain both candidates")
					require.LessOrEqual(t, len(provider.hits), 8)
					require.Zero(t, firstRepo.modelWrites)
					require.Zero(t, secondRepo.modelWrites)
					require.Empty(t, firstRepo.rateLimitedIDs)
					require.Empty(t, secondRepo.rateLimitedIDs)
				})
			}
		}
	}
}

func TestPoolCapacityExplicitPolicyCooldownDoesNotSlideAndRecovers(t *testing.T) {
	for _, pool := range []bool{false, true} {
		t.Run(fmt.Sprintf("pool=%t", pool), func(t *testing.T) {
			repo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
				accounts: []service.Account{poolCapacityAccount(11, 1, pool), poolCapacityAccount(12, 2, pool)}}}
			recovered := false
			upstream := &poolCapacityTransport{reply: func(_ *http.Request, id int64) *http.Response {
				if id == 11 && !recovered {
					return poolCapacityResponse(503, "application/json", `{"error":{"message":"Service temporarily unavailable"}}`, "")
				}
				return poolCapacityResponse(200, "application/json", poolCapacityCompleted, "")
			}}
			router := newPoolCapacityRouter(t, repo, upstream)
			require.Equal(t, 200, servePoolCapacity(router, false).Code)
			require.Equal(t, []int64{11, 12}, upstream.hits)
			require.Equal(t, 1, repo.modelWrites, "generic unavailable still honors explicit model policy")
			limits := repo.accounts[0].Extra["model_rate_limits"].(map[string]any)
			entry := limits["gpt-6.1-sol"].(map[string]any)
			reset := entry["rate_limit_reset_at"].(string)
			until, err := time.Parse(time.RFC3339, reset)
			require.NoError(t, err)
			require.WithinDuration(t, time.Now().Add(30*time.Minute), until, 2*time.Second)
			for request := 0; request < 2; request++ {
				require.Equal(t, 200, servePoolCapacity(router, false).Code)
			}
			require.Equal(t, []int64{11, 12, 12, 12}, upstream.hits)
			require.Equal(t, 1, repo.modelWrites)
			require.Equal(t, reset, entry["rate_limit_reset_at"], "reads must not renew the absolute cooldown")
			// Advance the persisted mock deadline without a sleep or clearing the
			// record: the same scheduler must admit it after expiration.
			repo.accounts[0].Extra["model_rate_limits"] = map[string]any{"gpt-6.1-sol": map[string]any{
				"rate_limit_reset_at": time.Now().Add(-time.Second).UTC().Format(time.RFC3339),
				"reason":              entry["reason"],
			}}
			recovered = true
			require.Equal(t, 200, servePoolCapacity(router, false).Code)
			require.Equal(t, int64(11), upstream.hits[len(upstream.hits)-1])
			require.Equal(t, 1, repo.modelWrites)
		})
	}
}

func TestPoolCapacityRealQuotaAndModelRejectionStillIsolateOneAccountModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"quota", 429, `{"error":{"type":"usage_limit_reached","code":"insufficient_quota","message":"Rate limit exceeded: quota exhausted"}}`},
		{"model-not-found", 404, `{"error":{"code":"model_not_found","message":"Model not found"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
				accounts: []service.Account{poolCapacityAccount(11, 1, true), poolCapacityAccount(12, 2, true)}}}
			if tc.status == http.StatusNotFound {
				// Explicit administrator policy retains missing-model isolation;
				// default-pool no-cache behavior has its own regressions.
				repo.accounts[0].Credentials["custom_error_codes_enabled"] = true
				repo.accounts[0].Credentials["custom_error_codes"] = []any{float64(http.StatusNotFound)}
			}
			upstream := &poolCapacityTransport{reply: func(_ *http.Request, id int64) *http.Response {
				if id == 11 {
					return poolCapacityResponse(tc.status, "application/json", tc.body, "60")
				}
				return poolCapacityResponse(200, "application/json", poolCapacityCompleted, "")
			}}
			router := newPoolCapacityRouter(t, repo, upstream)
			require.Equal(t, 200, servePoolCapacity(router, false).Code)
			require.Equal(t, 1, repo.modelWrites)
			require.Equal(t, []int64{11, 12}, upstream.hits)
			require.Equal(t, 200, servePoolCapacity(router, false).Code)
			require.Equal(t, []int64{11, 12, 12}, upstream.hits)
			require.True(t, repo.accounts[0].IsSchedulable(), "other models on this account remain eligible")
		})
	}
}

func TestPoolCapacityTwoLayersNeverReplayAfterSemanticOutput(t *testing.T) {
	firstRepo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
		accounts: []service.Account{poolCapacityAccount(11, 1, true), poolCapacityAccount(12, 2, true)}}}
	provider := &poolCapacityTransport{reply: func(*http.Request, int64) *http.Response {
		return poolCapacityResponse(200, "text/event-stream",
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"already-visible\"}\n\n"+
				"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"type\":\"server_error\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}}\n\n", "")
	}}
	first := newPoolCapacityRouter(t, firstRepo, provider)
	secondRepo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
		accounts: []service.Account{poolCapacityAccount(21, 1, true), poolCapacityAccount(22, 2, true)}}}
	relay := &poolCapacityTransport{reply: func(req *http.Request, _ int64) *http.Response {
		rec := httptest.NewRecorder()
		forwarded := req.Clone(req.Context())
		forwarded.URL.Path = "/v1/responses"
		first.ServeHTTP(rec, forwarded)
		return rec.Result()
	}}
	second := newPoolCapacityRouter(t, secondRepo, relay)
	rec := servePoolCapacity(second, true)
	require.Equal(t, 200, rec.Code, "committed SSE keeps its HTTP status")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "already-visible"))
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
	require.Equal(t, []int64{11}, provider.hits)
	require.Equal(t, []int64{21}, relay.hits)
	require.Zero(t, firstRepo.modelWrites)
	require.Zero(t, secondRepo.modelWrites)
}
