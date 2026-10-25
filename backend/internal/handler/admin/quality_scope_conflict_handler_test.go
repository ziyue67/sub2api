package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qualityConflictRepo struct{ reusePlanRepo }

func (r *qualityConflictRepo) Update(context.Context, *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	return nil, infraerrors.Conflict("QUALITY_ISOLATION_SCOPE_CONFLICT", "restore scope before changing targets")
}
func (r *qualityConflictRepo) Delete(context.Context, int64) error {
	return infraerrors.Conflict("QUALITY_ISOLATION_SCOPE_CONFLICT", "restore scope before deleting")
}
func TestQualityScopeConflictsUse409ForUpdateAndDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &qualityConflictRepo{reusePlanRepo{plan: &service.ScheduledTestPlan{ID: 7, AccountID: 42, ModelID: "test", CronExpression: "0 * * * *", Enabled: false}}}
	h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil))
	router := gin.New()
	router.PUT("/plans/:id", h.Update)
	router.DELETE("/plans/:id", h.Delete)
	for _, method := range []string{"PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/plans/7", strings.NewReader(`{"enabled":false}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, 409, w.Code)
			require.Contains(t, w.Body.String(), "QUALITY_ISOLATION_SCOPE_CONFLICT")
		})
	}
}
