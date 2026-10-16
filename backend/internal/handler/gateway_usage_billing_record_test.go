package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type downstreamBillingRepoStub struct {
	service.UsageLogRepository
	logs    []service.UsageLog
	err     error
	filters usagestats.UsageLogFilters
	called  bool
}

func (r *downstreamBillingRepoStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	r.called = true
	r.filters = filters
	return r.logs, nil, r.err
}

func serveDownstreamBillingQuery(t *testing.T, repo *downstreamBillingRepoStub, query string, key *service.APIKey, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &GatewayHandler{}
	if repo != nil {
		h.usageService = service.NewUsageService(repo, nil, nil, nil)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if key != nil {
			c.Set(string(middleware.ContextKeyAPIKey), key)
		}
		if userID != 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
		}
		c.Next()
	})
	r.GET("/v1/usage", h.Usage)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/usage?"+query, nil))
	return w
}

func TestDownstreamBillingRecordConvertedAndRawUsage(t *testing.T) {
	repo := &downstreamBillingRepoStub{logs: []service.UsageLog{{
		ID: 9, UserID: 7, APIKeyID: 8, AccountID: 29665, RequestID: "client:test-1",
		InputTokens: 4, OutputTokens: 2, CacheCreationTokens: 360, CacheReadTokens: 1000,
		CacheCreation1hTokens: 360, ActualCost: 0.09664109,
		CacheCreationBilling: &service.CacheCreationBillingAudit{
			Enabled: true, Status: "converted", Original5mPresent: true, Original1hPresent: true,
			OriginalCreationTokens: 360, Original5mTokens: 360, OriginalReadTokens: 1000,
			OriginalCreationCost: 0.0018, ActualCostDelta: 0.000918,
		},
	}}}
	w := serveDownstreamBillingQuery(t, repo, "request_id=client%3Atest-1", &service.APIKey{ID: 8, UserID: 7}, 7)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, int64(7), repo.filters.UserID)
	require.Equal(t, int64(8), repo.filters.APIKeyID)
	require.Equal(t, "client:test-1", repo.filters.RequestID)
	var got downstreamBillingRecord
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "site_billing", got.BillingBasis)
	require.Equal(t, "recorded", got.RecordStatus)
	require.Equal(t, 360, got.BilledUsage.CacheCreation1h)
	require.Zero(t, got.BilledUsage.CacheCreation5m)
	require.Equal(t, 1000, got.BilledUsage.CacheReadTokens)
	require.NotNil(t, got.UpstreamCacheUsage)
	require.Equal(t, 360, got.UpstreamCacheUsage.Creation5m)
	require.Zero(t, got.UpstreamCacheUsage.Creation1h)
	require.InDelta(t, 0.09664109, got.ActualCost, 1e-12)
	for _, secret := range []string{"account_id", "29665", "original_creation_cost", "actual_cost_delta", "cache_creation_billing", "api_key"} {
		require.NotContains(t, w.Body.String(), secret)
	}
}

func TestDownstreamBillingRecordPreservesRecordedFallback(t *testing.T) {
	for _, status := range []string{"", "missing_breakdown", "unavailable_pricing", "unchanged"} {
		t.Run(status, func(t *testing.T) {
			log := service.UsageLog{UserID: 7, APIKeyID: 8, RequestID: "local:one", CacheCreationTokens: 100, CacheCreation5mTokens: 100}
			if status != "" {
				log.CacheCreationBilling = &service.CacheCreationBillingAudit{Enabled: true, Status: status, Original5mTokens: 100}
			}
			w := serveDownstreamBillingQuery(t, &downstreamBillingRepoStub{logs: []service.UsageLog{log}}, "request_id=local:one", &service.APIKey{ID: 8, UserID: 7}, 7)
			require.Equal(t, http.StatusOK, w.Code)
			var got downstreamBillingRecord
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			require.Equal(t, 100, got.BilledUsage.CacheCreation5m)
			require.Zero(t, got.BilledUsage.CacheCreation1h)
			if status == "" {
				require.Nil(t, got.UpstreamCacheUsage, "legacy records must not invent upstream evidence")
				require.Nil(t, got.CacheBillingStatus)
			} else {
				require.NotNil(t, got.CacheBillingStatus)
				require.Equal(t, status, *got.CacheBillingStatus)
			}
		})
	}
}

func TestDownstreamBillingRecordIsolationAndErrors(t *testing.T) {
	valid := service.UsageLog{UserID: 7, APIKeyID: 8, RequestID: "local:one"}
	for _, tc := range []struct {
		name string
		logs []service.UsageLog
		err  error
		code int
	}{
		{name: "pending or absent", code: 404},
		{name: "another key", logs: []service.UsageLog{{UserID: 7, APIKeyID: 9, RequestID: "local:one"}}, code: 404},
		{name: "another user", logs: []service.UsageLog{{UserID: 9, APIKeyID: 8, RequestID: "local:one"}}, code: 404},
		{name: "different request", logs: []service.UsageLog{{UserID: 7, APIKeyID: 8, RequestID: "local:two"}}, code: 404},
		{name: "duplicate records", logs: []service.UsageLog{valid, valid}, code: 409},
		{name: "database error", err: errors.New("secret database details"), code: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serveDownstreamBillingQuery(t, &downstreamBillingRepoStub{logs: tc.logs, err: tc.err}, "request_id=local:one", &service.APIKey{ID: 8, UserID: 7}, 7)
			require.Equal(t, tc.code, w.Code)
			require.NotContains(t, w.Body.String(), "billed_usage")
			require.NotContains(t, w.Body.String(), "secret database")
		})
	}
	for _, query := range []string{"request_id=", "request_id=++", "request_id=a&request_id=b", "request_id=" + strings.Repeat("a", 257), "request_id=" + url.QueryEscape("a\nb")} {
		repo := &downstreamBillingRepoStub{}
		w := serveDownstreamBillingQuery(t, repo, query, &service.APIKey{ID: 8, UserID: 7}, 7)
		require.Equal(t, 400, w.Code)
		require.False(t, repo.called)
	}
	for _, tc := range []struct {
		key    *service.APIKey
		userID int64
	}{
		{nil, 7}, {&service.APIKey{ID: 8, UserID: 7}, 0}, {&service.APIKey{ID: 8, UserID: 9}, 7}, {&service.APIKey{UserID: 7}, 7},
	} {
		repo := &downstreamBillingRepoStub{}
		w := serveDownstreamBillingQuery(t, repo, "request_id=local:one", tc.key, tc.userID)
		require.Equal(t, 401, w.Code)
		require.False(t, repo.called)
	}
	w := serveDownstreamBillingQuery(t, nil, "request_id=local:one", &service.APIKey{ID: 8, UserID: 7}, 7)
	require.Equal(t, 503, w.Code)
}
