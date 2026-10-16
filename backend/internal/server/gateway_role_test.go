package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoleExposesAuthenticatedModelRoutesWithoutAdminOrPayments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway}, Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024}}
	h := &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}, AsyncImage: handler.NewAsyncImageHandler(nil, nil)}
	registerRoutes(router, h, nil, nil, nil, func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }, nil, nil, nil, nil, nil, nil, nil, cfg, nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/health", 200},
		{"GET", "/v1/models", 401},
		{"POST", "/v1/responses", 401},
		{"GET", "/api/v1/admin/settings", 404},
		{"POST", "/api/v1/auth/login", 404},
		{"POST", "/api/v1/payment/orders", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestRegionalSchedulerPreviewBlocksBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway, RegionalSchedulerFile: "preview.json"}, Gateway: config.GatewayConfig{MaxBodySize: 1024, TextMaxBodySize: 1024}}
	h := &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}, AsyncImage: handler.NewAsyncImageHandler(nil, nil)}
	registerRoutes(router, h, nil, nil, nil, func(c *gin.Context) { t.Fatal("preview reached primary authentication") }, nil, nil, nil, nil, nil, nil, nil, cfg, nil)
	for _, path := range []string{"/v1/models", "/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(func() string {
			if path == "/v1/models" {
				return http.MethodGet
			}
			return http.MethodPost
		}(), path, nil))
		require.Equal(t, http.StatusServiceUnavailable, w.Code, path)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRegionalAuthPreviewNeverFallsBackToPrimary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway, RegionalAuthFile: "auth.json"}, Gateway: config.GatewayConfig{MaxBodySize: 1024, TextMaxBodySize: 1024}}
	h := &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}, AsyncImage: handler.NewAsyncImageHandler(nil, nil)}
	registerRoutes(router, h, nil, nil, nil, func(c *gin.Context) { t.Fatal("regional auth reached primary middleware") }, nil, nil, nil, nil, nil, nil, nil, cfg, nil)
	for _, path := range []string{"/v1/models", "/v1/responses", "/v1/messages", "/v1/chat/completions", "/v1beta/models"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer key")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, 503, w.Code, path)
		require.Contains(t, w.Body.String(), "REGIONAL_AUTH_UNAVAILABLE")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, 200, w.Code)
}
