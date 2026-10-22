package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newAuthRoutesTestRouter(redisClient *redis.Client, fixtures ...*handler.Handlers) *gin.Engine {
	return newAuthRoutesTestRouterWithSettings(redisClient, nil, fixtures...)
}

func newAuthRoutesTestRouterWithSettings(redisClient *redis.Client, settings *service.SettingService, fixtures ...*handler.Handlers) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")

	handlers := &handler.Handlers{
		Auth:    &handler.AuthHandler{},
		Setting: &handler.SettingHandler{},
	}
	if len(fixtures) > 0 {
		handlers = fixtures[0]
	}
	RegisterAuthRoutes(
		v1,
		handlers,
		servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
			c.Next()
		}),
		servermiddleware.AuditLogMiddleware(func(c *gin.Context) {
			c.Next()
		}),
		redisClient,
		settings,
		nil,
	)

	return router
}

func TestAuthRoutesRateLimitFailCloseWhenRedisUnavailable(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	router := newAuthRoutesTestRouter(rdb)
	paths := []string{
		"/api/v1/auth/register",
		"/api/v1/auth/login",
		"/api/v1/auth/login/2fa",
		"/api/v1/auth/send-verify-code",
		"/api/v1/auth/oauth/pending/send-verify-code",
	}

	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.10:12345"

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), "rate limit exceeded", "path=%s", path)
	}
}

func TestAuthRoutesBanLoginSprayBeforeRegisterHandler(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fixture := newAuthCredentialFixture(t)
	router := newAuthRoutesTestRouter(rdb, fixture)
	require.NoError(t, router.SetTrustedProxies(nil))
	request := func(address, path string) *httptest.ResponseRecorder {
		body := `{}`
		if path == "/api/v1/auth/login" {
			body = `{"email":"unknown@example.com","password":"incorrect"}`
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = address
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for i := 0; i < 10; i++ {
		require.Equal(t, http.StatusUnauthorized, request("192.0.2.1:1234", "/api/v1/auth/login").Code)
	}
	w := request("192.0.2.1:1234", "/api/v1/auth/register")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "3600", w.Header().Get("Retry-After"))
	require.Equal(t, http.StatusBadRequest, request("192.0.2.2:1234", "/api/v1/auth/register").Code)
	require.Equal(t, http.StatusBadRequest, request("192.0.2.1:1234", "/api/v1/auth/refresh").Code, "banned IP still reaches refresh validation")
	require.Equal(t, http.StatusOK, request("192.0.2.1:1234", "/api/v1/auth/logout").Code)
	mr.SetError("unavailable")
	require.Equal(t, http.StatusOK, request("192.0.2.1:1234", "/api/v1/auth/logout").Code)
	require.Equal(t, http.StatusTooManyRequests, request("192.0.2.1:1234", "/api/v1/auth/refresh").Code, "existing refresh limiter still fails closed")
}
