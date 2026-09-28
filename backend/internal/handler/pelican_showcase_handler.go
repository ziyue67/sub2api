package handler

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// PelicanShowcaseHandler serves the user-facing Pelican gallery (read-only) and the
// admin actions on it: the gallery settings and removing a snapshot.
type PelicanShowcaseHandler struct {
	showcase *service.PelicanShowcaseService
}

func NewPelicanShowcaseHandler(showcase *service.PelicanShowcaseService) *PelicanShowcaseHandler {
	return &PelicanShowcaseHandler{showcase: showcase}
}

// List GET /api/v1/pelican-showcase
// A disabled gallery answers enabled=false with no groups rather than an error.
func (h *PelicanShowcaseHandler) List(c *gin.Context) {
	view, err := h.showcase.View(c.Request.Context(), time.Now())
	if err != nil {
		response.InternalError(c, "Failed to load pelican showcase")
		return
	}
	response.Success(c, view)
}

// GetItem GET /api/v1/pelican-showcase/items/:id
// Returns the raw model output; the frontend renders it in a sandboxed iframe.
func (h *PelicanShowcaseHandler) GetItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid item id")
		return
	}
	item, err := h.showcase.Item(c.Request.Context(), id, time.Now())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

// DeleteItem DELETE /api/v1/admin/pelican-showcase/items/:id
func (h *PelicanShowcaseHandler) DeleteItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid item id")
		return
	}
	if err := h.showcase.Remove(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// pelicanShowcaseSettings is the gallery switch plus its limits, as edited on the admin page.
type pelicanShowcaseSettings struct {
	Enabled       bool `json:"enabled"`
	MaxItems      int  `json:"max_items"`
	AutoCleanup   bool `json:"auto_cleanup"`
	RetentionDays int  `json:"retention_days"`
}

func pelicanShowcaseSettingsFrom(runtime service.PelicanShowcaseRuntime) pelicanShowcaseSettings {
	return pelicanShowcaseSettings{
		Enabled:       runtime.Enabled,
		MaxItems:      runtime.Config.MaxItems,
		AutoCleanup:   runtime.Config.AutoCleanup,
		RetentionDays: runtime.Config.RetentionDays,
	}
}

// GetSettings GET /api/v1/admin/pelican-showcase/settings
func (h *PelicanShowcaseHandler) GetSettings(c *gin.Context) {
	runtime, err := h.showcase.Settings(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Failed to load pelican showcase settings")
		return
	}
	response.Success(c, pelicanShowcaseSettingsFrom(runtime))
}

// UpdateSettings PUT /api/v1/admin/pelican-showcase/settings
func (h *PelicanShowcaseHandler) UpdateSettings(c *gin.Context) {
	var req pelicanShowcaseSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	runtime, err := h.showcase.UpdateSettings(c.Request.Context(), req.Enabled, service.PelicanShowcaseConfig{
		MaxItems:      req.MaxItems,
		AutoCleanup:   req.AutoCleanup,
		RetentionDays: req.RetentionDays,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	role, _ := middleware.GetUserRoleFromContext(c)
	slog.Info("settings updated", "audit", true, "user_id", subject.UserID, "role", role,
		"changed", []string{"pelican_showcase_enabled", "pelican_showcase_config"})
	response.Success(c, pelicanShowcaseSettingsFrom(runtime))
}
