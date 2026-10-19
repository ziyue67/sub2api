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

func TestOpenAISessionExportRequiresAuthAndStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	stepUpCalls := 0
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { stepUpCalls++; c.AbortWithStatus(http.StatusForbidden) })
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}}}
	registerAccountRoutes(group, handlers, stepUp)
	for _, endpoint := range []struct{ method, path string }{{"POST", "/api/v1/admin/accounts/7/export-session"}, {"GET", "/api/v1/admin/accounts/7/export-session/9/download"}} {
		for _, authenticated := range []bool{false, true} {
			request := httptest.NewRequest(endpoint.method, endpoint.path, nil)
			if authenticated {
				request.Header.Set("Authorization", "Bearer offline-test")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if authenticated {
				require.Equal(t, http.StatusForbidden, recorder.Code)
			} else {
				require.Equal(t, http.StatusUnauthorized, recorder.Code)
			}
		}
	}
	require.Equal(t, 2, stepUpCalls)
}
