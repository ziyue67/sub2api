package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIOAuthWSSSEAccelerationReason = "oauth_http_sse_acceleration"

// IsOpenAIOAuthWSSSEAccelerationEnabled is deliberately opt-in and limited to
// ordinary OAuth accounts. Other credentials and adapters keep their transport.
func (a *Account) IsOpenAIOAuthWSSSEAccelerationEnabled() bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeOAuth ||
		a.IsShadow() || a.IsOpenAIAgentIdentity() || a.IsOpenAIPersonalAccessToken() {
		return false
	}
	enabled, _ := a.Extra["openai_oauth_ws_sse_acceleration"].(bool)
	return enabled
}

// resolveOpenAIHTTPWSSSEDecision preserves the existing account/global WS gates.
// Passthrough and plugin accounts retain their own request transformation and
// transport contracts rather than silently switching to the native forwarder.
func (s *OpenAIGatewayService) resolveOpenAIHTTPWSSSEDecision(
	c *gin.Context, account *Account, body []byte, decision OpenAIWSProtocolDecision,
) OpenAIWSProtocolDecision {
	clientTransport := GetOpenAIClientTransport(c)
	if clientTransport == OpenAIClientTransportHTTP &&
		decision.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 &&
		account.IsOpenAIOAuthWSSSEAccelerationEnabled() && !account.IsOpenAIPassthroughEnabled() &&
		c.Request != nil && c.Request.Method == http.MethodPost &&
		strings.HasSuffix(strings.TrimRight(c.Request.URL.Path, "/"), "/responses") &&
		!isOpenAICompatMessagesBridgeBody(body) &&
		gjson.GetBytes(body, "stream").Type == gjson.True &&
		strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String()) == "" &&
		(s.pluginManager == nil || !s.pluginManager.ShouldRouteOpenAIOAuth(account)) {
		decision.Reason = openAIOAuthWSSSEAccelerationReason
		return decision
	}
	return resolveOpenAIWSDecisionByClientTransport(decision, clientTransport)
}

// A failed handshake precedes response.create and can safely fall back to HTTP.
// A missing first event after writing the request is NOT proof it was unsent.
func canFallbackOpenAIWSSSEHandshake(ctx context.Context, c *gin.Context, err error) bool {
	if err == nil || ctx.Err() != nil || c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	var dialErr *openAIWSDialError
	if !errors.As(err, &dialErr) {
		return false
	}
	switch dialErr.StatusCode {
	case 0, http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed,
		http.StatusUpgradeRequired, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		// Authentication, permission and rate-limit failures keep their normal
		// error handling; changing transports must not circumvent them.
		return false
	}
}
