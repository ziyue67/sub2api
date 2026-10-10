package service

import (
	"context"
	"encoding/json"
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

// This error is produced locally before any response.create/prewarm write.
// Never infer replay safety from a peer close code or the absence of an event.
var errOpenAIWSSSEPayloadTooLarge = errors.New("HTTP SSE acceleration payload exceeds WS limit")

const openAIWSSSEMaxPayloadBytesDefault int64 = 15 * 1024 * 1024

func (s *OpenAIGatewayService) openAIWSSSEMaxPayloadBytes() int64 {
	if s == nil || s.cfg == nil || s.cfg.Gateway.OpenAIWS.SSEAccelerationMaxPayloadBytes <= 0 {
		return openAIWSSSEMaxPayloadBytesDefault
	}
	return s.cfg.Gateway.OpenAIWS.SSEAccelerationMaxPayloadBytes
}

// encodeOpenAIWSSSEPayload uses the same Encoder write-through pattern as
// wsjson.Write. The encoder's buffer is borrowed only until write returns: no
// second marshal, full-payload copy, retained buffer, or new shared pool/lock.
// Validate before invoking write, which may acquire a send quota. Optional
// prewarm waits stay outside the callback so they do not retain this buffer.
func encodeOpenAIWSSSEPayload(value any, maxBytes int64, write func([]byte) error) error {
	return json.NewEncoder(openAIWSSSEPayloadWriter(func(p []byte) (int, error) {
		if int64(len(p)) > maxBytes {
			return 0, errOpenAIWSSSEPayloadTooLarge
		}
		if err := write(p); err != nil {
			return 0, err
		}
		return len(p), nil
	})).Encode(value)
}

type openAIWSSSEPayloadWriter func([]byte) (int, error)

func (w openAIWSSSEPayloadWriter) Write(p []byte) (int, error) {
	return w(p)
}

func openAIWSSSEFallbackReason(ctx context.Context, c *gin.Context, err error) string {
	if err == nil || ctx.Err() != nil || c == nil || c.Writer == nil || c.Writer.Written() {
		return ""
	}
	switch {
	case errors.Is(err, errOpenAIWSSSEPayloadTooLarge):
		return "oauth_ws_sse_payload_too_large"
	case canFallbackOpenAIWSSSEHandshake(ctx, c, err):
		return "oauth_ws_sse_handshake_fallback"
	default:
		return ""
	}
}
