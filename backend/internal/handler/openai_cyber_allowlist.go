package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) cyberPolicyLogOnly(c *gin.Context, apiKey *service.APIKey) bool {
	return h != nil && c != nil && c.Request != nil && h.gatewayService.CyberPolicyLogOnly(c.Request.Context(), apiKey)
}

// findBlockedCyberSessionForRequest 在显式身份之外再叠加 body 指纹回退：
// 客户端未提供显式会话身份时，用内容指纹查既有屏蔽键，
// 使被 cyber 屏蔽的相同重试在选号前即被拒绝。
// Both HTTP and WebSocket admission skip existing blocks for trusted users.
// Session identity is still resolved and observed by the caller.
// findBlockedCyberSessionForRequest also uses the content fingerprint fallback
// when the client supplied no explicit session identity, so an identical retry
// of a cyber-blocked request is rejected before account selection.
func (h *OpenAIGatewayHandler) findBlockedCyberSessionForRequest(c *gin.Context, apiKey *service.APIKey, identity service.CyberSessionIdentityResolution, body []byte) string {
	if apiKey == nil || h.cyberPolicyLogOnly(c, apiKey) {
		return ""
	}
	return h.gatewayService.FindCyberSessionBlockedForIdentityAndBody(c.Request.Context(), apiKey.ID, identity, body)
}
