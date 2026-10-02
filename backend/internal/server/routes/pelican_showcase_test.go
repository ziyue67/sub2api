package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type publicPelicanSettingsRepo struct {
	service.SettingRepository
}

func (publicPelicanSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{service.SettingKeyPelicanShowcaseEnabled: "false"}, nil
}

func (publicPelicanSettingsRepo) GetValue(context.Context, string) (string, error) {
	return `{"enabled":true,"public_ip_rpm":2,"user_rpm":2}`, nil
}

type pelicanRouteKeyRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *pelicanRouteKeyRepo) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	if key != r.key.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.key
	return &clone, nil
}

func newPelicanRouteAuth(runMode string) (middleware.APIKeyAuthMiddleware, *pelicanRouteKeyRepo, *service.APIKeyService) {
	repo := &pelicanRouteKeyRepo{key: &service.APIKey{
		ID: 100, UserID: 7, Key: "pelican-route-test-key", Status: service.StatusActive,
		User: &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 0},
	}}
	cfg := &config.Config{RunMode: runMode}
	keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	return middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg), repo, keys
}

func TestPublicPelicanRoutesRequireAPIKeyAndKeepUserAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.CORS(config.CORSConfig{}))
	settings := service.NewSettingService(publicPelicanSettingsRepo{}, &config.Config{})
	h := &handler.Handlers{PelicanShowcase: handler.NewPelicanShowcaseHandler(service.NewPelicanShowcaseService(nil, settings))}
	v1 := router.Group("/api/v1")
	auth, repo, _ := newPelicanRouteAuth(config.RunModeStandard)
	RegisterPublicPelicanShowcaseRoutes(v1, h, auth, nil)
	RegisterUserRoutes(v1, h, func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }, func(c *gin.Context) { c.Next() }, settings, nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/api/v1/public/pelican-showcase", http.StatusOK},
		{http.MethodHead, "/api/v1/public/pelican-showcase", http.StatusOK},
		{http.MethodGet, "/api/v1/public/pelican-showcase/items/7", http.StatusNotFound},
		{http.MethodPost, "/api/v1/public/pelican-showcase", http.StatusNotFound},
		{http.MethodDelete, "/api/v1/public/pelican-showcase/items/7", http.StatusNotFound},
		{http.MethodGet, "/api/v1/pelican-showcase", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/pelican-showcase/items/7", http.StatusUnauthorized},
	} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Header.Set("Authorization", "Bearer "+repo.key.Key)
		router.ServeHTTP(w, request)
		require.Equal(t, tc.status, w.Code, "%s %s", tc.method, tc.path)
	}
}

func TestPublicPelicanRoutesUseExistingPublicIPLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	settings := service.NewSettingService(publicPelicanSettingsRepo{}, &config.Config{})
	h := &handler.Handlers{PelicanShowcase: handler.NewPelicanShowcaseHandler(service.NewPelicanShowcaseService(nil, settings))}
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	auth, repo, _ := newPelicanRouteAuth(config.RunModeStandard)
	RegisterPublicPelicanShowcaseRoutes(router.Group("/api/v1"), h, auth, middleware.NewPanelRateLimiter(client, settings))
	for i := 0; i < 3; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/public/pelican-showcase", nil)
		request.RemoteAddr = "203.0.113.10:12345"
		request.Header.Set("Authorization", "Bearer "+repo.key.Key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		if i < 2 {
			require.Equal(t, http.StatusOK, w.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		}
	}
}

func TestPublicPelicanRoutesAuthenticateBeforeCachedResults(t *testing.T) {
	for _, mode := range []string{config.RunModeStandard, config.RunModeSimple} {
		t.Run(mode, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			settings := service.NewSettingService(publicPelicanSettingsRepo{}, &config.Config{})
			h := &handler.Handlers{PelicanShowcase: handler.NewPelicanShowcaseHandler(service.NewPelicanShowcaseService(nil, settings))}
			auth, repo, keys := newPelicanRouteAuth(mode)
			RegisterPublicPelicanShowcaseRoutes(router.Group("/api/v1"), h, auth, nil)
			read := func(method, path, key, etag string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(method, path, nil)
				if key != "" {
					request.Header.Set("Authorization", "Bearer "+key)
				}
				request.Header.Set("If-None-Match", etag)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, request)
				return w
			}
			path := "/api/v1/public/pelican-showcase"
			first := read(http.MethodGet, path, repo.key.Key, "")
			require.Equal(t, http.StatusOK, first.Code, first.Body.String())
			require.Equal(t, "private, no-cache, must-revalidate", first.Header().Get("Cache-Control"))
			require.Contains(t, strings.Join(first.Header().Values("Vary"), ","), "Authorization")
			etag := first.Header().Get("ETag")
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, endpoint := range []string{path, path + "/items/7"} {
					for _, key := range []string{"", "invalid-key", "eyJ.example-panel-jwt"} {
						denied := read(method, endpoint, key, etag)
						require.Equal(t, http.StatusUnauthorized, denied.Code)
						require.Equal(t, "no-store", denied.Header().Get("Cache-Control"))
						require.Empty(t, denied.Header().Get("ETag"))
					}
				}
				cached := read(method, path, repo.key.Key, etag)
				require.Equal(t, http.StatusNotModified, cached.Code)
				require.Equal(t, "private, no-cache, must-revalidate", cached.Header().Get("Cache-Control"))
			}
			require.Equal(t, http.StatusBadRequest, read(http.MethodGet, path+"?api_key=secret", "", "").Code)
			for _, status := range []string{service.StatusDisabled, service.StatusAPIKeyExpired} {
				repo.key.Status = status
				keys.InvalidateAuthCacheByKey(context.Background(), repo.key.Key)
				want := http.StatusUnauthorized
				if status == service.StatusAPIKeyExpired {
					want = http.StatusForbidden
				}
				require.Equal(t, want, read(http.MethodGet, path, repo.key.Key, etag).Code)
			}
			repo.key.Status = service.StatusActive
			past := time.Now().Add(-time.Hour)
			repo.key.ExpiresAt = &past
			keys.InvalidateAuthCacheByKey(context.Background(), repo.key.Key)
			require.Equal(t, http.StatusForbidden, read(http.MethodGet, path, repo.key.Key, etag).Code)
		})
	}
}

func TestPublicPelicanRoutesUserLimitSurvivesChangingIPs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	settings := service.NewSettingService(publicPelicanSettingsRepo{}, &config.Config{})
	h := &handler.Handlers{PelicanShowcase: handler.NewPelicanShowcaseHandler(service.NewPelicanShowcaseService(nil, settings))}
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	auth, repo, _ := newPelicanRouteAuth(config.RunModeStandard)
	RegisterPublicPelicanShowcaseRoutes(router.Group("/api/v1"), h, auth, middleware.NewPanelRateLimiter(client, settings))
	for i := 0; i < 3; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/public/pelican-showcase", nil)
		request.RemoteAddr = "203.0.113." + strconv.Itoa(10+i) + ":12345"
		request.Header.Set("Authorization", "Bearer "+repo.key.Key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		if i < 2 {
			require.Equal(t, http.StatusOK, w.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		}
	}
}
