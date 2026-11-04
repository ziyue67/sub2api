//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type legacyIPSettingsRepo struct {
	service.SettingRepository
	policy string
	err    error
}

func (r *legacyIPSettingsRepo) GetValue(_ context.Context, k string) (string, error) {
	if k == "admin_api_key" {
		return "legacy-secret", nil
	}
	if k == service.SettingKeyLegacyAdminKeyIPPolicy {
		if r.err != nil {
			return "", r.err
		}
		if r.policy == "" {
			return "", service.ErrSettingNotFound
		}
		return r.policy, nil
	}
	return "", service.ErrSettingNotFound
}

type legacyIPUsersRepo struct {
	*stubUserRepo
	admin *service.User
}

func (r *legacyIPUsersRepo) GetFirstAdmin(context.Context) (*service.User, error) {
	return r.admin, nil
}

func TestLegacyAdminKeyIPAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := &service.User{ID: 1, Email: "lab@example.invalid", Role: service.RoleAdmin, Status: service.StatusActive}
	users := service.NewUserService(&legacyIPUsersRepo{&stubUserRepo{getByID: func(context.Context, int64) (*service.User, error) { return admin, nil }}, admin}, nil, nil, nil)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	jwt, e := auth.GenerateToken(context.Background(), admin)
	require.NoError(t, e)
	repo := &legacyIPSettingsRepo{policy: `{"enabled":true,"allowed_ips":["192.0.2.1"]}`}
	settings := service.NewSettingService(repo, cfg)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(adminAuth(auth, users, settings, nil))
	calls := 0
	r.GET("/test", func(c *gin.Context) { calls++; c.Status(200) })
	request := func(ip, key string, withJWT bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		req.Header.Set("X-Real-IP", "192.0.2.1")
		req.Header.Set("CF-Connecting-IP", "192.0.2.1")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		if withJWT {
			req.Header.Set("Authorization", "Bearer "+jwt)
		}
		r.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, 200, request("192.0.2.1", "legacy-secret", false).Code)
	require.Equal(t, 403, request("192.0.2.2", "legacy-secret", false).Code)
	require.Equal(t, 403, request("192.0.2.2", "legacy-secret", true).Code)
	require.Equal(t, 1, calls)
	require.Equal(t, 200, request("192.0.2.2", "", true).Code)
	require.Equal(t, 401, request("192.0.2.1", "wrong", false).Code)
	repo.err = errors.New("offline")
	require.Equal(t, 503, request("192.0.2.1", "legacy-secret", false).Code)
	require.Equal(t, 401, request("192.0.2.1", "wrong", false).Code)
	require.Equal(t, 200, request("192.0.2.2", "", true).Code)
	repo.err = nil
	repo.policy = `{"enabled":true,"allowed_ips":[]}`
	require.Equal(t, 503, request("192.0.2.1", "legacy-secret", false).Code)
	repo.policy = ""
	require.Equal(t, 200, request("192.0.2.2", "legacy-secret", false).Code)
}
