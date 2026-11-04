//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type legacyIPHandlerRepo struct {
	service.SettingRepository
	value  string
	writes int
}

func (r *legacyIPHandlerRepo) GetValue(context.Context, string) (string, error) {
	if r.value == "" {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}
func (r *legacyIPHandlerRepo) Set(_ context.Context, _ string, v string) error {
	r.value = v
	r.writes++
	return nil
}

func TestLegacyAdminKeyIPHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &legacyIPHandlerRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	request := func(method, role, body string, fn gin.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("PUT", "/ip-policy", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("auth_method", method)
		c.Set("user_role", role)
		fn(c)
		return w
	}
	for _, method := range []string{"", "admin_api_key", "scoped_admin_key"} {
		for _, fn := range []gin.HandlerFunc{h.GetLegacyAdminKeyIPPolicy, h.UpdateLegacyAdminKeyIPPolicy} {
			require.Equal(t, 403, request(method, "admin", `{"enabled":false,"allowed_ips":[]}`, fn).Code)
		}
	}
	require.Zero(t, repo.writes)
	require.Equal(t, 403, request(service.AuditAuthMethodJWT, "observer", "", h.GetLegacyAdminKeyIPPolicy).Code)
	for _, body := range []string{`{}`, `{"enabled":true}`, `{"allowed_ips":[]}`, `{"enabled":true,"allowed_ips":[]}`, `{"enabled":false,"allowed_ips":["::/0"]}`} {
		require.Equal(t, 400, request(service.AuditAuthMethodJWT, "admin", body, h.UpdateLegacyAdminKeyIPPolicy).Code)
	}
	require.Zero(t, repo.writes)
	require.Equal(t, 200, request(service.AuditAuthMethodJWT, "admin", `{"enabled":true,"allowed_ips":[" 192.0.2.1 "]}`, h.UpdateLegacyAdminKeyIPPolicy).Code)
	require.Equal(t, 1, repo.writes)
	var p service.LegacyAdminKeyIPPolicy
	require.NoError(t, json.Unmarshal([]byte(repo.value), &p))
	require.True(t, p.Enabled)
	require.Equal(t, []string{"192.0.2.1"}, p.AllowedIPs)
	repo.value = `broken`
	require.Equal(t, 503, request(service.AuditAuthMethodJWT, "admin", "", h.GetLegacyAdminKeyIPPolicy).Code)
	require.Equal(t, 200, request(service.AuditAuthMethodJWT, "admin", `{"enabled":false,"allowed_ips":[]}`, h.UpdateLegacyAdminKeyIPPolicy).Code)
}
