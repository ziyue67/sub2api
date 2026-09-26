package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSettingsExcelBPSImagesRoundTripAndOmission(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{
		"excel_bps_image_relay_enabled":  true,
		"excel_bps_image_base_url":       " https://images.example/ ",
		"excel_bps_image_body_limit_mib": 32,
		"excel_bps_image_budget_mib":     768,
		"excel_bps_image_max_requests":   512,
		"excel_bps_image_max_images":     40,
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, gjson.Get(rec.Body.String(), "data.excel_bps_image_relay_enabled").Bool())
	require.Equal(t, "https://images.example", gjson.Get(rec.Body.String(), "data.excel_bps_image_base_url").String())
	require.Equal(t, "true", repo.values[service.SettingKeyExcelBPSImageRelayEnabled])
	require.Equal(t, "https://images.example", repo.values[service.SettingKeyExcelBPSImageBaseURL])
	require.Equal(t, "32", repo.values[service.SettingKeyExcelBPSImageBodyLimitMiB])
	require.Equal(t, "768", repo.values[service.SettingKeyExcelBPSImageBudgetMiB])
	require.Equal(t, "512", repo.values[service.SettingKeyExcelBPSImageMaxRequests])
	require.Equal(t, "40", repo.values[service.SettingKeyExcelBPSImageMaxImages])
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "keep relay"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, gjson.Get(rec.Body.String(), "data.excel_bps_image_relay_enabled").Bool())
	require.Equal(t, "https://images.example", gjson.Get(rec.Body.String(), "data.excel_bps_image_base_url").String())
	require.Equal(t, int64(32), gjson.Get(rec.Body.String(), "data.excel_bps_image_body_limit_mib").Int())
	require.Equal(t, int64(768), gjson.Get(rec.Body.String(), "data.excel_bps_image_budget_mib").Int())
	require.Equal(t, int64(512), gjson.Get(rec.Body.String(), "data.excel_bps_image_max_requests").Int())
	require.Equal(t, int64(40), gjson.Get(rec.Body.String(), "data.excel_bps_image_max_images").Int())
	public, err := h.settingService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "images.example", "relay configuration is admin-only")
	rec = doUpdateSettings(t, h, map[string]any{"excel_bps_image_base_url": "http://invalid.example"}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "https://images.example", repo.values[service.SettingKeyExcelBPSImageBaseURL])
	for _, field := range []string{"excel_bps_image_body_limit_mib", "excel_bps_image_budget_mib", "excel_bps_image_max_requests", "excel_bps_image_max_images"} {
		rec = doUpdateSettings(t, h, map[string]any{field: 0}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, field)
	}
	rec = doUpdateSettings(t, h, map[string]any{"excel_bps_image_max_requests": 513}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "512", repo.values[service.SettingKeyExcelBPSImageMaxRequests])
	require.Equal(t, "32", repo.values[service.SettingKeyExcelBPSImageBodyLimitMiB])
	rec = doUpdateSettings(t, h, map[string]any{"excel_bps_image_max_images": 513}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "40", repo.values[service.SettingKeyExcelBPSImageMaxImages])
	rec = doUpdateSettings(t, h, map[string]any{"excel_bps_image_relay_enabled": false}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "false", repo.values[service.SettingKeyExcelBPSImageRelayEnabled])
	require.Equal(t, "https://images.example", repo.values[service.SettingKeyExcelBPSImageBaseURL])
}

func TestSettingsExcelBPSImagesRequireHTTPSOriginWhenEnabled(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{"excel_bps_image_relay_enabled": true}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "INVALID_EXCEL_BPS_IMAGE_BASE_URL")
}

func TestSettingsExcelBPSNativeModePersistsWithoutOrigin(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{"excel_bps_image_relay_enabled": true, "excel_bps_image_mode": "native"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "native", gjson.Get(rec.Body.String(), "data.excel_bps_image_mode").String())
	require.Empty(t, repo.values[service.SettingKeyExcelBPSImageBaseURL])
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "preserve native"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "native", repo.values[service.SettingKeyExcelBPSImageMode])
	for _, mode := range []string{"relay", "invalid"} {
		rec = doUpdateSettings(t, h, map[string]any{"excel_bps_image_mode": mode}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Equal(t, "native", repo.values[service.SettingKeyExcelBPSImageMode])
	}
	settings, err := h.settingService.GetExcelBPSImageRelaySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, service.ExcelBPSImageModeNative, settings.Mode)
	require.True(t, settings.Enabled)
}

func TestSettingsExcelBPSImageLimitsValidationAndPreservation(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	values := map[string]any{
		"excel_bps_image_max_image_mib": 30, "excel_bps_image_max_images": 100,
		"excel_bps_image_max_total_mib": 64, "excel_bps_image_storage_mib": 2048,
		"excel_bps_image_storage_entries": 2048, "excel_bps_image_ttl_minutes": 60,
	}
	rec := doUpdateSettings(t, h, values, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for k, v := range values {
		require.EqualValues(t, v, gjson.Get(rec.Body.String(), "data."+k).Int())
	}
	// Old clients omit the added fields: do not reset saved limits.
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "preserve image limits"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for k, v := range values {
		require.EqualValues(t, v, gjson.Get(rec.Body.String(), "data."+k).Int())
	}
	for _, bad := range []map[string]any{
		{"excel_bps_image_max_images": 0}, {"excel_bps_image_max_images": 4097},
		{"excel_bps_image_max_image_mib": 129}, {"excel_bps_image_max_total_mib": 20},
		{"excel_bps_image_storage_mib": 32}, {"excel_bps_image_storage_mib": 16385},
		{"excel_bps_image_storage_entries": 99}, {"excel_bps_image_storage_entries": 65537},
		{"excel_bps_image_ttl_minutes": 1441}, {"excel_bps_image_ttl_minutes": -1},
		{"excel_bps_image_max_images": 1.5},
	} {
		rec = doUpdateSettings(t, h, bad, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Equal(t, "100", repo.values[service.SettingKeyExcelBPSImageMaxImages])
		require.Equal(t, "64", repo.values[service.SettingKeyExcelBPSImageMaxTotalMiB])
	}
}
