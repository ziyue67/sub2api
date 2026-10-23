package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsRegistrationIPLimitDefaultAndPersistence(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, get.Code)
	require.Contains(t, get.Body.String(), `"registration_ip_limit_enabled":true`)

	for _, enabled := range []bool{false, true} {
		rec := doUpdateSettings(t, h, map[string]any{"registration_ip_limit_enabled": enabled}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		want := "false"
		if enabled {
			want = "true"
		}
		require.Equal(t, want, repo.values["registration_ip_limit_enabled"])
		require.Contains(t, rec.Body.String(), `"registration_ip_limit_enabled":`+want)
		rec = doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, want, repo.values["registration_ip_limit_enabled"], "old clients must preserve the switch")
	}
	require.Contains(t, diffSettings(&service.SystemSettings{RegistrationIPLimitEnabled: true}, &service.SystemSettings{}, nil, nil, UpdateSettingsRequest{}), "registration_ip_limit_enabled")
}
