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

func requireUsageUpstreamResponseModelVisibility(t *testing.T, h *SettingHandler, want bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, want, gjson.Get(rec.Body.String(), "data.usage_show_upstream_response_model_to_users").Bool())

	public, err := h.settingService.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, public.UsageShowUpstreamResponseModelToUsers)

	raw, err := h.settingService.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	payload, ok := raw.(*service.PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.Equal(t, want, payload.UsageShowUpstreamResponseModelToUsers)
	require.Equal(t, want, h.settingService.IsUserUpstreamResponseModelVisible(context.Background()))
}

func TestSettingsUsageUpstreamResponseModelVisibilityDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored map[string]string
		want   bool
	}{
		{name: "missing defaults to hidden", stored: map[string]string{}, want: false},
		{name: "empty stays hidden", stored: map[string]string{service.SettingKeyUsageShowUpstreamResponseModelToUsers: ""}, want: false},
		{name: "explicit false", stored: map[string]string{service.SettingKeyUsageShowUpstreamResponseModelToUsers: "false"}, want: false},
		{name: "explicit true", stored: map[string]string{service.SettingKeyUsageShowUpstreamResponseModelToUsers: "true"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newStepUpSwitchTestHandler(t, tc.stored)
			requireUsageUpstreamResponseModelVisibility(t, h, tc.want)
		})
	}
}

func TestSettingsUsageUpstreamResponseModelVisibilityRoundTripAndOmission(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	for _, step := range []struct {
		name   string
		body   map[string]any
		want   bool
		stored string
	}{
		{name: "omit on legacy settings", body: map[string]any{"site_name": "Legacy Gateway"}, want: false, stored: "false"},
		{name: "enable", body: map[string]any{"usage_show_upstream_response_model_to_users": true}, want: true, stored: "true"},
		{name: "omit while enabled", body: map[string]any{"site_name": "Visible Model"}, want: true, stored: "true"},
		{name: "disable", body: map[string]any{"usage_show_upstream_response_model_to_users": false}, want: false, stored: "false"},
		{name: "omit while disabled", body: map[string]any{"site_name": "Hidden Model"}, want: false, stored: "false"},
	} {
		t.Run(step.name, func(t *testing.T) {
			rec := doUpdateSettings(t, h, step.body, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, step.want, gjson.Get(rec.Body.String(), "data.usage_show_upstream_response_model_to_users").Bool())
			require.Equal(t, step.stored, repo.values[service.SettingKeyUsageShowUpstreamResponseModelToUsers])
			requireUsageUpstreamResponseModelVisibility(t, h, step.want)
		})
	}
}
