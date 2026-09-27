//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestObserverPublicGroupRestrictionRejectsExistingAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{config.RunModeStandard, config.RunModeSimple} {
		t.Run(mode, func(t *testing.T) {
			group := &service.Group{ID: 20, Name: "public", Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, Hydrated: true}
			user := &service.User{ID: 7, Role: service.RoleObserver, Status: service.StatusActive, Balance: 100, Concurrency: 1000, RestrictPublicGroups: true, AllowedGroups: []int64{11}}
			key := &service.APIKey{ID: 1, UserID: user.ID, Key: "existing-public-key", Status: service.StatusActive, User: user, Group: group, GroupID: &group.ID}
			repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
			cfg := &config.Config{RunMode: mode}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := newAuthTestRouter(svc, nil, cfg)
			request := httptest.NewRequest(http.MethodGet, "/t", nil)
			request.Header.Set("x-api-key", key.Key)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusForbidden, response.Code)
			require.Contains(t, response.Body.String(), "GROUP_NOT_ALLOWED")
		})
	}
}
