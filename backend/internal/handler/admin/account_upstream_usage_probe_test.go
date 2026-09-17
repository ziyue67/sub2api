package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type upstreamUsageSnapshotAdminService struct {
	*stubAdminService
	accounts []*service.Account
	calls    int
}

func (s *upstreamUsageSnapshotAdminService) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	s.calls++
	byID := make(map[int64]*service.Account, len(s.accounts))
	for _, account := range s.accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	result := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if account, ok := byID[id]; ok {
			result = append(result, account)
		}
	}
	return result, nil
}

func setupUpstreamUsageProbeRouter(adminSvc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	// A non-nil but unavailable service makes valid probe requests deterministic:
	// no upstream transport is present, so the handler returns per-account errors.
	handler.SetUpstreamBillingProbeService(service.NewUpstreamBillingProbeService(nil, nil, nil))
	router := gin.New()
	router.POST("/admin/accounts/:id/upstream-usage-probe", handler.ProbeUpstreamUsage)
	router.POST("/admin/accounts/upstream-usage-probe/batch", handler.ProbeUpstreamUsageBatch)
	router.GET("/admin/accounts/upstream-usage-snapshots", handler.GetUpstreamUsageSnapshots)
	return router
}

func TestAccountHandlerProbeUpstreamUsageRejectsInvalidID(t *testing.T) {
	router := setupUpstreamUsageProbeRouter(newStubAdminService())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/accounts/not-an-id/upstream-usage-probe", nil))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestAccountHandlerProbeUpstreamUsageBatchValidatesAndDeduplicatesIDs(t *testing.T) {
	router := setupUpstreamUsageProbeRouter(newStubAdminService())

	for _, body := range []string{
		`{"account_ids":[]}`,
		`{"account_ids":[0,-1]}`,
		`{"account_ids":[1,0,2]}`,
		`{"account_ids":[1,` + strings.Repeat("2,", 20) + `3]}`,
		`{"account_ids":[1`,
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/admin/accounts/upstream-usage-probe/batch", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code, body)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/accounts/upstream-usage-probe/batch", bytes.NewBufferString(`{"account_ids":[7,7,8]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Data struct {
			Results []service.UpstreamUsageProbeResult `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Results, 2)
	require.Equal(t, int64(7), envelope.Data.Results[0].AccountID)
	require.Equal(t, int64(8), envelope.Data.Results[1].AccountID)
	require.Equal(t, service.ErrUpstreamUsageProbeUnavailable.Error(), envelope.Data.Results[0].Error)
}

func TestAccountHandlerGetUpstreamUsageSnapshotsIsReadOnlyAndRedactsCredentials(t *testing.T) {
	now := time.Date(2026, 9, 17, 1, 2, 3, 0, time.UTC)
	adminSvc := &upstreamUsageSnapshotAdminService{
		stubAdminService: newStubAdminService(),
		accounts: []*service.Account{
			{
				ID:          42,
				Platform:    service.PlatformOpenAI,
				Type:        service.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "sk-never-return-this"},
				Extra: map[string]any{
					service.UpstreamUsageProbeExtraKey: &service.UpstreamUsageProbeSnapshot{
						Status:        service.UpstreamUsageProbeStatusOK,
						Data:          map[string]any{"remaining": 12.5, "unit": "USD"},
						FetchedAt:     &now,
						FreshUntil:    ptrTime(now.Add(time.Hour)),
						LastAttemptAt: now,
						NextProbeAt:   now.Add(time.Hour),
					},
				},
			},
		},
	}
	router := setupUpstreamUsageProbeRouter(adminSvc)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/accounts/upstream-usage-snapshots?ids=42,42,999", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, adminSvc.calls)
	body := recorder.Body.String()
	require.NotContains(t, body, "sk-never-return-this")
	require.NotContains(t, body, "credentials")
	require.NotContains(t, body, "api_key")
	var envelope struct {
		Data struct {
			Items []service.UpstreamUsageSnapshotItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Items, 2)
	require.Equal(t, int64(42), envelope.Data.Items[0].AccountID)
	require.Equal(t, 12.5, envelope.Data.Items[0].Snapshot.Data["remaining"])
	require.Equal(t, int64(999), envelope.Data.Items[1].AccountID)
	require.Nil(t, envelope.Data.Items[1].Snapshot)
}

func TestAccountHandlerGetUpstreamUsageSnapshotsValidatesIDs(t *testing.T) {
	router := setupUpstreamUsageProbeRouter(newStubAdminService())
	for _, query := range []string{"", "ids=abc", "ids=0", "ids=1,,2"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/accounts/upstream-usage-snapshots?"+query, nil))
		require.Equal(t, http.StatusBadRequest, recorder.Code, query)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
