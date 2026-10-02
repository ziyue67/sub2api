//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthPelicanReadsSkipBillingAndWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 42, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeSubscription}
	key := &service.APIKey{
		ID: 100, UserID: 7, Key: "pelican-read-auth-only", Status: service.StatusAPIKeyQuotaExhausted,
		User:    &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 0},
		GroupID: &group.ID, Group: group, Quota: 1, QuotaUsed: 1,
	}
	touchCalls, subscriptionCalls := 0, 0
	repo := &stubApiKeyRepo{
		getByKey:       func(context.Context, string) (*service.APIKey, error) { clone := *key; return &clone, nil },
		updateLastUsed: func(context.Context, int64, time.Time) error { touchCalls++; return nil },
	}
	subRepo := &stubUserSubscriptionRepo{getActive: func(context.Context, int64, int64) (*service.UserSubscription, error) {
		subscriptionCalls++
		return nil, service.ErrSubscriptionNotFound
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	subscriptions := service.NewSubscriptionService(nil, subRepo, nil, nil, cfg)
	t.Cleanup(subscriptions.Stop)
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(keys, subscriptions, cfg)))
	root := "/api/v1/public/pelican-showcase"
	router.Any(root, func(c *gin.Context) { c.Status(http.StatusOK) })
	router.Any(root+"/items/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, path := range []string{root, root + "/items/7"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, header := range []string{"Authorization", "X-API-Key", "X-Goog-API-Key"} {
				request := httptest.NewRequest(method, path, nil)
				value := key.Key
				if header == "Authorization" {
					value = "Bearer " + value
				}
				request.Header.Set(header, value)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, request)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			}
		}
	}
	require.Zero(t, touchCalls)
	require.Zero(t, subscriptionCalls)
	// The billing exception must not match mutation methods or arbitrary subpaths.
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, root},
		{http.MethodGet, root + "/items/not-an-id"},
	} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Header.Set("Authorization", "Bearer "+key.Key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		require.Equal(t, http.StatusForbidden, w.Code, "non-read requests still require an active subscription")
	}
}
