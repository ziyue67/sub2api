//go:build unit

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type clientIPRiskStore struct {
	mu  sync.Mutex
	cfg iprisk.Config
}

func (s *clientIPRiskStore) Load(context.Context) (iprisk.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, nil
}

func (s *clientIPRiskStore) Save(_ context.Context, cfg iprisk.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	return nil
}

func TestLocalProxyClientIPControlsRiskRecordsAndRegistrationOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cfg := iprisk.DefaultConfig()
	cfg.Enabled, cfg.ObserveOnly = true, false
	cfg.Whitelist = []string{"127.0.0.1", "::1"}
	cfg.Blacklist = []iprisk.Rule{
		{Network: "198.51.100.10", Enabled: true},
		{Network: "2001:db8::10", Enabled: true},
	}
	cfg.LoginFailures.Limit = 1
	manager := iprisk.New(&clientIPRiskStore{cfg: cfg}, client, nil, false)
	t.Cleanup(manager.Stop)
	r := gin.New()
	configureTrustedProxies(r, config.ServerConfig{})
	// Legacy raw-header compatibility must not override either security guard.
	r.Use(func(c *gin.Context) { ippkg.SetLegacyForwardedIPTrust(c, true) })
	r.Use(manager.Middleware(), middleware.AuthAbuseGuard(client))
	r.POST("/api/v1/auth/register", func(c *gin.Context) {
		key, enabled := authguard.RegistrationIP(c.Request.Context())
		require.True(t, enabled)
		c.String(http.StatusOK, key)
	})
	r.POST("/api/v1/auth/login", func(c *gin.Context) {
		response.ErrorWithDetails(c, http.StatusUnauthorized, "Invalid credentials", "INVALID_CREDENTIALS", nil)
	})
	request := func(peer, visitor, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.RemoteAddr = peer
		// Nginx appends its real peer after any client-supplied prefix. Fake
		// loopback headers must not grant the real visitor a whitelist exemption.
		req.Header.Set("X-Forwarded-For", "127.0.0.1, "+visitor)
		req.Header.Set("X-Real-IP", "127.0.0.1")
		req.Header.Set("CF-Connecting-IP", "127.0.0.1")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, peer := range []string{"127.0.0.1:12345", "[::1]:12345", "[::ffff:127.0.0.1]:12345"} {
		for _, visitor := range []string{"198.51.100.10", "2001:db8::10"} {
			require.Equal(t, http.StatusForbidden, request(peer, visitor, "/api/v1/auth/register").Code)
		}
	}
	for _, visitor := range []string{"198.51.100.20", "198.51.100.21", "2001:db8:1::20"} {
		w := request("127.0.0.1:12345", visitor, "/api/v1/auth/register")
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, authguard.IPKey(visitor), w.Body.String(), "registration quota uses the real visitor")
		events, err := manager.Events(ctx, visitor, "", "")
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, "127.0.0.1", events[0].Peer)
		require.Equal(t, "trusted_proxy_chain", events[0].IPSource)
		require.Equal(t, "allowed", events[0].Decision)
	}
	events, err := manager.Events(ctx, "198.51.100.10", "", "")
	require.NoError(t, err)
	require.Len(t, events, 3)
	for _, event := range events {
		require.Equal(t, "ip_blacklist", event.Reason)
		require.Equal(t, "blocked", event.Decision)
		require.Equal(t, "trusted_proxy_chain", event.IPSource)
	}
	// A failed login bans its real visitor, not every user behind local Nginx.
	require.Equal(t, http.StatusUnauthorized, request("127.0.0.1:12345", "198.51.100.30", "/api/v1/auth/login").Code)
	require.Equal(t, http.StatusTooManyRequests, request("127.0.0.1:12345", "198.51.100.30", "/api/v1/auth/register").Code)
	require.Equal(t, http.StatusOK, request("127.0.0.1:12345", "198.51.100.31", "/api/v1/auth/register").Code)
	// Saving disabled must restore normal business handling for new requests.
	cfg.Enabled = false
	require.NoError(t, manager.Save(ctx, cfg))
	w := request("127.0.0.1:12345", "198.51.100.10", "/api/v1/auth/register")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, authguard.IPKey("198.51.100.10"), w.Body.String())
	events, err = manager.Events(ctx, "198.51.100.10", "", "")
	require.NoError(t, err)
	require.Len(t, events, 3, "disabled risk control does not write new events")
}
