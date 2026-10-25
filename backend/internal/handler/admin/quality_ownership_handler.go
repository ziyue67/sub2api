package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
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
