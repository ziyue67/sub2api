package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// logOpsSubagentRejection adds employee policy denials to Usage > Error requests
// while retaining the separate ingress aggregates. Build a local-only row: a
// previous WS turn's response, selected account or provider errors are unrelated.
func logOpsSubagentRejection(c *gin.Context, ops *service.OpsService) {
	rejection, ok := middleware2.GetGroupSubagentRejection(c)
	if !ok || rejection.APIKey == nil || ops == nil || !ops.IsMonitoringEnabled(c.Request.Context()) {
		return
	}
	const body = `{"error":{"type":"permission_error","code":"subagents_blocked","message":"` + middleware2.GroupSubagentsBlockedMessage + `"}}`
	if shouldSkipOpsErrorLog(c.Request.Context(), ops, middleware2.GroupSubagentsBlockedMessage, body, c.Request.URL.Path) {
		return
	}
	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	if requestID = strings.TrimSpace(requestID); requestID == "" {
		requestID = c.Writer.Header().Get("X-Request-Id")
	}
	clientRequestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	requestType := service.RequestTypeFromLegacy(rejection.Stream, rejection.WebSocket)
	requestTypeValue := int16(requestType)
	key := rejection.APIKey
	entry := &service.OpsInsertErrorLogInput{
		RequestID: requestID, ClientRequestID: clientRequestID,
		APIKeyID: &key.ID, APIKeyPrefix: keyPrefix(key.Key, 8), GroupID: key.GroupID,
		Platform: resolveOpsPlatform(c.Request.Context(), key, guessPlatformFromPath(c.Request.URL.Path)),
		Model:    rejection.Model, RequestedModel: rejection.Model,
		RequestPath: c.Request.URL.Path, InboundEndpoint: GetInboundEndpoint(c),
		Stream: rejection.Stream, RequestType: &requestTypeValue, UserAgent: c.GetHeader("User-Agent"),
		ErrorPhase: "request", ErrorType: "permission_error", Severity: "P3",
		StatusCode: http.StatusForbidden, IsBusinessLimited: true,
		ErrorMessage: middleware2.GroupSubagentsBlockedMessage, ErrorBody: body,
		ErrorSource: "client_request", ErrorOwner: "client", CreatedAt: time.Now(),
		DurationMs: getContextLatencyMs(c, service.OpsRequestDurationMsKey),
	}
	if key.User != nil {
		entry.UserID = &key.User.ID
	}
	if key.Group != nil && key.Group.Platform != "" {
		entry.Platform = key.Group.Platform
	}
	if clientIP := strings.TrimSpace(ip.GetClientIP(c)); clientIP != "" {
		entry.ClientIP = &clientIP
	}
	enqueueOpsErrorLog(ops, entry)
}
