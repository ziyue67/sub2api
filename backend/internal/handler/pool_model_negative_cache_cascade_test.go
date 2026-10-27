//go:build unit

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func negativeCacheAccount(id int64, priority int, pool bool) service.Account {
	account := poolCapacityAccount(id, priority, pool)
	// No administrator overrides: this test must exercise default pool policy.
	delete(account.Credentials, "temp_unschedulable_enabled")
	delete(account.Credentials, "temp_unschedulable_rules")
	account.Credentials["pool_mode_retry_status_codes"] = []any{float64(401), float64(403), float64(429), float64(503)}
	account.Credentials["model_mapping"] = map[string]any{
		"gpt-6.1-sol": "provider-sol", "provider-sol": "provider-sol", "other-model": "other-model",
	}
	return account
}

func seedNegativeCache(account *service.Account, reason string, until time.Time) {
	account.Extra["model_rate_limits"] = map[string]any{"provider-sol": map[string]any{
		"rate_limit_reset_at": until.UTC().Format(time.RFC3339), "reason": reason,
	}}
}

func negativeCacheDeadline(account *service.Account) string {
	return account.Extra["model_rate_limits"].(map[string]any)["provider-sol"].(map[string]any)["rate_limit_reset_at"].(string)
}

// The provider has recovered, but A still has legacy observations from the old
// writer. A NEW request must not turn those expiring observations into a fresh
// model_not_found response and another 30 minutes on B.
func TestPoolModelNegativeCacheTwoLayersHistoricalObservationsDoNotRenew(t *testing.T) {
	for _, reason := range []string{"upstream_404_model_not_found", "upstream_401_model_not_found", "upstream_400_codex_plan_gated_model"} {
		t.Run(reason, func(t *testing.T) {
			firstRepo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
				accounts: []service.Account{negativeCacheAccount(11, 1, true), negativeCacheAccount(12, 2, true)}}}
			until := time.Now().Add(30 * time.Second).UTC().Truncate(time.Second)
			for i := range firstRepo.accounts {
				// Use configured model routing: passthrough intentionally skips
				// outbound alias normalization in capability diagnosis.
				firstRepo.accounts[i].Extra["openai_passthrough"] = false
				seedNegativeCache(&firstRepo.accounts[i], reason, until)
			}
			provider := &poolCapacityTransport{reply: func(*http.Request, int64) *http.Response {
				return poolCapacityResponse(200, "application/json", poolCapacityCompleted, "")
			}}
			first := newPoolCapacityRouter(t, firstRepo, provider)
			secondRepo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
				accounts: []service.Account{negativeCacheAccount(21, 1, true), negativeCacheAccount(22, 2, true)}}}
			var firstStatuses []int
			var firstCodes []string
			relay := &poolCapacityTransport{reply: func(req *http.Request, _ int64) *http.Response {
				rec := httptest.NewRecorder()
				forwarded := req.Clone(req.Context())
				forwarded.URL.Path = "/v1/responses"
				first.ServeHTTP(rec, forwarded)
				firstStatuses = append(firstStatuses, rec.Code)
				firstCodes = append(firstCodes, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
				// Return the actual response before asserting: B must consume it
				// through Forward and its account-state fastpath even on old code.
				return rec.Result()
			}}
			second := newPoolCapacityRouter(t, secondRepo, relay)
			for request := 0; request < 2; request++ {
				rec := servePoolCapacity(second, false)
				require.Contains(t, []int{429, 502, 503}, rec.Code, rec.Body.String())
				require.NotEqual(t, "model_not_found", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
				require.Zero(t, secondRepo.modelWrites, "B must not create a new 30m deadline from A's near-expiry observations")
				for i, status := range firstStatuses {
					require.Contains(t, []int{429, 503}, status, "expiring observation must remain temporarily unavailable")
					require.NotEqual(t, "model_not_found", firstCodes[i])
				}
				for i := range firstRepo.accounts {
					require.Equal(t, until.Format(time.RFC3339), negativeCacheDeadline(&firstRepo.accounts[i]))
					require.False(t, firstRepo.accounts[i].IsSchedulableForModel("gpt-6.1-sol"))
					require.True(t, firstRepo.accounts[i].IsSchedulableForModel("other-model"))
				}
			}
			require.Empty(t, provider.hits, "active historical caches still block scheduling, despite recovered provider")
			require.Equal(t, []int64{21, 22, 21, 22}, relay.hits, "all candidates, finite switching, fresh next request")
			require.Len(t, firstStatuses, 4)
			require.Zero(t, firstRepo.modelWrites)
			// Advance mock persisted deadlines across expiry, without deleting
			// records or rebuilding either gateway's runtime state.
			for i := range firstRepo.accounts {
				seedNegativeCache(&firstRepo.accounts[i], reason, time.Now().Add(-time.Second))
			}
			rec := servePoolCapacity(second, false)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			require.Equal(t, []int64{11}, provider.hits)
			require.Equal(t, []int64{21, 22, 21, 22, 21}, relay.hits)
			require.Zero(t, firstRepo.modelWrites)
			require.Zero(t, secondRepo.modelWrites)
			require.Empty(t, secondRepo.rateLimitedIDs)
		})
	}
}

func TestPoolModelNegativeCacheDefaultHTTPMissingDoesNotPersistAcrossRequests(t *testing.T) {
	for _, status := range []int{404, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			repo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
				accounts: []service.Account{negativeCacheAccount(11, 1, true), negativeCacheAccount(12, 2, true)}}}
			recovered := false
			provider := &poolCapacityTransport{reply: func(*http.Request, int64) *http.Response {
				if !recovered {
					return poolCapacityResponse(status, "application/json", `{"error":{"code":"model_not_found","message":"unknown model provider-sol"}}`, "")
				}
				return poolCapacityResponse(200, "application/json", poolCapacityCompleted, "")
			}}
			router := newPoolCapacityRouter(t, repo, provider)
			require.GreaterOrEqual(t, servePoolCapacity(router, false).Code, 400)
			require.Equal(t, []int64{11, 12}, provider.hits)
			require.Zero(t, repo.modelWrites, "pool default covers both HTTP404 and compatible model401 fastpaths")
			recovered = true
			rec := servePoolCapacity(router, false)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			require.Equal(t, []int64{11, 12, 11}, provider.hits)
			require.Zero(t, repo.modelWrites)
		})
	}
}

func TestPoolModelNegativeCacheNonPoolWriterThenFreshRequestAndHealthyPeer(t *testing.T) {
	repo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
		accounts: []service.Account{negativeCacheAccount(11, 1, false), negativeCacheAccount(12, 2, false)}}}
	for i := range repo.accounts {
		repo.accounts[i].Extra["openai_passthrough"] = false
	}
	recovered := false
	provider := &poolCapacityTransport{reply: func(*http.Request, int64) *http.Response {
		if !recovered {
			return poolCapacityResponse(404, "application/json", `{"error":{"code":"model_not_found","message":"model not found"}}`, "")
		}
		return poolCapacityResponse(200, "application/json", poolCapacityCompleted, "")
	}}
	router := newPoolCapacityRouter(t, repo, provider)
	require.GreaterOrEqual(t, servePoolCapacity(router, false).Code, 400)
	require.Equal(t, 2, repo.modelWrites, "nonpool keeps actual missing-model protection")
	require.Equal(t, []int64{11, 12}, provider.hits)
	for i := range repo.accounts {
		until, err := time.Parse(time.RFC3339, negativeCacheDeadline(&repo.accounts[i]))
		require.NoError(t, err)
		require.WithinDuration(t, time.Now().Add(30*time.Minute), until, 2*time.Second)
	}
	recovered = true
	rec := servePoolCapacity(router, false) // NEW request, not failover continuation
	require.Contains(t, []int{429, 503}, rec.Code, rec.Body.String())
	require.NotEqual(t, "model_not_found", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Equal(t, []int64{11, 12}, provider.hits)
	require.Equal(t, 2, repo.modelWrites)
	// A newly eligible configured peer must still be selectable while the
	// original pairs remain blocked. Nothing clears the existing observations.
	repo.accounts = append(repo.accounts, negativeCacheAccount(13, 3, false))
	require.Equal(t, 200, servePoolCapacity(router, false).Code)
	require.Equal(t, []int64{11, 12, 13}, provider.hits)
	require.Equal(t, 2, repo.modelWrites)
}

func TestPoolModelNegativeCacheConfiguredUnsupportedStill404(t *testing.T) {
	repo := &poolCapacityRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{
		accounts: []service.Account{negativeCacheAccount(11, 1, true), negativeCacheAccount(12, 2, true)}}}
	for i := range repo.accounts {
		// Passthrough deliberately bypasses the account model allowlist. This
		// fixture must use configured routing to prove a genuine config 404.
		repo.accounts[i].Extra["openai_passthrough"] = false
		repo.accounts[i].Credentials["model_mapping"] = map[string]any{"other-model": "other-model"}
	}
	provider := &poolCapacityTransport{reply: func(*http.Request, int64) *http.Response {
		t.Fatal("unsupported configuration must never reach provider")
		return nil
	}}
	router := newPoolCapacityRouter(t, repo, provider)
	rec := servePoolCapacity(router, false)
	require.Equal(t, 404, rec.Code, rec.Body.String())
	require.Equal(t, "model_not_found", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Empty(t, provider.hits)
	require.Zero(t, repo.modelWrites)
	for i := range repo.accounts {
		require.True(t, repo.accounts[i].IsSchedulableForModelWithContext(context.Background(), "other-model"))
	}
}
