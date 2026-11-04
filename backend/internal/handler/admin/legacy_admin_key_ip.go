package admin

import (
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetLegacyAdminKeyIPPolicy(c *gin.Context) {
	if !requireAdminKeyJWT(c) {
		return
	}
	p, err := h.settingService.GetLegacyAdminKeyIPPolicy(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "无法读取旧版管理密钥 IP 策略")
		return
	}
	response.Success(c, gin.H{"enabled": p.Enabled, "allowed_ips": p.AllowedIPs, "current_ip": ip.GetTrustedClientIP(c)})
}

func (h *SettingHandler) UpdateLegacyAdminKeyIPPolicy(c *gin.Context) {
	if !requireAdminKeyJWT(c) {
		return
	}
	var req struct {
		Enabled    *bool     `json:"enabled"`
		AllowedIPs *[]string `json:"allowed_ips"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil || req.AllowedIPs == nil {
		response.Error(c, 400, "必须同时提交开关及 IP 列表")
		return
	}
	before, readErr := h.settingService.GetLegacyAdminKeyIPPolicy(c.Request.Context())
	// A JWT administrator must be able to repair a corrupt policy. Read errors
	// never cause legacy Key authentication to allow access.
	after, err := h.settingService.SetLegacyAdminKeyIPPolicy(c.Request.Context(), service.LegacyAdminKeyIPPolicy{Enabled: *req.Enabled, AllowedIPs: *req.AllowedIPs})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	slog.Info("legacy admin key IP policy updated", "audit", true, "user_id", subject.UserID, "client_ip", ip.GetTrustedClientIP(c), "before_read_failed", readErr != nil, "before", before, "after", after)
	response.Success(c, gin.H{"enabled": after.Enabled, "allowed_ips": after.AllowedIPs, "current_ip": ip.GetTrustedClientIP(c)})
}
