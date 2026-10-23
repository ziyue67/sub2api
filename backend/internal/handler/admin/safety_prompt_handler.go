package admin

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type safetyPromptConfigRequest struct {
	Enabled       bool `json:"enabled"`
	SignalEnabled bool `json:"signal_enabled"`
	RetentionDays int  `json:"retention_days"`
}

func (h *ContentModerationHandler) GetSafetyPromptConfig(c *gin.Context) {
	if !ipAuditAdministrator(c) {
		return
	}
	if h.safetyPromptConfig == nil || h.safetyPromptStore == nil {
		c.JSON(503, gin.H{"error": "safety prompt module unavailable"})
		return
	}
	cfg := h.safetyPromptConfig.GetSafetyPromptConfig(c.Request.Context())
	c.JSON(200, gin.H{"config": cfg, "stats": h.safetyPromptStore.Stats(cfg.RetentionDays)})
}

func (h *ContentModerationHandler) UpdateSafetyPromptConfig(c *gin.Context) {
	if !ipAuditAdministrator(c) {
		return
	}
	if h.safetyPromptConfig == nil {
		c.JSON(503, gin.H{"error": "safety prompt module unavailable"})
		return
	}
	var req safetyPromptConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid configuration"})
		return
	}
	cfg := service.SafetyPromptConfig{Enabled: req.Enabled, SignalEnabled: req.SignalEnabled, RetentionDays: req.RetentionDays}
	if !cfg.Valid() {
		c.JSON(400, gin.H{"error": "retention_days must be 1..90"})
		return
	}
	if err := h.safetyPromptConfig.UpdateSafetyPromptConfig(c.Request.Context(), cfg); err != nil {
		c.JSON(500, gin.H{"error": "could not save configuration"})
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": cfg.Enabled, "signal_enabled": cfg.SignalEnabled, "retention_days": cfg.RetentionDays})
	c.JSON(200, gin.H{"config": cfg})
}

func (h *ContentModerationHandler) ListSafetyPromptEvents(c *gin.Context) {
	if !ipAuditAdministrator(c) {
		return
	}
	if h.safetyPromptStore == nil || h.safetyPromptConfig == nil {
		c.JSON(503, gin.H{"error": "safety prompt module unavailable"})
		return
	}
	cfg := h.safetyPromptConfig.GetSafetyPromptConfig(c.Request.Context())
	from, err := parseSafetyPromptDate(c.Query("from"), false)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid from date"})
		return
	}
	to, err := parseSafetyPromptDate(c.Query("to"), true)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid to date"})
		return
	}
	events := h.safetyPromptStore.List(from, to, c.Query("search"), cfg.RetentionDays)
	middleware.SetAuditExtra(c, map[string]any{"event_count": len(events)})
	c.JSON(200, gin.H{"events": events, "stats": h.safetyPromptStore.Stats(cfg.RetentionDays)})
}

func parseSafetyPromptDate(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		value = value.Add(24*time.Hour - time.Nanosecond)
	}
	return value, nil
}
