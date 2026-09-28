package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type groupTestHandlerRepo struct {
	service.PelicanGroupTestRepository
	plans   map[int64]*service.PelicanGroupTestPlan
	claimOK bool
	results []*service.PelicanGroupTestResult
}

func (r *groupTestHandlerRepo) ListPlans(context.Context) ([]*service.PelicanGroupTestPlan, error) {
	out := []*service.PelicanGroupTestPlan{}
	for _, plan := range r.plans {
		out = append(out, plan)
	}
	return out, nil
}
func (r *groupTestHandlerRepo) GetPlan(_ context.Context, id int64) (*service.PelicanGroupTestPlan, error) {
	return r.plans[id], nil
}
func (r *groupTestHandlerRepo) CreatePlan(_ context.Context, plan *service.PelicanGroupTestPlan) (*service.PelicanGroupTestPlan, error) {
	saved := *plan
	saved.ID = 9
	r.plans[9] = &saved
	return &saved, nil
}
func (r *groupTestHandlerRepo) DeletePlan(_ context.Context, id int64) (bool, error) {
	_, ok := r.plans[id]
	delete(r.plans, id)
	return ok, nil
}
func (r *groupTestHandlerRepo) Claim(context.Context, *service.PelicanGroupTestPlan, time.Time, time.Time, *time.Time) (bool, error) {
	return r.claimOK, nil
}
func (r *groupTestHandlerRepo) ListResults(_ context.Context, _, _ int64, limit int) ([]*service.PelicanGroupTestResult, error) {
	if len(r.results) > limit {
		return r.results[:limit], nil
	}
	return r.results, nil
}
func (r *groupTestHandlerRepo) GetResult(context.Context, int64) (*service.PelicanGroupTestResult, error) {
	return nil, nil
}

type groupTestHandlerGroups struct {
	service.GroupRepository
}

func (groupTestHandlerGroups) GetByID(_ context.Context, id int64) (*service.Group, error) {
	if id == 4 {
		return &service.Group{ID: 4, Status: service.StatusActive}, nil
	}
	return nil, service.ErrGroupNotFound
}

func serveGroupTest(h gin.HandlerFunc, method, route, target, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, route, h)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestPelicanGroupTestHandlerStatusCodes(t *testing.T) {
	plan := &service.PelicanGroupTestPlan{ID: 7, GroupID: 4, ModelID: "gpt-6-astra", CronExpression: "*/30 * * * *",
		PelicanConfig: &service.PelicanTestConfig{QuestionKind: "pelican", Prompt: "draw", ReasoningEffort: "high", ParallelCount: 1}}
	repo := &groupTestHandlerRepo{plans: map[int64]*service.PelicanGroupTestPlan{7: plan}}
	for id := int64(60); id > 0; id-- {
		repo.results = append(repo.results, &service.PelicanGroupTestResult{ID: id, Status: "success"})
	}
	h := NewPelicanGroupTestHandler(service.NewPelicanGroupTestService(repo, groupTestHandlerGroups{}, nil, nil, nil, nil, nil))

	w := serveGroupTest(h.ListPlans, http.MethodGet, "/plans", "/plans", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"model_id":"gpt-6-astra"`)

	require.Equal(t, http.StatusBadRequest, serveGroupTest(h.CreatePlan, http.MethodPost, "/plans", "/plans", `{"group_id":`).Code)
	w = serveGroupTest(h.CreatePlan, http.MethodPost, "/plans", "/plans", `{"group_id":99,"model_id":"m","prompt":"p","reasoning_effort":"high"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "INVALID_PELICAN_GROUP_TEST")
	w = serveGroupTest(h.CreatePlan, http.MethodPost, "/plans", "/plans", `{"group_id":4,"model_id":"m","prompt":"p","reasoning_effort":"high","enabled":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"cron_expression":"*/30 * * * *"`)

	require.Equal(t, http.StatusBadRequest, serveGroupTest(h.UpdatePlan, http.MethodPut, "/plans/:id", "/plans/abc", `{}`).Code)
	require.Equal(t, http.StatusNotFound, serveGroupTest(h.UpdatePlan, http.MethodPut, "/plans/:id", "/plans/404",
		`{"model_id":"m","prompt":"p","reasoning_effort":"high"}`).Code)
	require.Equal(t, http.StatusNotFound, serveGroupTest(h.DeletePlan, http.MethodDelete, "/plans/:id", "/plans/404", "").Code)
	require.Equal(t, http.StatusOK, serveGroupTest(h.DeletePlan, http.MethodDelete, "/plans/:id", "/plans/9", "").Code)

	require.Equal(t, http.StatusNotFound, serveGroupTest(h.RunPlan, http.MethodPost, "/plans/:id/run", "/plans/404/run", "").Code)
	w = serveGroupTest(h.RunPlan, http.MethodPost, "/plans/:id/run", "/plans/7/run", "")
	require.Equal(t, http.StatusConflict, w.Code, "a run in progress is reported, not queued twice")
	require.Contains(t, w.Body.String(), "PELICAN_GROUP_TEST_PLAN_RUNNING")

	w = serveGroupTest(h.ListResults, http.MethodGet, "/results", "/results?limit=20", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"next_cursor":41`)
	require.Equal(t, http.StatusBadRequest, serveGroupTest(h.ListResults, http.MethodGet, "/results", "/results?before_id=-1", "").Code)
	require.Equal(t, http.StatusNotFound, serveGroupTest(h.GetResult, http.MethodGet, "/results/:id", "/results/5", "").Code)
}
