package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerPresetRepo struct {
	service.QualityRuleTemplateRepository
	presets []*service.QualityRulePreset
}

func (r *handlerPresetRepo) CreatePreset(_ context.Context, p *service.QualityRulePreset) (*service.QualityRulePreset, error) {
	p.ID = int64(len(r.presets) + 1)
	r.presets = append(r.presets, p)
	return p, nil
}

func (r *handlerPresetRepo) ListPresets(context.Context) ([]*service.QualityRulePreset, error) {
	return r.presets, nil
}

func TestQualityPresetHTTPCreateAndReloadWithoutPlanWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &handlerPresetRepo{}
	h := NewScheduledTestHandler(service.ProvideScheduledTestService(nil, nil, repo, nil))
	r := gin.New()
	r.POST("/admin/account-quality-presets", h.CreateQualityPreset)
	r.GET("/admin/account-quality-presets", h.ListQualityPresets)
	body := `{"name":"Saved check","model_id":"test-model","cron_expression":"*/30 * * * *","enabled":false,"max_results":75,
		"account_id":42,"id":99,"pelican_config":{"question_kind":"candy","prompt":"question","parallel_count":1,"reasoning_effort":"high",
		"trigger_source":"upstream_5xx","credentials":{"token":"synthetic"},"quality":{"expected_answer":"42","action":"disable_scheduling","auto_restore":true}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/admin/account-quality-presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	var saved service.QualityRulePreset
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &saved))
	require.Equal(t, int64(1), saved.ID)
	require.False(t, saved.Enabled)
	require.Empty(t, saved.PelicanConfig.TriggerSource)
	require.NotContains(t, w.Body.String(), "credentials")
	require.NotContains(t, w.Body.String(), "account_id")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin/account-quality-presets", nil))
	require.Equal(t, 200, w.Code)
	var reloaded []service.QualityRulePreset
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &reloaded))
	require.Equal(t, []service.QualityRulePreset{saved}, reloaded)
}

func TestQualityPresetCompleteRejectsObserverAndInvalidName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, observer := range []bool{false, true} {
		repo := &handlerPresetRepo{}
		h := NewScheduledTestHandler(service.ProvideScheduledTestService(nil, nil, repo, nil))
		r := gin.New()
		if observer {
			r.Use(func(c *gin.Context) {
				c.Request = c.Request.WithContext(service.WithObserverScope(c.Request.Context(), []int64{}))
				c.Next()
			})
		}
		r.POST("/presets", h.CreateQualityPreset)
		r.PUT("/presets/:id", h.UpdateQualityPreset)
		r.GET("/presets", h.ListQualityPresets)
		req := httptest.NewRequest("POST", "/presets", strings.NewReader(`{"name":"","pelican_config":{"quality":{"action":"observe_only"}}}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if observer {
			require.Equal(t, 403, w.Code)
		} else {
			require.Equal(t, 400, w.Code)
		}
		require.Empty(t, repo.presets)
	}
}
