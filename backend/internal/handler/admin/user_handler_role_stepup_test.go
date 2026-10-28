package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 角色提升为管理员的 step-up 门控条件测试。
// 测试环境不注入认证上下文，因此门控一旦触发会以 401 中止；
// 借此区分「触发了 step-up 校验」与「直接放行到业务层（200）」。
func setupRoleStepUpRouter(t *testing.T) (*gin.Engine, *stubAdminService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	// 追加一个已是管理员的目标用户，验证「目标已是 admin 不触发门控」。
	adminSvc.users = append(adminSvc.users, service.User{
		ID:     2,
		Email:  "admin@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})

	h := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/users", h.Create)
	router.PUT("/api/v1/admin/users/:id", h.Update)
	return router, adminSvc
}

func doJSON(t *testing.T, router *gin.Engine, method, path string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func TestUpdateUserPromoteToAdminRequiresStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{"role": "admin"})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUpdateUserKeepAdminRoleSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/2", map[string]any{"role": "admin"})
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUpdateUserRegularRoleSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{"role": "user", "email": "u@example.com"})
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUserQuickEnableReasonLimit(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)
	for _, length := range []int{1, 500, 501} {
		rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{
			"status": "active", "reason": strings.Repeat("审", length),
		})
		if length > 500 {
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		} else {
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}
	}
}

type quickEnableAdminService struct {
	service.AdminService
	input *service.UpdateUserInput
}

func (s *quickEnableAdminService) UpdateUser(_ context.Context, id int64, input *service.UpdateUserInput) (*service.User, error) {
	s.input = input
	return &service.User{ID: id, Role: service.RoleUser, Status: input.Status}, nil
}

func TestUserQuickEnableUsesExistingStatusUpdate(t *testing.T) {
	svc := &quickEnableAdminService{}
	h := NewUserHandler(svc, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 17})
		c.Next()
	})
	router.PUT("/api/v1/admin/users/:id", h.Update)
	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/306", map[string]any{
		"status": "active", "reason": "reviewed disable source",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, &service.UpdateUserInput{Status: service.StatusActive, ActorAdminID: 17}, svc.input)
	require.Contains(t, rec.Body.String(), `"status":"active"`)
	svc.input = nil
	rec = doJSON(t, router, http.MethodPut, "/api/v1/admin/users/306", map[string]any{"status": "unknown"})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, svc.input, "invalid status must not reach the user management service")
}

func TestCreateAdminUserRequiresStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-admin@example.com", "password": "pass123", "role": "admin",
	})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateRegularUserSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-user@example.com", "password": "pass123", "role": "user",
	})
	require.Equal(t, http.StatusOK, rec.Code)
}
