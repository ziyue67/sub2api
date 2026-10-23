package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/cyberevidence"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *ContentModerationHandler) evidenceService(c *gin.Context) *cyberevidence.Service {
	// Defense in depth: sensitive content also requires an admin role here,
	// irrespective of how an outer router was assembled.
	if !ipAuditAdministrator(c) {
		return nil
	}
	c.Header("Cache-Control", "no-store")
	if h.service == nil || h.service.CyberEvidence() == nil {
		response.Error(c, http.StatusServiceUnavailable, "Evidence storage unavailable")
		return nil
	}
	return h.service.CyberEvidence()
}
func evidenceID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid log ID")
		return 0, false
	}
	return id, true
}
func evidenceError(c *gin.Context, err error) {
	if errors.Is(err, cyberevidence.ErrNotFound) {
		response.NotFound(c, "Evidence record not found")
		return
	}
	response.Error(c, http.StatusServiceUnavailable, "Evidence storage unavailable")
}
func (h *ContentModerationHandler) GetCyberEvidenceConfig(c *gin.Context) {
	s := h.evidenceService(c)
	if s == nil {
		return
	}
	response.Success(c, gin.H{"config": s.Config(), "runtime": s.Stats()})
}
func (h *ContentModerationHandler) UpdateCyberEvidenceConfig(c *gin.Context) {
	s := h.evidenceService(c)
	if s == nil {
		return
	}
	var cfg cyberevidence.Config
	if c.ShouldBindJSON(&cfg) != nil || !cfg.Valid() {
		response.BadRequest(c, "retention_days must be 1..30")
		return
	}
	if err := s.UpdateConfig(c.Request.Context(), cfg); err != nil {
		evidenceError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": cfg.Enabled})
	response.Success(c, cfg)
}
func (h *ContentModerationHandler) GetCyberEvidence(c *gin.Context) {
	s := h.evidenceService(c)
	if s == nil {
		return
	}
	id, ok := evidenceID(c)
	if !ok {
		return
	}
	r, err := s.Get(c.Request.Context(), id)
	if err != nil {
		evidenceError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"event_id": id})
	response.Success(c, r)
}
func (h *ContentModerationHandler) ExportCyberEvidence(c *gin.Context) {
	s := h.evidenceService(c)
	if s == nil {
		return
	}
	id, ok := evidenceID(c)
	if !ok {
		return
	}
	r, err := s.Get(c.Request.Context(), id)
	if err != nil {
		evidenceError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"event_id": id})
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="cyber-evidence-%d.json"`, id))
	c.JSON(http.StatusOK, r)
}
func (h *ContentModerationHandler) DeleteCyberEvidence(c *gin.Context) {
	s := h.evidenceService(c)
	if s == nil {
		return
	}
	id, ok := evidenceID(c)
	if !ok {
		return
	}
	if err := s.Delete(c.Request.Context(), id); err != nil {
		evidenceError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"event_id": id})
	response.Success(c, gin.H{"deleted": true})
}
