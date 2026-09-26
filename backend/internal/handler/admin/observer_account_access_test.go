package admin

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type observerAccountService struct {
	service.AdminService
	accounts map[int64]*service.Account
}

func (s *observerAccountService) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	result := []*service.Account{}
	for _, id := range ids {
		if a := s.accounts[id]; a != nil {
			result = append(result, a)
		}
	}
	return result, nil
}

func TestObserverGuardRejectsWholeUnauthorizedBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &observerAccountService{accounts: map[int64]*service.Account{
		1: {ID: 1, GroupIDs: []int64{10}},
		2: {ID: 2, GroupIDs: []int64{20}},
		3: {ID: 3, GroupIDs: []int64{10, 20}},
		4: {ID: 4},
	}}
	for _, tc := range []struct {
		name, method, route, path, body string
		grants                          []int64
		status                          int
	}{
		{"single", "GET", "/accounts/:id", "/accounts/1", "", []int64{10}, 204},
		{"shared", "DELETE", "/accounts/:id", "/accounts/3", "", []int64{10}, 204},
		{"hidden", "GET", "/accounts/:id", "/accounts/2", "", []int64{10}, 403},
		{"ungrouped", "DELETE", "/accounts/:id", "/accounts/4", "", []int64{10}, 403},
		{"no_grants", "GET", "/accounts/:id", "/accounts/1", "", nil, 403},
		{"mixed_delete", "POST", "/accounts/batch-delete", "/accounts/batch-delete", `{"account_ids":[1,2]}`, []int64{10}, 403},
		{"mixed_read", "POST", "/accounts/usage/batch", "/accounts/usage/batch", `{"account_ids":[1,2]}`, []int64{10}, 403},
		{"allowed_batch", "POST", "/accounts/batch-refresh", "/accounts/batch-refresh", `{"account_ids":[1,3]}`, []int64{10}, 204},
		{"export", "GET", "/accounts/data", "/accounts/data?ids=1,3", "", []int64{10}, 204},
		{"mixed_export", "GET", "/accounts/data", "/accounts/data?ids=1&ids=2", "", []int64{10}, 403},
		{"escape_group", "PUT", "/accounts/:id", "/accounts/1", `{"group_ids":[20]}`, []int64{10}, 403},
		{"remove_all", "PUT", "/accounts/:id", "/accounts/1", `{"group_ids":[]}`, []int64{10}, 403},
		{"case_insensitive", "PUT", "/accounts/:id", "/accounts/1", `{"GROUP_IDS":[20]}`, []int64{10}, 403},
		{"group_models", "PUT", "/accounts/:id", "/accounts/1", `{"group_allowed_models":{"20":["secret"]}}`, []int64{10}, 403},
		{"mixed_create", "POST", "/accounts/batch", "/accounts/batch", `{"accounts":[{"group_ids":[10]},{"group_ids":[20]}]}`, []int64{10}, 403},
		{"credential_body_untouched", "PUT", "/accounts/:id", "/accounts/1", `{"credentials":{"api_key":"test-secret","account_ids":[2]},"group_ids":[10]}`, []int64{10}, 204},
		{"system_update", "POST", "/system/update", "/system/update", "{}", []int64{10}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &AccountHandler{adminService: svc}
			r := gin.New()
			hit := false
			r.Use(func(c *gin.Context) {
				c.Request = c.Request.WithContext(service.WithObserverScope(c.Request.Context(), tc.grants))
			})
			r.Use(h.AuthorizeObserver)
			r.Handle(tc.method, "/api/v1/admin"+tc.route, func(c *gin.Context) {
				hit = true
				if tc.body != "" {
					body, err := io.ReadAll(c.Request.Body)
					require.NoError(t, err)
					require.Equal(t, tc.body, string(body))
				}
				c.Status(204)
			})
			req := httptest.NewRequest(tc.method, "/api/v1/admin"+tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, tc.status == 204, hit, "no business handler executes on forbidden references")
		})
	}
}

func TestObserverRoleAndGrantChangesRequireStepUp(t *testing.T) {
	r, svc := setupRoleStepUpRouter(t)
	svc.users = append(svc.users, service.User{ID: 3, Role: service.RoleObserver, ObserverGroupIDs: []int64{10}})
	require.Equal(t, 401, doJSON(t, r, "POST", "/api/v1/admin/users", map[string]any{"email": "observer@example.test", "password": "pass123", "role": "observer"}).Code)
	require.Equal(t, 401, doJSON(t, r, "PUT", "/api/v1/admin/users/1", map[string]any{"role": "observer"}).Code)
	require.Equal(t, 401, doJSON(t, r, "PUT", "/api/v1/admin/users/3", map[string]any{"observer_group_ids": []int64{10, 20}}).Code)
	require.Equal(t, 200, doJSON(t, r, "PUT", "/api/v1/admin/users/3", map[string]any{"role": "observer", "observer_group_ids": []int64{10}}).Code)
	require.Equal(t, 200, doJSON(t, r, "PUT", "/api/v1/admin/users/3", map[string]any{"observer_group_ids": []int64{}}).Code)
}
