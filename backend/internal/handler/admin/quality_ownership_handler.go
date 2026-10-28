package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *ScheduledTestHandler) QualityOwnershipPreview(c *gin.Context) {
	id, ok := parseQualityTemplateID(c)
	if !ok {
		return
	}
	preview, err := h.scheduledTestSvc.QualityOwnership(c.Request.Context(), id, nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}
func (h *ScheduledTestHandler) QualityOwnershipReconcile(c *gin.Context) {
	id, ok := parseQualityTemplateID(c)
	if !ok {
		return
	}
	var req service.QualityOwnershipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid ownership confirmation")
		return
	}
	preview, err := h.scheduledTestSvc.QualityOwnership(c.Request.Context(), id, &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

// QualityOwnershipRestore uses the same preview/fence as reconciliation, but
// explicit consent restores matching isolation and pauses the rule before deletion.
func (h *ScheduledTestHandler) QualityOwnershipRestore(c *gin.Context) {
	id, ok := parseQualityTemplateID(c)
	if !ok {
		return
	}
	var req service.QualityOwnershipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid ownership confirmation")
		return
	}
	req.Restore = true
	preview, err := h.scheduledTestSvc.QualityOwnership(c.Request.Context(), id, &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

// QualityOwnershipRemoveModel restores only a reviewed model marker and removes
// its configured target atomically. The normal admin middleware audits this POST.
func (h *ScheduledTestHandler) QualityOwnershipRemoveModel(c *gin.Context) {
	id, ok := parseQualityTemplateID(c)
	if !ok {
		return
	}
	var req service.QualityOwnershipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid model removal confirmation")
		return
	}
	req.RemoveModel = true
	middleware.SetAuditExtra(c, map[string]any{"source": "quality_model_removal", "reason_code": req.ReasonCode, "confirm": req.Confirm, "requested_count": 1})
	preview, err := h.scheduledTestSvc.QualityOwnership(c.Request.Context(), id, &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}
