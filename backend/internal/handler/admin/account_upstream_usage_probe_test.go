package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupUpstreamUsageProbeRouter(adminSvc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	// A non-nil but unavailable service makes valid probe requests deterministic:
	// no upstream transport is present, so the handler returns per-account errors.
	handler.SetUpstreamBillingProbeService(service.NewUpstreamBillingProbeService(nil, nil, nil))
	router := gin.New()
	router.POST("/admin/accounts/:id/upstream-usage-probe", handler.ProbeUpstreamUsage)
	router.POST("/admin/accounts/upstream-usage-probe/batch", handler.ProbeUpstreamUsageBatch)
	return router
}

func TestAccountHandlerProbeUpstreamUsageRejectsInvalidID(t *testing.T) {
	router := setupUpstreamUsageProbeRouter(newStubAdminService())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/accounts/not-an-id/upstream-usage-probe", nil))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestAccountHandlerProbeUpstreamUsageReturnsStructuredServiceUnavailable(t *testing.T) {
	handler := NewAccountHandler(newStubAdminService(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/admin/accounts/:id/upstream-usage-probe", handler.ProbeUpstreamUsage)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/accounts/7/upstream-usage-probe", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, http.StatusServiceUnavailable, envelope.Code)
	require.Equal(t, "upstream usage probe is unavailable", envelope.Message)
	require.Equal(t, "UPSTREAM_USAGE_PROBE_UNAVAILABLE", envelope.Reason)
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
