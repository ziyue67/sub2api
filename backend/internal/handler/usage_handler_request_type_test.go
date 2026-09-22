package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userUsageRepoCapture struct {
	service.UsageLogRepository
	listParams       pagination.PaginationParams
	listFilters      usagestats.UsageLogFilters
	statsFilters     usagestats.UsageLogFilters
	trendFilters     usagestats.UsageLogFilters
	groupFilters     usagestats.UsageLogFilters
	leaderboardQuery usagestats.TokenLeaderboardQuery
	listRows         []service.UsageLog
	stats            *usagestats.UsageStats
	modelStats       []usagestats.ModelStat
	groupStats       []usagestats.GroupStat
	leaderboardRows  []usagestats.TokenLeaderboardRow
}

// leaderboardVisibilitySettingRepo is intentionally minimal: the handler only
// needs GetMultiple to resolve the public leaderboard visibility switch.
type leaderboardVisibilitySettingRepo struct {
	service.SettingRepository
	values map[string]string
	err    error
}

func (r *leaderboardVisibilitySettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.values, nil
}

func (s *userUsageRepoCapture) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	s.listParams = params
	s.listFilters = filters
	return s.listRows, &pagination.PaginationResult{
		Total:    int64(len(s.listRows)),
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    0,
	}, nil
}

func (s *userUsageRepoCapture) GetStatsWithFilters(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error) {
	s.statsFilters = filters
	if s.stats != nil {
		return s.stats, nil
	}
	return &usagestats.UsageStats{}, nil
}

func (s *userUsageRepoCapture) GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagestats.TrendDataPoint, error) {
	s.trendFilters = usagestats.UsageLogFilters{
		UserID:      userID,
		APIKeyID:    apiKeyID,
		AccountID:   accountID,
		GroupID:     groupID,
		Model:       model,
		RequestType: requestType,
		Stream:      stream,
		BillingType: billingType,
	}
	return []usagestats.TrendDataPoint{}, nil
}

func (s *userUsageRepoCapture) GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) ([]usagestats.ModelStat, error) {
	return s.modelStats, nil
}

func (s *userUsageRepoCapture) GetGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) ([]usagestats.GroupStat, error) {
	s.groupFilters = usagestats.UsageLogFilters{
		UserID:      userID,
		APIKeyID:    apiKeyID,
		AccountID:   accountID,
		GroupID:     groupID,
		RequestType: requestType,
		Stream:      stream,
		BillingType: billingType,
	}
	return s.groupStats, nil
}

func (s *userUsageRepoCapture) GetTokenLeaderboardWithFilters(_ context.Context, _ time.Time, _ time.Time, _ int, query usagestats.TokenLeaderboardQuery) ([]usagestats.TokenLeaderboardRow, error) {
	s.leaderboardQuery = query
	return s.leaderboardRows, nil
}

func newUserUsageRequestTypeTestRouterWithRole(repo *userUsageRepoCapture, role string) *gin.Engine {
	return newUserUsageRequestTypeTestRouterWithSettings(repo, role, nil)
}

func newUserUsageRequestTypeTestRouterWithSettings(repo *userUsageRepoCapture, role string, settingService *service.SettingService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(repo, nil, nil, nil)
	handler := NewUsageHandler(usageSvc, nil, nil, settingService)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
		c.Set(string(middleware2.ContextKeyUserRole), role)
		c.Next()
	})
	router.GET("/usage", handler.List)
	router.GET("/usage/stats", handler.Stats)
	router.GET("/usage/dashboard/models", handler.DashboardModels)
	router.GET("/usage/dashboard/snapshot-v2", handler.DashboardSnapshotV2)
	router.GET("/usage/dashboard/leaderboard", handler.DashboardLeaderboard)
	return router
}

func newUserUsageRequestTypeTestRouter(repo *userUsageRepoCapture) *gin.Engine {
	return newUserUsageRequestTypeTestRouterWithRole(repo, service.RoleAdmin)
}

func TestDashboardLeaderboardFiltersAndSort(t *testing.T) {
	repo := &userUsageRepoCapture{
		leaderboardRows: []usagestats.TokenLeaderboardRow{{
			UserID:       42,
			Email:        "user@example.com",
			Requests:     3,
			TotalTokens:  120,
			Cost:         0.2,
			ActualCost:   0.1,
			AccountCost:  0.15,
			LastActiveAt: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC),
		}},
	}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard?days=7&sort_by=cost&billing_mode=image&request_type=2&timezone=UTC", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cost", repo.leaderboardQuery.SortBy)
	require.Equal(t, "image", repo.leaderboardQuery.BillingMode)
	require.NotNil(t, repo.leaderboardQuery.RequestType)
	require.Equal(t, int16(service.RequestTypeStream), *repo.leaderboardQuery.RequestType)
	require.Contains(t, rec.Body.String(), `"actual_cost":0.1`)
	require.Contains(t, rec.Body.String(), `"account_cost":0.15`)
}

func TestDashboardLeaderboardRejectsInvalidSort(t *testing.T) {
	router := newUserUsageRequestTypeTestRouter(&userUsageRepoCapture{})
	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard?sort_by=not_allowed", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardLeaderboardHidesActualCostForOrdinaryUsersWhenDisabled(t *testing.T) {
	repo := &userUsageRepoCapture{
		leaderboardRows: []usagestats.TokenLeaderboardRow{{
			UserID:     42,
			Email:      "user@example.com",
			ActualCost: 0.1,
		}},
	}
	settings := service.NewSettingService(&leaderboardVisibilitySettingRepo{
		values: map[string]string{service.SettingKeyLeaderboardShowActualCost: "false"},
	}, &config.Config{})
	router := newUserUsageRequestTypeTestRouterWithSettings(repo, service.RoleUser, settings)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `"actual_cost"`)
}

func TestDashboardLeaderboardDoesNotExposeHiddenActualCostAsSortOracle(t *testing.T) {
	repo := &userUsageRepoCapture{
		leaderboardRows: []usagestats.TokenLeaderboardRow{{
			UserID:     42,
			Email:      "user@example.com",
			ActualCost: 0.1,
		}},
	}
	settings := service.NewSettingService(&leaderboardVisibilitySettingRepo{
		values: map[string]string{service.SettingKeyLeaderboardShowActualCost: "false"},
	}, &config.Config{})
	router := newUserUsageRequestTypeTestRouterWithSettings(repo, service.RoleUser, settings)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard?sort_by=actual_cost", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "tokens", repo.leaderboardQuery.SortBy)
	require.Contains(t, rec.Body.String(), `"sort_by":"tokens"`)
	require.NotContains(t, rec.Body.String(), `"actual_cost"`)
}

func TestDashboardLeaderboardKeepsActualCostForAdminsWhenDisabled(t *testing.T) {
	repo := &userUsageRepoCapture{
		leaderboardRows: []usagestats.TokenLeaderboardRow{{
			UserID:     42,
			Email:      "admin@example.com",
			ActualCost: 0.1,
		}},
	}
	settings := service.NewSettingService(&leaderboardVisibilitySettingRepo{
		values: map[string]string{service.SettingKeyLeaderboardShowActualCost: "false"},
	}, &config.Config{})
	router := newUserUsageRequestTypeTestRouterWithSettings(repo, service.RoleAdmin, settings)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard?sort_by=actual_cost", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "actual_cost", repo.leaderboardQuery.SortBy)
	require.Contains(t, rec.Body.String(), `"actual_cost":0.1`)
}

func TestDashboardLeaderboardKeepsActualCostForOrdinaryUsersWhenSettingMissing(t *testing.T) {
	repo := &userUsageRepoCapture{
		leaderboardRows: []usagestats.TokenLeaderboardRow{{
			UserID:     42,
			Email:      "user@example.com",
			ActualCost: 0.1,
		}},
	}
	settings := service.NewSettingService(&leaderboardVisibilitySettingRepo{values: map[string]string{}}, &config.Config{})
	router := newUserUsageRequestTypeTestRouterWithSettings(repo, service.RoleUser, settings)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"actual_cost":0.1`)
}

func TestDashboardLeaderboardHidesActualCostForOrdinaryUsersWhenSettingsUnavailable(t *testing.T) {
	repo := &userUsageRepoCapture{
		leaderboardRows: []usagestats.TokenLeaderboardRow{{
			UserID:     42,
			Email:      "user@example.com",
			ActualCost: 0.1,
		}},
	}
	router := newUserUsageRequestTypeTestRouterWithRole(repo, service.RoleUser)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `"actual_cost"`)
}

func TestDashboardLeaderboardPreservesVisibleZeroActualCost(t *testing.T) {
	repo := &userUsageRepoCapture{
		leaderboardRows: []usagestats.TokenLeaderboardRow{{
			UserID:     42,
			Email:      "user@example.com",
			ActualCost: 0,
		}},
	}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/leaderboard", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"actual_cost":0`)
}

func TestUserUsageListRequestTypePriority(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?request_type=ws_v2&stream=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(42), repo.listFilters.UserID)
	require.NotNil(t, repo.listFilters.RequestType)
	require.Equal(t, int16(service.RequestTypeWSV2), *repo.listFilters.RequestType)
	require.Nil(t, repo.listFilters.Stream)
}

func TestUserUsageListInvalidRequestType(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?request_type=invalid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserUsageListInvalidStream(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?stream=invalid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserUsageListNativeCompactionFilter(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?request_type=stream&native_compaction_v2=true", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, repo.listFilters.RequestType)
	require.Equal(t, int16(service.RequestTypeStream), *repo.listFilters.RequestType)
	require.NotNil(t, repo.listFilters.NativeCompactionV2)
	require.True(t, *repo.listFilters.NativeCompactionV2)
}

func TestUserUsageListInvalidNativeCompactionFilter(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?native_compaction_v2=unknown", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserUsageListAdvancedFilters(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?group_id=7&model=gpt-5&billing_type=1&billing_mode=image&start_date=2026-03-01&end_date=2026-03-02", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(42), repo.listFilters.UserID)
	require.Equal(t, int64(7), repo.listFilters.GroupID)
	require.Equal(t, "gpt-5", repo.listFilters.Model)
	require.Equal(t, usagestats.ModelSourceRequested, repo.listFilters.ModelFilterSource)
	require.NotNil(t, repo.listFilters.BillingType)
	require.Equal(t, int8(1), *repo.listFilters.BillingType)
	require.Equal(t, "image", repo.listFilters.BillingMode)
	require.NotNil(t, repo.listFilters.StartTime)
	require.NotNil(t, repo.listFilters.EndTime)
}

func TestUserUsageListInvalidBillingMode(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?billing_mode=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserUsageListAllowsVideoBillingMode(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?billing_mode=video", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "video", repo.listFilters.BillingMode)
}

func TestUserUsageListKeepsUserBillingAndIPWithoutAdminCostFields(t *testing.T) {
	ipAddress := "203.0.113.10"
	upstreamModel := "upstream-private-model"
	billingTier := "internal-tier"
	channelID := int64(99)
	accountRateMultiplier := 1.7
	accountStatsCost := 0.12
	repo := &userUsageRepoCapture{
		listRows: []service.UsageLog{{
			ID:                    1,
			UserID:                42,
			APIKeyID:              7,
			AccountID:             5,
			RequestID:             "req_user_billing",
			Model:                 "gpt-5",
			NativeCompactionV2:    true,
			InputCost:             0.01,
			OutputCost:            0.02,
			CacheCreationCost:     0.03,
			CacheReadCost:         0.04,
			TotalCost:             0.10,
			ActualCost:            0.08,
			RateMultiplier:        0.8,
			IPAddress:             &ipAddress,
			UpstreamModel:         &upstreamModel,
			BillingTier:           &billingTier,
			ChannelID:             &channelID,
			AccountRateMultiplier: &accountRateMultiplier,
			AccountStatsCost:      &accountStatsCost,
		}},
	}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `"input_cost":0.01`)
	require.Contains(t, body, `"output_cost":0.02`)
	require.Contains(t, body, `"cache_creation_cost":0.03`)
	require.Contains(t, body, `"cache_read_cost":0.04`)
	require.Contains(t, body, `"total_cost":0.1`)
	require.Contains(t, body, `"actual_cost":0.08`)
	require.Contains(t, body, `"rate_multiplier":0.8`)
	require.Contains(t, body, `"native_compaction_v2":true`)
	require.Contains(t, body, `"ip_address":"203.0.113.10"`)
	require.NotContains(t, body, "upstream_endpoint")
	require.NotContains(t, body, "account_rate_multiplier")
	require.NotContains(t, body, "account_stats_cost")
	require.NotContains(t, body, "upstream_model")
	require.NotContains(t, body, "upstream_response_model")
	require.NotContains(t, body, "upstream_model_mismatch")
	require.NotContains(t, body, "billing_tier")
	require.NotContains(t, body, "channel_id")
	require.NotContains(t, body, `"account":`)
}

func TestUserUsageStatsUsesScopedFilters(t *testing.T) {
	accountCost := 0.12
	repo := &userUsageRepoCapture{
		stats: &usagestats.UsageStats{
			TotalCost:        0.10,
			TotalActualCost:  0.08,
			TotalAccountCost: &accountCost,
			UpstreamEndpoints: []usagestats.EndpointStat{{
				Endpoint: "/v1/responses",
			}},
			EndpointPaths: []usagestats.EndpointStat{{
				Endpoint: "/v1/chat/completions -> /v1/responses",
			}},
		},
	}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/stats?group_id=9&request_type=sync&billing_mode=token&start_date=2026-03-01&end_date=2026-03-02", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(42), repo.statsFilters.UserID)
	require.Equal(t, int64(9), repo.statsFilters.GroupID)
	require.Equal(t, usagestats.ModelSourceRequested, repo.statsFilters.ModelFilterSource)
	require.NotNil(t, repo.statsFilters.RequestType)
	require.Equal(t, int16(service.RequestTypeSync), *repo.statsFilters.RequestType)
	require.Equal(t, "token", repo.statsFilters.BillingMode)
	require.Contains(t, rec.Body.String(), `"total_cost":0.1`)
	require.Contains(t, rec.Body.String(), `"total_actual_cost":0.08`)
	require.NotContains(t, rec.Body.String(), "total_account_cost")
	require.NotContains(t, rec.Body.String(), "upstream_endpoints")
	require.NotContains(t, rec.Body.String(), "endpoint_paths")
}

func TestUserUsageDashboardModelsOmitsAccountCost(t *testing.T) {
	repo := &userUsageRepoCapture{
		modelStats: []usagestats.ModelStat{{
			Model:       "gpt-5",
			Requests:    2,
			TotalTokens: 30,
			Cost:        0.10,
			ActualCost:  0.08,
			AccountCost: 0.07,
		}},
	}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/models?start_date=2026-03-01&end_date=2026-03-02", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `"cost":0.1`)
	require.Contains(t, body, `"actual_cost":0.08`)
	require.NotContains(t, body, "account_cost")
}

func TestUserUsageDashboardModelsRejectsAdminModelSources(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/models?model_source=upstream&start_date=2026-03-01&end_date=2026-03-02", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserUsageSnapshotUsesScopedFilters(t *testing.T) {
	repo := &userUsageRepoCapture{
		modelStats: []usagestats.ModelStat{{Model: "gpt-5", AccountCost: 0.07}},
		groupStats: []usagestats.GroupStat{{GroupID: 1, GroupName: "default", AccountCost: 0.06}},
	}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/snapshot-v2?include_trend=true&include_model_stats=true&include_group_stats=true&group_id=11&request_type=stream&start_date=2026-03-01&end_date=2026-03-02", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(42), repo.trendFilters.UserID)
	require.Equal(t, int64(11), repo.trendFilters.GroupID)
	require.NotNil(t, repo.trendFilters.RequestType)
	require.Equal(t, int16(service.RequestTypeStream), *repo.trendFilters.RequestType)
	require.Equal(t, int64(42), repo.groupFilters.UserID)
	require.Equal(t, int64(11), repo.groupFilters.GroupID)
	require.NotContains(t, rec.Body.String(), "account_cost")
}

func TestUserUsageSnapshotRejectsInvalidIncludeFlags(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	for _, query := range []string{
		"include_trend=bad",
		"include_model_stats=bad",
		"include_group_stats=bad",
	} {
		req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/snapshot-v2?start_date=2026-03-01&end_date=2026-03-02&"+query, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code, query)
	}
}
