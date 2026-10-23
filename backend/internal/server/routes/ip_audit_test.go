package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestIPAuditRoutesRequireExistingAdministratorAuthentication(t *testing.T) {
	router := gin.New()
	group := router.Group("/api/v1/admin/settings")
	group.Use(gin.HandlerFunc(middleware.NewAdminAuthMiddleware(nil, nil, nil, nil)))
	registerIPAuditRoutes(group, &admin.SettingHandler{})
	for _, method := range []string{"GET", "POST"} {
		path := "/api/v1/admin/settings/ip-audit"
		if method == "POST" {
			path += "/release"
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		require.Equal(t, 401, w.Code)
	}
}
