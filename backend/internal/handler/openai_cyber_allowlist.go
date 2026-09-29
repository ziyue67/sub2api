package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) cyberPolicyLogOnly(c *gin.Context, apiKey *service.APIKey) bool {
	return h != nil && c != nil && c.Request != nil && h.gatewayService.CyberPolicyLogOnly(c.Request.Context(), apiKey)
}

// Both HTTP and WebSocket admission skip existing blocks for trusted users.
//
// 本 Fork 的 cyber 会话身份按「显式会话身份 → 类型化 v3 屏蔽键」解析（见
// service.ResolveCyberSessionIdentity），不引入上游的 transcript 相似度键：
// 上游 0.2.10 的 findBlockedCyberSessionKey/buildCyberSessionBlockWritePlan 依赖
// openai_cyber_transcript.go，而该文件在本 Fork 的移植中已被删除（本 Fork 采用
// 严格身份门控）。此处保留上游「白名单用户（log-only）跳过既有屏蔽查询」的语义，
// 查询实现复用本 Fork 的身份解析链路。
func (h *OpenAIGatewayHandler) findBlockedCyberSessionForAPIKey(c *gin.Context, apiKey *service.APIKey, body []byte) string {
	if apiKey == nil || h.cyberPolicyLogOnly(c, apiKey) {
		return ""
	}
	clientIP, userAgent := "", ""
	if c != nil {
		clientIP = ip.GetClientIP(c)
		userAgent = c.GetHeader("User-Agent")
	}
	return h.gatewayService.FindCyberSessionBlockedForRequest(c.Request.Context(), apiKey.ID, c, body, clientIP, userAgent)
}
