package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type timezoneSettingsRepo struct{ SettingRepository }

func (*timezoneSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == SettingKeyOpenAIRequestTimezoneEnabled {
		return "true", nil
	}
	return "", ErrSettingNotFound
}
func (*timezoneSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func timezoneEnabledSettings() *SettingService {
	return NewSettingService(&timezoneSettingsRepo{}, &config.Config{})
}

func TestRequestTimezoneSwitch(t *testing.T) {
	body := localeTestBody([]string{`<environment_context><timezone>Asia/Shanghai</timezone></environment_context>`}, nil)
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{openAIRequestTimezoneExtraKey: "Asia/Tokyo"}}
	for _, tc := range []struct {
		name, value string
		err         error
		enabled     bool
	}{
		{name: "missing", err: ErrSettingNotFound}, {name: "disabled", value: "false"},
		{name: "invalid", value: "invalid"}, {name: "read failure", err: errors.New("offline")},
		{name: "enabled", value: "true", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{settingService: NewSettingService(&codexTicketLifecycleSettings{get: func(context.Context, string) (string, error) { return tc.value, tc.err }}, &config.Config{})}
			for _, transport := range []string{"http", "ws"} {
				got := svc.normalizeRequestTimezone(context.Background(), account, body, transport)
				if !tc.enabled {
					require.Equal(t, body, got)
					continue
				}
				require.Contains(t, gjson.GetBytes(got, "input.0.content.0.text").String(), "Asia/Tokyo")
				require.Equal(t, "Asia/Tokyo", gjson.GetBytes(got, "tools.0.user_location.timezone").String())
			}
		})
	}
	require.Equal(t, body, (&OpenAIGatewayService{}).normalizeRequestTimezone(context.Background(), account, body, "http"))
	svc := &OpenAIGatewayService{settingService: timezoneEnabledSettings()}
	require.Equal(t, body, svc.normalizeRequestTimezone(context.Background(), &Account{Platform: PlatformAnthropic}, body, "http"))
}

func TestRequestTimezoneSettingCacheInvalidation(t *testing.T) {
	value := "true"
	settings := NewSettingService(&codexTicketLifecycleSettings{get: func(context.Context, string) (string, error) { return value, nil }}, &config.Config{})
	require.True(t, settings.GetOpenAIRequestTimezoneEnabled(context.Background(), false))
	value = "false"
	settings.InvalidateOpenAIRequestTimezoneEnabledCache()
	require.False(t, settings.GetOpenAIRequestTimezoneEnabled(context.Background(), false))
	require.False(t, settings.parseSettings(map[string]string{}).OpenAIRequestTimezoneEnabled)
	require.True(t, settings.parseSettings(map[string]string{SettingKeyOpenAIRequestTimezoneEnabled: "true"}).OpenAIRequestTimezoneEnabled)
}
