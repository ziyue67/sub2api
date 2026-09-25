package admin

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestCaptureSettings(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	for _, body := range []map[string]any{
		{"request_capture_quota_mib": 0}, {"request_capture_quota_mib": -1}, {"request_capture_quota_mib": 1.5},
		{"request_capture_retention_days": 0}, {"request_capture_retention_days": 31}, {"request_capture_quota_mib": int64(1<<63 - 1)},
	} {
		r := doUpdateSettings(t, h, body, nil)
		require.Equal(t, 400, r.Code, r.Body.String())
	}
	r := doUpdateSettings(t, h, map[string]any{"request_capture_enabled": true, "request_capture_quota_mib": 42, "request_capture_retention_days": 30}, nil)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.True(t, gjson.Get(r.Body.String(), "data.request_capture_enabled").Bool())
	require.Equal(t, "42", repo.values[service.SettingKeyRequestCaptureQuotaMiB])
	r = doUpdateSettings(t, h, map[string]any{"site_name": "keep capture"}, nil)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.EqualValues(t, 30, gjson.Get(r.Body.String(), "data.request_capture_retention_days").Int())
	require.True(t, gjson.Get(r.Body.String(), "data.request_capture_enabled").Bool())
	public, err := h.settingService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "request_capture")
}
func TestRequestCaptureGateAndValidation(t *testing.T) {
	h := NewRequestCaptureHandler(nil, nil, nil, nil)
	router := gin.New()
	called := false
	router.GET("/capture", h.Gate, func(c *gin.Context) { called = true; c.Status(200) })
	r := httptest.NewRecorder()
	router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/capture", nil))
	require.Equal(t, 404, r.Code)
	require.False(t, called)
	for _, body := range []string{`{"target_id":0,"duration_minutes":1}`, `{"target_type":"user","target_id":1,"duration_minutes":1441}`, `{"target_type":"all","target_id":1,"duration_minutes":10}`, `{"target_type":"user","target_id":1,"duration_minutes":1.5}`} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest(http.MethodPost, "/capture", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h.Create(c)
		require.Equal(t, 400, r.Code, r.Body.String())
	}
}
