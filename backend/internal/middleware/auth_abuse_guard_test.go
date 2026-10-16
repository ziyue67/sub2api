package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newAbuseTestRouter(t *testing.T, client *redis.Client, status *int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	// Even with legacy forwarding trust enabled, new guards must ignore raw
	// spoofed headers when the peer is not a configured trusted proxy.
	router.Use(func(c *gin.Context) { ippkg.SetLegacyForwardedIPTrust(c, true) })
	router.Use(AuthAbuseGuard(client))
	for _, path := range []string{"/auth/login", "/auth/login/2fa", "/auth/oauth/pending/bind-login", "/auth/passkey/login/finish", "/auth/register", "/auth/refresh", "/auth/logout"} {
		router.POST(path, func(c *gin.Context) {
			key, enabled := authguard.RegistrationIP(c.Request.Context())
			require.Equal(t, c.FullPath() != "/auth/refresh" && c.FullPath() != "/auth/logout", enabled)
			if enabled {
				require.Equal(t, authguard.IPKey(c.ClientIP()), key)
			}
			reason := ""
			if *status == http.StatusUnauthorized {
				reason = "INVALID_CREDENTIALS"
			}
			response.ErrorWithDetails(c, *status, "test response", reason, nil)
		})
	}
	return router
}

func abuseRequest(router *gin.Engine, address, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = address
	req.Header.Set("X-Forwarded-For", "198.51.100.99")
	req.Header.Set("X-Real-IP", "198.51.100.98")
	req.Header.Set("CF-Connecting-IP", "198.51.100.97")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func newAbuseTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func TestAuthAbuseBanAcrossCredentialsAndInstances(t *testing.T) {
	mr, client := newAbuseTestRedis(t)
	status := http.StatusUnauthorized
	router := newAbuseTestRouter(t, client, &status)
	secondInstance := newAbuseTestRouter(t, client, &status)
	paths := []string{"/auth/login", "/auth/oauth/pending/bind-login", "/auth/login/2fa", "/auth/passkey/login/finish"}
	for i := 0; i < authFailureLimit; i++ {
		if i == 5 {
			status = http.StatusOK
			require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
		}
		status = http.StatusUnauthorized
		require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", paths[i%len(paths)]).Code)
	}
	status = http.StatusOK
	w := abuseRequest(secondInstance, "192.0.2.1:5678", "/auth/register")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "3600", w.Header().Get("Retry-After"))
	require.Equal(t, http.StatusOK, abuseRequest(router, "192.0.2.2:1234", "/auth/login").Code)
	mr.FastForward(30 * time.Minute)
	w = abuseRequest(router, "192.0.2.1:1234", "/auth/login")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "1800", w.Header().Get("Retry-After"), "blocked requests must not prolong bans")
	mr.FastForward(31 * time.Minute)
	require.Equal(t, http.StatusOK, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
}

func TestAuthAbuseWindowAndNonCredentialErrors(t *testing.T) {
	mr, client := newAbuseTestRedis(t)
	status := http.StatusInternalServerError
	router := newAbuseTestRouter(t, client, &status)
	for i := 0; i < authFailureLimit; i++ {
		require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
	}
	status = http.StatusBadRequest
	for i := 0; i < authFailureLimit; i++ {
		require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", "/auth/register").Code)
	}
	status = http.StatusUnauthorized
	for i := 0; i < authFailureLimit-1; i++ {
		require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
	}
	mr.FastForward(authFailureWindow + time.Second)
	require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
	status = http.StatusOK
	require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
}

func TestAuthAbuseOnlyCountsCredentialReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reason string
		banned bool
	}{
		{"password", 401, "INVALID_CREDENTIALS", true},
		{"totp", 400, "TOTP_INVALID_CODE", true},
		{"passkey", 401, "PASSKEY_VERIFICATION_FAILED", true},
		{"malformed", 400, "", false},
		{"captcha", 400, "TURNSTILE_INVALID_TOKEN", false},
		{"disabled account", 403, "USER_NOT_ACTIVE", false},
		{"backend mode", 403, "BACKEND_MODE_ADMIN_ONLY", false},
		{"expired oauth state", 401, "PENDING_AUTH_SESSION_EXPIRED", false},
		{"throttled", 429, "TOTP_TOO_MANY_ATTEMPTS", false},
		{"server error", 500, "INVALID_CREDENTIALS", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newAbuseTestRedis(t)
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			router.Use(AuthAbuseGuard(client))
			router.POST("/auth/login", func(c *gin.Context) {
				response.ErrorWithDetails(c, tc.status, "test failure", tc.reason, nil)
			})
			for i := 0; i < authFailureLimit; i++ {
				require.Equal(t, tc.status, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
			}
			want := tc.status
			if tc.banned {
				want = http.StatusTooManyRequests
			}
			require.Equal(t, want, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
		})
	}
}

func TestAuthAbuseSessionMaintenanceBypassesGuard(t *testing.T) {
	mr, client := newAbuseTestRedis(t)
	_, err := runAuthAbuse(context.Background(), client, authguard.IPKey("192.0.2.1"), "check")
	require.NoError(t, err)
	require.NoError(t, mr.Set("auth_abuse:{"+authguard.IPKey("192.0.2.1")+"}:banned", "1"))
	status := http.StatusOK
	router := newAbuseTestRouter(t, client, &status)
	for _, path := range []string{"/auth/refresh", "/auth/logout"} {
		require.Equal(t, status, abuseRequest(router, "192.0.2.1:1234", path).Code)
	}
	mr.SetError("unavailable")
	for _, path := range []string{"/auth/refresh", "/auth/logout"} {
		require.Equal(t, status, abuseRequest(router, "invalid", path).Code)
	}
	router = newAbuseTestRouter(t, nil, &status)
	require.Equal(t, status, abuseRequest(router, "invalid", "/auth/logout").Code)
}

func TestAuthAbuseIPv6RotationSharesBan(t *testing.T) {
	_, client := newAbuseTestRedis(t)
	status := http.StatusUnauthorized
	router := newAbuseTestRouter(t, client, &status)
	for i := 0; i < authFailureLimit; i++ {
		require.Equal(t, status, abuseRequest(router, "[2001:db8::1]:1234", "/auth/login").Code)
	}
	require.Equal(t, http.StatusTooManyRequests, abuseRequest(router, "[2001:db8::abcd]:1234", "/auth/login").Code)
	status = http.StatusOK
	require.Equal(t, status, abuseRequest(router, "[2001:db8:0:1::1]:1234", "/auth/login").Code)
}

func TestAuthAbuseRedisFailureAndInvalidOriginFailClosed(t *testing.T) {
	mr, client := newAbuseTestRedis(t)
	status := http.StatusOK
	router := newAbuseTestRouter(t, client, &status)
	require.Equal(t, http.StatusTooManyRequests, abuseRequest(router, "invalid", "/auth/login").Code)
	mr.SetError("unavailable")
	require.Equal(t, http.StatusTooManyRequests, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
	router = newAbuseTestRouter(t, nil, &status)
	require.Equal(t, http.StatusTooManyRequests, abuseRequest(router, "192.0.2.1:1234", "/auth/login").Code)
}

func TestAuthAbuseTrustedProxyKeepsClientsSeparate(t *testing.T) {
	_, client := newAbuseTestRedis(t)
	status := http.StatusUnauthorized
	router := newAbuseTestRouter(t, client, &status)
	require.NoError(t, router.SetTrustedProxies([]string{"127.0.0.1"}))
	request := func(address string) int {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", address)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < authFailureLimit; i++ {
		require.Equal(t, status, request("192.0.2.1"))
	}
	require.Equal(t, http.StatusTooManyRequests, request("192.0.2.1"))
	status = http.StatusOK
	require.Equal(t, status, request("192.0.2.2"))
}

func TestAuthAbuseRepairsNonExpiringState(t *testing.T) {
	mr, client := newAbuseTestRedis(t)
	ctx := context.Background()
	key := authguard.IPKey("192.0.2.1")
	prefix := "auth_abuse:{" + key + "}:"
	require.NoError(t, mr.Set(prefix+"failures", "1"))
	_, err := runAuthAbuse(ctx, client, key, "failure")
	require.NoError(t, err)
	require.Equal(t, authFailureWindow, mr.TTL(prefix+"failures"))
	require.NoError(t, mr.Set(prefix+"banned", "1"))
	ttl, err := runAuthAbuse(ctx, client, key, "check")
	require.NoError(t, err)
	require.Equal(t, authBanDuration.Milliseconds(), ttl)
	require.Equal(t, authBanDuration, mr.TTL(prefix+"banned"))
}
