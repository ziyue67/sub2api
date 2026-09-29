package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) cyberPolicyLogOnly(c *gin.Context, apiKey *service.APIKey) bool {
	return h != nil && c != nil && c.Request != nil && h.gatewayService.CyberPolicyLogOnly(c.Request.Context(), apiKey)
}

// findBlockedCyberSessionForIdentity 是 HTTP 与 WebSocket 准入共用的既有会话屏蔽门控：
// 白名单（log-only）用户直接放行，其余按类型化会话身份查询既有屏蔽键。
//
// 本 Fork 的 cyber 会话身份按「显式会话身份 → 类型化屏蔽键」解析（见
// service.ResolveCyberSessionIdentity），不引入上游的 transcript 相似度键：
// 上游 0.2.10 的 findBlockedCyberSessionKey/buildCyberSessionBlockWritePlan 依赖
// openai_cyber_transcript.go，而该文件在本 Fork 的移植中已被删除（本 Fork 采用
// 严格身份门控）。此处保留上游「白名单用户（log-only）跳过既有屏蔽查询」的语义，
// 查询实现复用本 Fork 的身份解析链路。
//
// 传入已解析身份而非原始 body：WebSocket 握手与后续 turn 复用连接绑定身份
// （identity.LookupKeys），不能重新从 payload 推断，否则继承身份会丢失。
func (h *OpenAIGatewayHandler) findBlockedCyberSessionForIdentity(
	c *gin.Context,
	apiKey *service.APIKey,
	identity service.CyberSessionIdentityResolution,
) string {
	if apiKey == nil || h.cyberPolicyLogOnly(c, apiKey) {
		return ""
	}
	return h.gatewayService.FindCyberSessionBlockedForIdentity(c.Request.Context(), identity)
}
