//go:build unit

package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"testing"
)

type observerErrorRepo struct {
	service.OpsRepository
	filter *service.OpsErrorLogFilter
	detail service.OpsErrorLogDetail
	calls  int
}

func (r *observerErrorRepo) ListErrorLogs(_ context.Context, f *service.OpsErrorLogFilter) (*service.OpsErrorLogList, error) {
	r.calls++
	r.filter = f
	return &service.OpsErrorLogList{Errors: []*service.OpsErrorLog{&r.detail.OpsErrorLog}, Total: 1, Page: 1, PageSize: 20}, nil
}
func (r *observerErrorRepo) GetErrorLogByID(context.Context, int64) (*service.OpsErrorLogDetail, error) {
	r.calls++
	return &r.detail, nil
}

func TestObserverUsageErrorsScopeAndVisibility(t *testing.T) {
	for _, enabled := range []string{"true", "false"} {
		t.Run(enabled, func(t *testing.T) {
			owner := int64(42)
			repo := &observerErrorRepo{detail: service.OpsErrorLogDetail{OpsErrorLog: service.OpsErrorLog{ID: 7, UserID: &owner, AccountName: "own-account"}, APIKeyPrefix: "secret-prefix", ErrorBody: "own-error"}}
			settings := service.NewSettingService(&settingHandlerPublicRepoStub{values: map[string]string{service.SettingKeyAllowUserViewErrorRequests: enabled}}, nil)
			ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			h := NewUsageHandler(nil, nil, ops, settings)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				c.Set(string(middleware.ContextKeyUserRole), service.RoleObserver)
			})
			router.GET("/usage/errors", h.ListErrors)
			router.GET("/usage/errors/:id", h.GetErrorDetail)
			w := usageGET(router, "/usage/errors?user_id=999&account_id=8&group_id=9&phase=upstream&status_code=502&start_date=2026-09-01&end_date=2026-09-02")
			if enabled == "false" {
				require.Equal(t, 403, w.Code)
				require.Equal(t, 403, usageGET(router, "/usage/errors/7").Code)
				require.Zero(t, repo.calls)
				return
			}
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, int64(42), *repo.filter.UserID)
			require.Equal(t, int64(8), *repo.filter.AccountID)
			require.Equal(t, int64(9), *repo.filter.GroupID)
			require.Equal(t, "upstream", repo.filter.Phase)
			require.Equal(t, []int{502}, repo.filter.StatusCodes)
			require.NotNil(t, repo.filter.StartTime)
			require.NotContains(t, w.Body.String(), "secret-prefix")
			w = usageGET(router, "/usage/errors/7")
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "own-error")
			require.NotContains(t, w.Body.String(), "secret-prefix")
			owner = 999
			require.Equal(t, 404, usageGET(router, "/usage/errors/7").Code)
		})
	}
}
