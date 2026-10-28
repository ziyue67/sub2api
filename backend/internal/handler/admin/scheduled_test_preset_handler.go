package admin

import (
	"errors"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ScheduledTestHandler) ListQualityPresets(c *gin.Context) {
	presets, err := h.scheduledTestSvc.ListQualityPresets(c.Request.Context())
	if err != nil {
		qualityPresetError(c, err)
		return
	}
	c.JSON(http.StatusOK, presets)
}

type qualityPresetRequest struct {
	Name           string                     `json:"name"`
	ModelID        string                     `json:"model_id"`
	CronExpression string                     `json:"cron_expression"`
	Enabled        *bool                      `json:"enabled"`
	MaxResults     int                        `json:"max_results"`
	PelicanConfig  *service.PelicanTestConfig `json:"pelican_config"`
}

func (h *ScheduledTestHandler) CreateQualityPreset(c *gin.Context) {
	var req qualityPresetRequest
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
		qualityPresetError(c, err)
		return
	}
	c.JSON(http.StatusOK, created)
}

func (h *ScheduledTestHandler) UpdateQualityPreset(c *gin.Context) {
	id, ok := parseQualityTemplateID(c)
	if !ok {
		return
	}
	var req qualityPresetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	preset := &service.QualityRulePreset{Name: req.Name, ModelID: req.ModelID, CronExpression: req.CronExpression, Enabled: true, MaxResults: req.MaxResults, PelicanConfig: req.PelicanConfig}
	if req.Enabled != nil {
		preset.Enabled = *req.Enabled
	}
	saved, err := h.scheduledTestSvc.UpdateQualityPreset(c.Request.Context(), id, preset)
	if err != nil {
		qualityPresetError(c, err)
		return
	}
	c.JSON(http.StatusOK, saved)
}

func qualityPresetError(c *gin.Context, err error) {
	var appErr *infraerrors.ApplicationError
	if errors.As(err, &appErr) {
		response.ErrorFrom(c, err)
	} else {
		response.BadRequest(c, err.Error())
	}
}
