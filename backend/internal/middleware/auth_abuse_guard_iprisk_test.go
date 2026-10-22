package middleware

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type riskGuardStore struct{ config iprisk.Config }

func (s *riskGuardStore) Load(context.Context) (iprisk.Config, error)   { return s.config, nil }
func (s *riskGuardStore) Save(_ context.Context, c iprisk.Config) error { s.config = c; return nil }

func TestAuthAbuseIPRiskWhitelistRetainsRegistrationAccounting(t *testing.T) {
	for name, observe := range map[string]bool{"enforce": false, "observe": true} {
		t.Run(name, func(t *testing.T) { testIPRiskWhitelistAccounting(t, observe) })
	}
}

func testIPRiskWhitelistAccounting(t *testing.T, observe bool) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	config := iprisk.DefaultConfig()
	config.Enabled = true
	config.ObserveOnly = observe
	config.Whitelist = []string{"198.51.100.7"}
	m := iprisk.New(&riskGuardStore{config: config}, client, nil, false)
	legacyKey := "auth_abuse:{" + authguard.IPKey("198.51.100.7") + "}:banned"
	require.NoError(t, server.Set(legacyKey, "1"))
	server.SetTTL(legacyKey, time.Hour)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(m.Middleware(), AuthAbuseGuard(client))
	r.POST("/api/v1/auth/register", func(c *gin.Context) {
		key, enabled := authguard.RegistrationIP(c.Request.Context())
		require.True(t, enabled)
		require.Equal(t, authguard.IPKey("198.51.100.7"), key)
		c.Status(200)
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/register", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("User-Agent", "python-requests")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
}

func TestAuthAbuseIPRiskObservationRetainsExistingBan(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	config := iprisk.DefaultConfig()
	config.Enabled = true
	config.ObserveOnly = true
	m := iprisk.New(&riskGuardStore{config: config}, client, nil, false)
	legacyKey := "auth_abuse:{" + authguard.IPKey("198.51.100.7") + "}:banned"
	require.NoError(t, server.Set(legacyKey, "1"))
	server.SetTTL(legacyKey, time.Hour)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(m.Middleware(), AuthAbuseGuard(client))
	r.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("User-Agent", "Mozilla/5.0")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 429, w.Code)
	require.NoError(t, m.Unban(context.Background(), "198.51.100.7/32"))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.Clone(context.Background()))
	require.Equal(t, 200, w.Code, "unban must also clear the baseline login IP ban")
}

// The upstream login-limit switch and the optional IP risk module remain
// independent: disabling IP risk must restore the configured baseline behavior.
func TestAuthAbuseIPRiskDisabledPreservesLoginLimitSwitch(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "login-limit-off", true: "login-limit-on"}[enabled], func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			defer func() { _ = client.Close() }()
			manager := iprisk.New(&riskGuardStore{config: iprisk.DefaultConfig()}, client, nil, false)
			defer manager.Stop()
			key := "auth_abuse:{" + authguard.IPKey("198.51.100.7") + "}:banned"
			require.NoError(t, server.Set(key, "1"))
			server.SetTTL(key, time.Hour)
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			router.Use(manager.Middleware(), AuthAbuseGuardWithOptions(client, AuthAbuseGuardOptions{
				LoginRateLimitEnabled: func(*gin.Context) bool { return enabled },
			}))
			router.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(200) })
			router.POST("/api/v1/auth/register", func(c *gin.Context) { c.Status(200) })
			request := func(path string) int {
				req := httptest.NewRequest("POST", path, nil)
				req.RemoteAddr = "198.51.100.7:1234"
				req.Header.Set("User-Agent", "python-requests")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w.Code
			}
			want := 200
			if enabled {
				want = 429
			}
			require.Equal(t, want, request("/api/v1/auth/login"))
			require.Equal(t, 429, request("/api/v1/auth/register"))
			require.Equal(t, []string{key}, server.Keys(), "disabled IP risk must not create counters or events")
		})
	}
}
