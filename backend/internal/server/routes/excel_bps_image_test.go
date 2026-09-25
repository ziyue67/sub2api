package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSImageRouteAllowsUnauthenticatedFetchOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	serviceGateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	gateway := handler.NewOpenAIGatewayHandler(serviceGateway, nil, nil, nil, nil, nil, nil, nil, cfg)
	h := &handler.Handlers{OpenAIGateway: gateway, Gateway: &handler.GatewayHandler{}}
	router := gin.New()
	RegisterGatewayRoutes(router, h, func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }, nil, nil, nil, nil, nil, cfg)
	path := "/api/bps-images/" + strings.Repeat("a", 43)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"), "must reach the image handler without API authentication")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	require.Equal(t, http.StatusNotFound, w.Code, "no public upload route may exist")
}
