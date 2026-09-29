package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type qualityRuleTemplateRequest struct {
	PelicanConfig  *service.PelicanTestConfig        `json:"pelican_config"`
	AccountFilter  *service.QualityRuleAccountFilter `json:"account_filter"`
	Enabled        *bool                             `json:"enabled"`
	ModelID        string                            `json:"model_id"`
	CronExpression string                            `json:"cron_expression"`
	MaxResults     int                               `json:"max_results"`
}

func parseQualityTemplateID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid template id")
		return 0, false
	}
	return id, true
}

// ListQualityTemplates GET /admin/account-quality-templates
func (h *ScheduledTestHandler) ListQualityTemplates(c *gin.Context) {
	templates, err := h.scheduledTestSvc.ListQualityTemplates(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Failed to load quality rule templates")
		return
	}
	if templates == nil {
		templates = []*service.QualityRuleTemplate{}
	}
	c.JSON(http.StatusOK, templates)
}

// CreateQualityTemplate POST /admin/account-quality-templates
func (h *ScheduledTestHandler) CreateQualityTemplate(c *gin.Context) {
	var req qualityRuleTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.CronExpression == "" {
		response.BadRequest(c, "cron_expression is required")
		return
	}
	template := &service.QualityRuleTemplate{
		PelicanConfig:  req.PelicanConfig,
		ModelID:        req.ModelID,
		CronExpression: req.CronExpression,
		MaxResults:     req.MaxResults,
		Enabled:        true,
	}
	if req.AccountFilter != nil {
		template.AccountFilter = *req.AccountFilter
	}
	if req.Enabled != nil {
		template.Enabled = *req.Enabled
	}
	created, count, err := h.scheduledTestSvc.CreateQualityTemplate(c.Request.Context(), template)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"template": created, "created": count})
}

// UpdateQualityTemplate PUT /admin/account-quality-templates/:id
func (h *ScheduledTestHandler) UpdateQualityTemplate(c *gin.Context) {
	id, ok := parseQualityTemplateID(c)
	if !ok {
		return
	}
	existing, err := h.scheduledTestSvc.GetQualityTemplate(c.Request.Context(), id)
	if errors.Is(err, service.ErrQualityTemplateNotFound) {
		response.NotFound(c, "template not found")
		return
	}
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	var req qualityRuleTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.PelicanConfig != nil {
		existing.PelicanConfig = req.PelicanConfig
	}
	if req.AccountFilter != nil {
		existing.AccountFilter = *req.AccountFilter
	}
	if req.ModelID != "" {
		existing.ModelID = req.ModelID
	}
	if req.CronExpression != "" {
		existing.CronExpression = req.CronExpression
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.MaxResults > 0 {
		existing.MaxResults = req.MaxResults
	}
	result, err := h.scheduledTestSvc.UpdateQualityTemplate(c.Request.Context(), existing)
	if errors.Is(err, service.ErrQualityTemplateNotFound) {
		response.NotFound(c, "template not found")
		return
	}
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

// DeleteQualityTemplate DELETE /admin/account-quality-templates/:id?delete_plans=true
func (h *ScheduledTestHandler) DeleteQualityTemplate(c *gin.Context) {
	id, ok := parseQualityTemplateID(c)
	if !ok {
		return
	}
	deletePlans := c.Query("delete_plans") == "true"
	deleted, err := h.scheduledTestSvc.DeleteQualityTemplate(c.Request.Context(), id, deletePlans)
	if errors.Is(err, service.ErrQualityTemplateNotFound) {
		response.NotFound(c, "template not found")
		return
	}
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}
