package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func requireUsageCacheRateSettings(t *testing.T, h *SettingHandler, enabled, showToUsers bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, enabled, gjson.Get(rec.Body.String(), "data.usage_show_cache_rate").Bool())
	require.Equal(t, showToUsers, gjson.Get(rec.Body.String(), "data.usage_show_cache_rate_to_users").Bool())

	public, err := h.settingService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, enabled, public.UsageShowCacheRate)
	require.Equal(t, showToUsers, public.UsageShowCacheRateToUsers)

	raw, err := h.settingService.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	payload, ok := raw.(*service.PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.Equal(t, enabled, payload.UsageShowCacheRate)
	require.Equal(t, showToUsers, payload.UsageShowCacheRateToUsers)
}

func TestSettingsUsageCacheRateDefaults(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{})
	requireUsageCacheRateSettings(t, h, true, false)

	h, _ = newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyUsageShowCacheRate:        "false",
		service.SettingKeyUsageShowCacheRateToUsers: "true",
	})
	requireUsageCacheRateSettings(t, h, false, true)
}

func TestSettingsUsageCacheRateRoundTripAndOmission(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	for _, step := range []struct {
		name         string
		body         map[string]any
		enabled      bool
		showToUsers  bool
		storedEnable string
		storedUsers  string
	}{
		{name: "omit keeps defaults", body: map[string]any{"site_name": "Legacy Gateway"}, enabled: true, showToUsers: false, storedEnable: "true", storedUsers: "false"},
		{name: "hide from admin", body: map[string]any{"usage_show_cache_rate": false}, enabled: false, showToUsers: false, storedEnable: "false", storedUsers: "false"},
		{name: "show to users", body: map[string]any{"usage_show_cache_rate": true, "usage_show_cache_rate_to_users": true}, enabled: true, showToUsers: true, storedEnable: "true", storedUsers: "true"},
		{name: "omit preserves user visibility", body: map[string]any{"site_name": "Still Visible"}, enabled: true, showToUsers: true, storedEnable: "true", storedUsers: "true"},
	} {
		t.Run(step.name, func(t *testing.T) {
			rec := doUpdateSettings(t, h, step.body, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, step.enabled, gjson.Get(rec.Body.String(), "data.usage_show_cache_rate").Bool())
			require.Equal(t, step.showToUsers, gjson.Get(rec.Body.String(), "data.usage_show_cache_rate_to_users").Bool())
			require.Equal(t, step.storedEnable, repo.values[service.SettingKeyUsageShowCacheRate])
			require.Equal(t, step.storedUsers, repo.values[service.SettingKeyUsageShowCacheRateToUsers])
			requireUsageCacheRateSettings(t, h, step.enabled, step.showToUsers)
		})
	}
}
