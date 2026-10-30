//go:build unit

package middleware

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestImageHistoryAuthNoBalanceStillRequiresValidCredential(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		expired    bool
		userStatus string
		want       int
	}{
		{"exhausted", service.StatusAPIKeyQuotaExhausted, false, service.StatusActive, 200},
		{"disabled", service.StatusDisabled, false, service.StatusActive, 401},
		{"expired", service.StatusActive, true, service.StatusActive, 403},
		{"inactive_user", service.StatusActive, false, service.StatusDisabled, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &service.User{ID: 7, Status: tc.userStatus, Role: service.RoleUser, Balance: 0}
			group := &service.Group{ID: 42, Status: service.StatusActive, Hydrated: true}
			key := &service.APIKey{ID: 100, UserID: 7, User: user, Status: tc.status, Group: group, GroupID: &group.ID, Quota: 1, QuotaUsed: 1}
			if tc.expired {
				past := time.Now().Add(-time.Hour)
				key.ExpiresAt = &past
			}
			repo := &stubApiKeyRepo{getByKey: func(_ context.Context, raw string) (*service.APIKey, error) {
				if raw != "test-history" {
					return nil, service.ErrAPIKeyNotFound
				}
				return key, nil
			}, updateLastUsed: func(context.Context, int64, time.Time) error { t.Fatal("history touched usage"); return nil }}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
			router.GET("/v1/images/history", func(c *gin.Context) { c.Status(200) })
			for _, token := range []string{"test-history", "invalid-history-key"} {
				req := httptest.NewRequest(http.MethodGet, "/v1/images/history", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				want := tc.want
				if token != "test-history" {
					want = 401
				}
				require.Equal(t, want, w.Code, w.Body.String())
			}
		})
	}
}
