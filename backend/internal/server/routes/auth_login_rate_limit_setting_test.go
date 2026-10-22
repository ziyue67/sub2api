package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type loginRateLimitRouteRepo struct {
	service.SettingRepository
	value string
	reads int
}

func (r *loginRateLimitRouteRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyLoginRateLimitEnabled {
		r.reads++
		return r.value, nil
	}
	return "", service.ErrSettingNotFound
}

func loginLimitRequest(router *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestAuthRoutesLoginRateLimitSettingBypassesBothLayers(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	repo := &loginRateLimitRouteRepo{value: "false"}
	settings := service.NewSettingService(repo, &config.Config{})
	fixture := &handler.Handlers{Auth: &handler.AuthHandler{}, Passkey: &handler.PasskeyHandler{}, Setting: &handler.SettingHandler{}}
	router := newAuthRoutesTestRouterWithSettings(rdb, settings, fixture)
	require.NoError(t, router.SetTrustedProxies(nil))
	banKey := "auth_abuse:{" + authguard.IPKey("192.0.2.1") + "}:banned"
	require.NoError(t, mr.Set(banKey, "1"))
	mr.SetTTL(banKey, time.Hour)

	paths := []string{"login", "login/2fa", "passkey/login/begin", "passkey/login/finish", "oauth/pending/bind-login", "oauth/linuxdo/bind-login", "oauth/wechat/bind-login", "oauth/oidc/bind-login", "oauth/dingtalk/bind-login"}
	for _, path := range paths {
		for i := 0; i < 25; i++ {
			before := repo.reads
			w := loginLimitRequest(router, path)
			require.Contains(t, []int{http.StatusBadRequest, http.StatusForbidden}, w.Code, "path=%s body=%s", path, w.Body.String())
			require.Equal(t, before+1, repo.reads, "both layers must share one setting snapshot")
		}
	}
	require.Equal(t, []string{banKey}, mr.Keys(), "disabled requests must not count or clear an existing ban")
	require.Equal(t, http.StatusTooManyRequests, loginLimitRequest(router, "register").Code)

	// Re-enable on the same router: the existing ban immediately applies again.
	repo.value = "true"
	require.Equal(t, http.StatusTooManyRequests, loginLimitRequest(router, "login").Code)
	require.NoError(t, rdb.Del(context.Background(), banKey).Err())
	for i := 0; i < 20; i++ {
		require.Equal(t, http.StatusBadRequest, loginLimitRequest(router, "login").Code)
	}
	require.Equal(t, http.StatusTooManyRequests, loginLimitRequest(router, "login").Code)
	repo.value = "false"
	require.Equal(t, http.StatusBadRequest, loginLimitRequest(router, "login").Code, "disabling must bypass an exhausted minute counter")

	// Redis/Lua failures must not defeat a deliberately disabled login limit.
	mr.SetError("Lua unavailable")
	for _, path := range paths {
		w := loginLimitRequest(router, path)
		require.Contains(t, []int{http.StatusBadRequest, http.StatusForbidden}, w.Code, "path=%s", path)
	}
	require.Equal(t, http.StatusTooManyRequests, loginLimitRequest(router, "register").Code)
	repo.value = "true"
	require.Equal(t, http.StatusTooManyRequests, loginLimitRequest(router, "login").Code)
}

func TestAuthRoutesDisabledLoginLimitDoesNotRecordFailures(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	repo := &loginRateLimitRouteRepo{value: "false"}
	settings := service.NewSettingService(repo, &config.Config{})
	router := newAuthRoutesTestRouterWithSettings(rdb, settings, newAuthCredentialFixture(t))
	require.NoError(t, router.SetTrustedProxies(nil))
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"unknown@example.com","password":"incorrect"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "INVALID_CREDENTIALS")
	}
	require.Empty(t, mr.Keys(), "disabled failures must not consume the minute or ban budget")
	repo.value = "true"
	require.Equal(t, http.StatusBadRequest, loginLimitRequest(router, "login").Code, "no ban was created while disabled")
}
