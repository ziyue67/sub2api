//go:build unit

package repository_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type sessionSecurityUsers struct {
	service.UserRepository
	user service.User
}

func (r *sessionSecurityUsers) GetByID(context.Context, int64) (*service.User, error) {
	u := r.user
	return &u, nil
}
func (r *sessionSecurityUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func TestTKSecuritySessionRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, action := range []string{"logout", "all", "refresh-reuse", "cache-outage"} {
		t.Run(action, func(t *testing.T) {
			server := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = rdb.Close() })
			cache := repository.NewRefreshTokenCache(rdb)
			now := time.Now()
			repo := &sessionSecurityUsers{user: service.User{ID: 29, Email: "fixture@example.com", Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 7, TokenVersionResolved: true, LastActiveAt: &now}}
			cfg := &config.Config{JWT: config.JWTConfig{Secret: "fixture-secret-with-enough-random-material", ExpireHour: 1, RefreshTokenExpireDays: 1}}
			auth := service.NewAuthService(nil, repo, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
			users := service.NewUserService(repo, nil, nil, nil)
			router := gin.New()
			router.GET("/profile", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, users, nil, nil)), func(c *gin.Context) { c.Status(200) })
			router.GET("/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, users, nil, nil)), func(c *gin.Context) { c.Status(200) })
			status := func(token string) int {
				req := httptest.NewRequest(http.MethodGet, "/profile", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				adminRequest := req.Clone(context.Background())
				adminRequest.URL.Path = "/admin"
				adminRecorder := httptest.NewRecorder()
				router.ServeHTTP(adminRecorder, adminRequest)
				require.Equal(t, rec.Code, adminRecorder.Code, "admin and user JWT consumers must agree on revocation")
				return rec.Code
			}
			ctx := context.Background()
			first, err := auth.GenerateTokenPair(ctx, &repo.user, "")
			require.NoError(t, err)
			other, err := auth.GenerateTokenPair(ctx, &repo.user, "")
			require.NoError(t, err)
			require.Equal(t, 200, status(first.AccessToken))
			legacyClaims, err := auth.ValidateToken(first.AccessToken)
			require.NoError(t, err)
			legacyClaims.SessionID = ""
			legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, legacyClaims).SignedString([]byte(cfg.JWT.Secret))
			require.NoError(t, err)
			if action == "cache-outage" {
				server.Close()
				require.Equal(t, 503, status(first.AccessToken))
				require.Error(t, auth.RevokeAllUserTokens(ctx, repo.user.ID))
				return
			}
			switch action {
			case "logout":
				require.NoError(t, auth.RevokeRefreshToken(ctx, first.RefreshToken))
			case "all":
				require.NoError(t, auth.RevokeAllUserTokens(ctx, repo.user.ID))
			case "refresh-reuse":
				next, err := auth.RefreshTokenPair(ctx, first.RefreshToken)
				require.NoError(t, err)
				_, err = auth.RefreshTokenPair(ctx, first.RefreshToken)
				require.Error(t, err)
				_, err = auth.RefreshTokenPair(ctx, next.RefreshToken)
				require.Error(t, err, "reuse must revoke the successor, not only reject the old token")
				require.Equal(t, 401, status(next.AccessToken))
			}
			require.Equal(t, 401, status(first.AccessToken), "revoked access token must be rejected immediately")
			if action == "all" {
				require.Equal(t, 401, status(other.AccessToken))
				require.Equal(t, 401, status(legacy), "old access-only JWTs must also honor user-wide revocation")
			} else {
				require.Equal(t, 200, status(other.AccessToken), "another device must remain logged in")
			}
			fresh, err := auth.GenerateTokenPair(ctx, &repo.user, "")
			require.NoError(t, err)
			require.Equal(t, 200, status(fresh.AccessToken), "a fresh login in the same second must work")
		})
	}
}

func TestTKSecurityRefreshConsumptionIsAtomic(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := repository.NewRefreshTokenCache(rdb)
	store := cache.(service.SessionSecurityStore)
	data := &service.RefreshTokenData{UserID: 29, FamilyID: "fixture-family", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, cache.StoreRefreshToken(context.Background(), "fixture-hash", data, time.Hour))
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.ConsumeRefreshToken(context.Background(), "fixture-hash")
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
	rotated, err := store.GetRotatedRefreshToken(context.Background(), "fixture-hash")
	require.NoError(t, err)
	require.Equal(t, data.FamilyID, rotated.FamilyID)
}
