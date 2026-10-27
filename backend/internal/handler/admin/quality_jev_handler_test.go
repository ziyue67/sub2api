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

type jevPlanRepo struct{ reusePlanRepo }

func (r *jevPlanRepo) Create(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func TestQualityJevAPICompatibilityAndPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{"", "llm", "jev", "unsupported"} {
		for _, observer := range []bool{false, true} {
			for _, method := range []string{"POST", "PUT"} {
				t.Run(fmt.Sprintf("%s/%s/observer_%t", method, kind, observer), func(t *testing.T) {
					judge := fmt.Sprintf(`{"kind":%q,"group_id":9,"model_id":"chat","prompt":"compare","api_key":"synthetic-secret"}`, kind)
					if kind == "jev" {
						judge = `{"kind":"jev","group_id":0,"model_id":"","prompt":"compare","api_key":"synthetic-secret"}`
					}
					body := fmt.Sprintf(`{"account_id":42,"model_id":"tested","cron_expression":"0 * * * *","enabled":false,"pelican_config":{"question_kind":"candy","prompt":"question","reasoning_effort":"high","parallel_count":1,"quality":{"expected_answer":"21","action":"disable_scheduling","judge":%s}}}`, judge)
					repo := &jevPlanRepo{reusePlanRepo{plan: &service.ScheduledTestPlan{ID: 7, AccountID: 42, ModelID: "tested", Enabled: false, MaxResults: 100, CronExpression: "0 * * * *", PelicanConfig: &service.PelicanTestConfig{QuestionKind: "candy", Prompt: "q", ReasoningEffort: "high", ParallelCount: 1, Quality: &service.QualityPolicy{ExpectedAnswer: "21", Action: "disable_scheduling"}}}}}
					h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil))
					router := gin.New()
					if observer {
						router.Use(func(c *gin.Context) {
							c.Request = c.Request.WithContext(service.WithObserverScope(c.Request.Context(), []int64{9}))
							c.Next()
						})
					}
					router.POST("/plans", h.Create)
					router.PUT("/plans/:id", h.Update)
					path := "/plans"
					if method == "PUT" {
						path += "/7"
					}
					req := httptest.NewRequest(method, path, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					want := 200
					if kind == "unsupported" {
						want = 400
					}
					if observer && kind == "jev" {
						want = 403
					}
					require.Equal(t, want, w.Code, w.Body.String())
					require.NotContains(t, w.Body.String(), "synthetic-secret")
					if want == 200 {
						require.Equal(t, kind, repo.saved.PelicanConfig.Quality.Judge.Kind)
					} else {
						require.Nil(t, repo.saved)
					}
				})
			}
		}
	}
}

func TestQualityJevObserverCannotEnableSavedRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(service.WithObserverScope(c.Request.Context(), []int64{9}))
		c.Next()
	})
	repo := &jevPlanRepo{reusePlanRepo{plan: &service.ScheduledTestPlan{ID: 7, PelicanConfig: &service.PelicanTestConfig{Quality: &service.QualityPolicy{Judge: &service.QualityJudgeConfig{Kind: "jev"}}}}}}
	h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil))
	router.PUT("/plans/:id", h.Update)
	req := httptest.NewRequest("PUT", "/plans/7", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
	require.Nil(t, repo.saved)
}
