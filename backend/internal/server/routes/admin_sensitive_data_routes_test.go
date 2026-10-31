package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTKSecuritySensitiveDataRoutesRequireStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	group := router.Group("/api/v1/admin")
	stepUpCalls := 0
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) {
		stepUpCalls++
		c.AbortWithStatus(http.StatusForbidden)
	})
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		Account: &adminhandler.AccountHandler{},
		Setting: &adminhandler.SettingHandler{},
		Proxy:   &adminhandler.ProxyHandler{},
		Redeem:  &adminhandler.RedeemHandler{},
	}}

	registerAccountRoutes(group, handlers, stepUp)
	registerProxyRoutes(group, handlers, stepUp)
	registerRedeemCodeRoutes(group, handlers, stepUp)

	endpoints := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/admin/accounts/data"},
		{method: http.MethodPost, path: "/api/v1/admin/proxies/data"},
		{method: http.MethodGet, path: "/api/v1/admin/redeem-codes/export"},
	}
	for _, endpoint := range endpoints {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(endpoint.method, endpoint.path, nil)
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusForbidden, recorder.Code, "%s %s must stop at step-up", endpoint.method, endpoint.path)
	}
	require.Equal(t, len(endpoints), stepUpCalls)
}
