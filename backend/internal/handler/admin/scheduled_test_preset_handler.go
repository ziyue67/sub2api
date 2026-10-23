package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ScheduledTestHandler) ListQualityPresets(c *gin.Context) {
	presets, err := h.scheduledTestSvc.ListQualityPresets(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Failed to load quality rule presets")
		return
	}
	c.JSON(http.StatusOK, presets)
}

func (h *ScheduledTestHandler) CreateQualityPreset(c *gin.Context) {
	var req struct {
		Name           string                     `json:"name"`
		ModelID        string                     `json:"model_id"`
		CronExpression string                     `json:"cron_expression"`
		Enabled        *bool                      `json:"enabled"`
		MaxResults     int                        `json:"max_results"`
		PelicanConfig  *service.PelicanTestConfig `json:"pelican_config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	preset := &service.QualityRulePreset{Name: req.Name, ModelID: req.ModelID, CronExpression: req.CronExpression,
		Enabled: true, MaxResults: req.MaxResults, PelicanConfig: req.PelicanConfig}
	if req.Enabled != nil {
		preset.Enabled = *req.Enabled
	}
	created, err := h.scheduledTestSvc.CreateQualityPreset(c.Request.Context(), preset)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, created)
}
