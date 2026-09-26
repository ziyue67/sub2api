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

func TestObserverAdminAuthCapabilitiesAndRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "observer-auth-test", ExpireHour: 1}}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	user := &service.User{ID: 12, Role: service.RoleObserver, Status: service.StatusActive, ObserverGroupIDs: []int64{7}}
	repo := &stubUserRepo{getByID: func(context.Context, int64) (*service.User, error) { clone := *user; return &clone, nil }}
	users := service.NewUserService(repo, nil, nil, nil)
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(auth, users, nil, nil)))
	var grants []int64
	handler := func(c *gin.Context) { grants, _ = service.ObserverGroupIDs(c.Request.Context()); c.Status(204) }
	router.GET("/api/v1/admin/accounts", handler)
	router.GET("/api/v1/admin/settings", handler)
	router.GET("/api/v1/admin/users", handler)
	router.POST("/api/v1/admin/system/update", handler)
	router.POST("/api/v1/admin/system/rollback", handler)
	router.PUT("/api/v1/admin/accounts/codex-harvest-controls", handler)
	call := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	require.Equal(t, 204, call("GET", "/api/v1/admin/accounts"))
	require.Equal(t, []int64{7}, grants)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/settings"}, {"GET", "/api/v1/admin/users"},
		{"POST", "/api/v1/admin/system/update"}, {"POST", "/api/v1/admin/system/rollback"},
		{"PUT", "/api/v1/admin/accounts/codex-harvest-controls"},
	} {
		require.Equal(t, http.StatusForbidden, call(tc.method, tc.path), tc.path)
	}
	// The same JWT sees current grants and role, not its original role claim.
	user.ObserverGroupIDs = []int64{}
	require.Equal(t, 204, call("GET", "/api/v1/admin/accounts"))
	require.Empty(t, grants)
	user.Role = service.RoleUser
	require.Equal(t, 403, call("GET", "/api/v1/admin/accounts"))
	user.Role = service.RoleAdmin
	require.Equal(t, 204, call("POST", "/api/v1/admin/system/update"))
	user.Status = service.StatusDisabled
	require.Equal(t, 401, call("GET", "/api/v1/admin/accounts"))
}

func TestObserverRoutesDenyNewAndGlobalEndpoints(t *testing.T) {
	for _, route := range []string{
		"POST /api/v1/admin/accounts/future-global-feature", "PUT /api/v1/admin/groups/:id",
		"POST /api/v1/admin/proxies", "GET /api/v1/admin/proxies/data", "GET /api/v1/admin/request-captures",
		"POST /api/v1/admin/system/update", "POST /api/v1/admin/system/restart",
	} {
		method, path := route[:4], route[5:]
		if route[:3] == "GET" || route[:3] == "PUT" {
			method, path = route[:3], route[4:]
		}
		require.False(t, ObserverAccountRouteAllowed(method, path), route)
	}
	require.True(t, ObserverAccountRouteAllowed("GET", "/api/v1/admin/accounts/data"))
	require.True(t, ObserverAccountRouteAllowed("POST", "/api/v1/admin/accounts/:id/test"))
	require.True(t, ObserverAccountRouteAllowed("POST", "/api/v1/admin/accounts/bulk-update"))
}
