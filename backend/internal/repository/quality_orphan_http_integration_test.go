//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQualityOrphanHTTPPreviewConfirmReadbackAndSave(t *testing.T) {
	f := repository.NewQualityOrphanFixture(t)
	h := admin.NewScheduledTestHandler(service.NewScheduledTestService(f.Plans, repository.NewScheduledTestResultRepository(repository.QualityLiveDB())))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/plans/:id/ownership", h.QualityOwnershipPreview)
	r.POST("/plans/:id/remove-model", h.QualityOwnershipRemoveModel)
	r.PUT("/plans/:id", h.Update)
	path := fmt.Sprintf("/plans/%d", f.Plan.ID)
	p := qualityLivePreview(t, r, f.Plan.ID)
	require.Len(t, p.ModelRemovals, 3)
	for _, body := range []map[string]any{
		{"model": "sol", "reason_code": "orphan_marker_recovery", "fence": p.Fence},
		{"model": "sol", "confirm": true, "fence": p.Fence},
		{"model": "sol", "reason_code": "arbitrary", "confirm": true, "fence": p.Fence},
	} {
		require.Equal(t, 400, qualityLiveRequest(r, "POST", path+"/remove-model", body).Code)
	}
	w := qualityLiveRequest(r, "POST", path+"/remove-model", map[string]any{"model": "sol", "reason_code": "orphan_marker_recovery", "confirm": true, "fence": p.Fence})
	require.Equal(t, 200, w.Code, w.Body.String())
	fresh := qualityLivePreview(t, r, f.Plan.ID)
	require.NotContains(t, fresh.Models, "sol")
	require.Contains(t, fresh.Models, "astra")
	require.NotContains(t, f.Extra()[service.QualityModelIsolationKey], "sol")
	require.Contains(t, f.Extra()[service.QualityModelIsolationKey], "astra")
	plan, err := f.Plans.GetByID(context.Background(), f.Plan.ID)
	require.NoError(t, err)
	w = qualityLiveRequest(r, "PUT", path, map[string]any{"model_id": plan.ModelID, "cron_expression": "0 * * * *", "enabled": plan.Enabled, "max_results": plan.MaxResults, "pelican_config": plan.PelicanConfig})
	require.Equal(t, 200, w.Code, w.Body.String())
	// Reusing consent after the rule update cannot mutate any remaining model.
	require.Equal(t, 409, qualityLiveRequest(r, "POST", path+"/remove-model", map[string]any{"model": "astra", "reason_code": "retire_model", "confirm": true, "fence": p.Fence}).Code)
	require.Contains(t, f.Extra()[service.QualityModelIsolationKey], "astra")
}
