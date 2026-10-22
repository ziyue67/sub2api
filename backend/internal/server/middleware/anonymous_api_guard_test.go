//go:build unit

package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type anonymousSettingsStore struct{ cfg iprisk.Config }

func (s *anonymousSettingsStore) Load(context.Context) (iprisk.Config, error) { return s.cfg, nil }
func (s *anonymousSettingsStore) Save(_ context.Context, c iprisk.Config) error {
	s.cfg = c
	return nil
}

func TestAnonymousGuardClassificationAndNATIsolation(t *testing.T) {
	for _, google := range []bool{false, true} {
		t.Run(fmt.Sprint(google), func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = client.Close() })
			settings := iprisk.DefaultConfig()
			settings.AnonymousAPI.Enabled = true
			settings.AnonymousAPI.Threshold = 2
			guard := iprisk.New(&anonymousSettingsStore{settings}, client, nil, false)
			t.Cleanup(guard.Stop)
			cfg := invalidAuthAbuseTestConfig(2)
			cfg.RunMode = config.RunModeStandard
			repo := &stubApiKeyRepo{getByKey: func(_ context.Context, k string) (*service.APIKey, error) {
				switch k {
				case "unknown":
					return nil, service.ErrAPIKeyNotFound
				case "db":
					return nil, errors.New("offline")
				case "overload":
					return nil, service.ErrAPIKeyAuthOverloaded
				}
				key := &service.APIKey{ID: 1, Status: service.StatusActive, User: &service.User{ID: 1, Status: service.StatusActive, Role: service.RoleUser, Balance: 1}}
				if k == "disabled" {
					key.Status = service.StatusAPIKeyDisabled
				}
				if k == "inactive-user" {
					key.User.Status = "disabled"
				}
				if k == "expired" {
					key.Status = service.StatusAPIKeyExpired
				}
				if k == "quota" {
					key.Status = service.StatusAPIKeyQuotaExhausted
				}
				if k == "balance" {
					key.User.Balance = 0
				}
				if k == "missing-user" {
					key.User = nil
				}
				if k == "group" {
					id := int64(1)
					key.GroupID = &id
					key.Group = &service.Group{ID: 1, Status: "disabled"}
				}
				if k == "acl" {
					key.IPWhitelist = []string{"9.9.9.9"}
				}
				return key, nil
			}}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			router.Use(guard.Middleware())
			require.NoError(t, router.SetTrustedProxies(nil))
			auth := gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg))
			if google {
				auth = APIKeyAuthGoogle(svc, cfg)
			}
			router.POST("/test", auth, func(c *gin.Context) {
				if c.GetHeader("upstream") == "401" {
					c.Status(401)
				} else if c.GetHeader("upstream") == "503" {
					c.Status(503)
				} else {
					c.Status(200)
				}
			})
			request := func(key, upstream string) int {
				req := httpRequest(t, "/test", "", key)
				req.RemoteAddr = "8.8.8.8:1234"
				req.Header.Set("upstream", upstream)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w.Code
			}
			for _, tc := range []struct {
				k, u string
				want int
			}{{"db", "", 500}, {"overload", "", 503}, {"inactive-user", "", 401}, {"acl", "", 403}, {"balance", "", 403}, {"missing-user", "", 401}, {"group", "", 403}, {"expired", "", 403}, {"quota", "", 429}, {"valid", "401", 401}, {"valid", "503", 503}} {
				require.Equal(t, tc.want, request(tc.k, tc.u), tc.k)
			}
			bans, err := guard.AnonymousBans(context.Background())
			require.NoError(t, err)
			require.Empty(t, bans)
			require.Equal(t, 401, request("unknown", ""))
			require.Equal(t, 429, request("disabled", ""))
			require.Equal(t, 200, request("valid", ""), "anonymous ban must never block a recognized valid key")
			require.Equal(t, 401, request("inactive-user", ""), "business rejection must retain original semantics during ban")
			require.Equal(t, 429, request("", ""))
			require.NoError(t, guard.UnbanAnonymous(context.Background(), "8.8.8.8"))
			require.Equal(t, 401, request("unknown", ""))
			server.Close()
			require.Equal(t, 401, request("unknown", ""), "Redis failure bypasses protection, not authentication")
			require.Equal(t, 200, request("valid", ""))
		})
	}
}

func TestAnonymousGuardTrustedProxyOnly(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprint(trusted), func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = client.Close() })
			settings := iprisk.DefaultConfig()
			settings.AnonymousAPI.Enabled = true
			settings.AnonymousAPI.Threshold = 2
			guard := iprisk.New(&anonymousSettingsStore{settings}, client, nil, false)
			t.Cleanup(guard.Stop)
			cfg := &config.Config{}
			svc := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			router.Use(guard.Middleware())
			require.NoError(t, router.SetTrustedProxies(nil))
			if trusted {
				require.NoError(t, router.SetTrustedProxies([]string{"8.8.8.8"}))
			}
			router.POST("/test", gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
			request := func(xff string) int {
				req := httptest.NewRequest("POST", "/test", nil)
				req.RemoteAddr = "8.8.8.8:1234"
				req.Header.Set("X-Forwarded-For", xff)
				req.Header.Set("Forwarded", "for=9.9.9.9")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w.Code
			}
			require.Equal(t, 401, request("1.1.1.1"))
			if trusted {
				require.Equal(t, 401, request("9.9.9.9"))
				require.Equal(t, 429, request("1.1.1.1"))
			} else {
				require.Equal(t, 429, request("9.9.9.9"))
			}
		})
	}
}

func TestAnonymousGuardWhitelistOverridesLegacyAndSuccessClears(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	settings := iprisk.DefaultConfig()
	settings.AnonymousAPI.Enabled = true
	settings.AnonymousAPI.Threshold = 2
	settings.AnonymousAPI.ResetOnSuccess = true
	settings.Whitelist = []string{"9.9.9.9"}
	guard := iprisk.New(&anonymousSettingsStore{settings}, client, nil, false)
	t.Cleanup(guard.Stop)
	cfg := invalidAuthAbuseTestConfig(2)
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*service.APIKey, error) {
		if key != "valid" {
			return nil, service.ErrAPIKeyNotFound
		}
		return &service.APIKey{ID: 1, Status: service.StatusActive, User: &service.User{ID: 1, Status: service.StatusActive, Role: service.RoleUser, Balance: 1}}, nil
	}}
	svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(guard.Middleware())
	router.POST("/test", gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)), func(c *gin.Context) { c.Status(200) })
	request := func(ip, key string) int {
		req := httpRequest(t, "/test", "", key)
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		require.Equal(t, 401, request("9.9.9.9", "unknown"))
	}
	require.Equal(t, 401, request("8.8.8.8", "unknown"))
	require.Equal(t, 429, request("8.8.8.8", "unknown"))
	require.Equal(t, 200, request("8.8.8.8", "valid"))
	require.Equal(t, 401, request("8.8.8.8", "unknown"))
	require.Equal(t, uint64(0), svc.InvalidAuthAbuseHealth().Recorded, "shared policy owns punishment while enabled")
}
