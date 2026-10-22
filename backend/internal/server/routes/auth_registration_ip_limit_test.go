package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type registrationIPLimitRouteRepo struct {
	service.SettingRepository
	value string
	err   error
}

func (r *registrationIPLimitRouteRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == "registration_ip_limit_enabled" {
		return r.value, r.err
	}
	return "", service.ErrSettingNotFound
}

func TestRegistrationIPLimitRoutePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, value string
		err         error
		want        bool
	}{
		{"missing", "", service.ErrSettingNotFound, true},
		{"enabled", "true", nil, true},
		{"disabled", "false", nil, false},
		{"invalid", "invalid", nil, true},
		{"lookup failure", "false", errors.New("database unavailable"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			repo := &registrationIPLimitRouteRepo{value: tc.value, err: tc.err}
			settings := service.NewSettingService(repo, &config.Config{})
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			want := tc.want
			router.Use(func(c *gin.Context) {
				c.Next()
				_, enabled := authguard.RegistrationIP(c.Request.Context())
				require.Equal(t, want, enabled, c.FullPath())
			})
			pass := func(c *gin.Context) { c.Next() }
			RegisterAuthRoutes(router.Group("/api/v1"), &handler.Handlers{
				Auth: &handler.AuthHandler{}, Setting: &handler.SettingHandler{},
			}, servermiddleware.JWTAuthMiddleware(pass), servermiddleware.AuditLogMiddleware(pass), client, settings, nil)
			for i := 0; i < 6; i++ {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = "192.0.2.40:1234"
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if i < 5 {
					require.Equal(t, http.StatusBadRequest, w.Code)
				} else {
					require.Equal(t, http.StatusTooManyRequests, w.Code, "request throttling must remain enabled")
				}
			}
			// A settings change applies to OAuth creation immediately, without restart.
			repo.value = "false"
			repo.err = nil
			want = false
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/create-account", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "192.0.2.41:1234"
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}
