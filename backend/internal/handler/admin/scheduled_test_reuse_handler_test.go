package admin

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type reusePlanRepo struct {
	service.ScheduledTestPlanRepository
	plan  *service.ScheduledTestPlan
	saved *service.ScheduledTestPlan
}

func (r *reusePlanRepo) GetByID(context.Context, int64) (*service.ScheduledTestPlan, error) {
	return r.plan, nil
}

func (r *reusePlanRepo) Update(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func TestQualityReuseUpdateKeepsTargetIdentityAndForwardsOwnershipOption(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprint(preserve), func(t *testing.T) {
			repo := &reusePlanRepo{plan: &service.ScheduledTestPlan{ID: 7, AccountID: 42, ModelID: "test-model", Enabled: false, MaxResults: 100,
				CronExpression: "*/30 * * * *", PelicanConfig: &service.PelicanTestConfig{QuestionKind: "candy", Prompt: "Question", ReasoningEffort: "high", ParallelCount: 1,
					Quality: &service.QualityPolicy{Action: "disable_scheduling", ExpectedAnswer: "42"}}}}
			h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil))
			r := gin.New()
			r.PUT("/admin/scheduled-test-plans/:id", h.Update)
			body := fmt.Sprintf(`{"id":99,"account_id":90,"preserve_quality_state":%t,"cron_expression":"0 * * * *"}`, preserve)
			req := httptest.NewRequest("PUT", "/admin/scheduled-test-plans/7", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.NotNil(t, repo.saved)
			require.Equal(t, int64(7), repo.saved.ID)
			require.Equal(t, int64(42), repo.saved.AccountID)
			require.Equal(t, preserve, repo.saved.PreserveQualityState)
			require.NotContains(t, w.Body.String(), "preserve_quality_state", "request option is not persisted rule state")
		})
	}
}
