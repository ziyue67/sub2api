package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type archiveAuthUserRepository struct {
	service.UserRepository
	user *service.User
}

func (r *archiveAuthUserRepository) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

func (r *archiveAuthUserRepository) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func TestImageArchiveRoutesRequireAdminForFilesAndMetadata(t *testing.T) {
	user := &service.User{ID: 12, Email: "customer@example.test", Role: service.RoleUser, Status: service.StatusActive}
	auth := service.NewAuthService(nil, nil, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "archive-test-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	users := service.NewUserService(&archiveAuthUserRepository{user: user}, nil, nil, nil)
	adminAuth := middleware.NewAdminAuthMiddleware(auth, users, nil, nil)
	router := gin.New()
	group := router.Group("/api/v1/admin")
	group.Use(gin.HandlerFunc(adminAuth))
	// Nil service deliberately proves unauthenticated/customer traffic never reaches any archive handler.
	registerImageArchiveRoutes(group, &handler.Handlers{Admin: &handler.AdminHandlers{ImageArchive: admin.NewImageArchiveHandler(nil)}})
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/image-archive"}, {"GET", "/api/v1/admin/image-archive/stats"}, {"PUT", "/api/v1/admin/image-archive/config"},
		{"DELETE", "/api/v1/admin/image-archive/00000000-0000-4000-8000-000000000000"}, {"DELETE", "/api/v1/admin/image-archive/00000000-0000-4000-8000-000000000000/files/1"},
		{"GET", "/api/v1/admin/image-archive/00000000-0000-4000-8000-000000000000"}, {"GET", "/api/v1/admin/image-archive/00000000-0000-4000-8000-000000000000/files/1"},
	} {
		for _, header := range []string{"", "Bearer " + token} {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Header.Set("Authorization", header)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if header == "" {
				require.Equal(t, http.StatusUnauthorized, rec.Code, route.path)
			} else {
				require.Equal(t, http.StatusForbidden, rec.Code, route.path)
			}
		}
	}
}
