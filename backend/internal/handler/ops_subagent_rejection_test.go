package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsErrorLoggerMiddleware_RecordsSubagentRejections(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		header    bool
		websocket bool
		laterTurn bool
	}{
		{name: "http_body", body: `{"model":"gpt-blocked","stream":true,"input":"private prompt","client_metadata":{"x-openai-subagent":"worker"}}`},
		{name: "http_header", header: true, body: `{"model":"gpt-blocked","stream":true,"input":"private prompt"}`},
		{name: "websocket_first_frame", websocket: true, body: `{"type":"response.create","model":"gpt-blocked","input":"private prompt","client_metadata":{"x-openai-subagent":"worker"}}`},
		{name: "websocket_later_frame", websocket: true, laterTurn: true, body: `{"type":"response.create","response":{"model":"gpt-blocked","input":"private prompt","client_metadata":{"x-openai-subagent":"worker"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			gin.SetMode(gin.TestMode)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			groupID := int64(53)
			key := &service.APIKey{
				ID: 41, Key: "sk-example-test-key-do-not-use", GroupID: &groupID,
				User:  &service.User{ID: 29},
				Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, BlockSubagents: true},
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), ctxkey.RequestID, "gateway-test-id")
				ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "client-test-id")
				c.Request = c.Request.WithContext(ctx)
				c.Set(string(middleware2.ContextKeyAPIKey), key)
			})
			router.Use(OpsErrorLoggerMiddleware(ops), middleware2.GroupBlockSubagents())
			method := http.MethodPost
			if tc.websocket {
				method = http.MethodGet
			}
			upstreamCalls := 0
			router.Handle(method, "/v1/responses", func(c *gin.Context) {
				if tc.websocket {
					if tc.laterTurn {
						require.False(t, middleware2.CheckGroupSubagentFrame(c, []byte(`{"type":"response.create","model":"gpt-previous"}`)))
						// A previous WS turn may have selected a composite target and
						// left provider telemetry. The denial belongs to employee ingress.
						targetGroupID := int64(99)
						c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 41, GroupID: &targetGroupID})
						c.Set(opsModelKey, "gpt-previous")
						c.Set(opsAccountIDKey, int64(83))
						c.Set(opsDedicatedErrorRecordedKey, true)
						c.Set(service.OpsUpstreamStatusCodeKey, http.StatusTooManyRequests)
						c.Set(service.OpsUpstreamErrorMessageKey, "previous provider error")
					}
					if middleware2.CheckGroupSubagentFrame(c, []byte(tc.body)) {
						c.Status(http.StatusSwitchingProtocols)
						return
					}
				}
				upstreamCalls++
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(method, "/v1/responses", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "codex-test-client")
			req.RemoteAddr = "192.0.2.10:12345"
			if tc.header {
				req.Header.Set("X-OpenAI-Subagent", "worker")
			}
			if tc.websocket {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)

			if tc.websocket {
				require.Equal(t, http.StatusSwitchingProtocols, recorder.Code)
			} else {
				require.Equal(t, http.StatusForbidden, recorder.Code)
			}
			require.Zero(t, upstreamCalls)
			require.Equal(t, int64(1), OpsErrorLogQueueLength(), "each denied request must produce one error detail")
			entry := (<-opsErrorLogQueue).entry
			require.Equal(t, "gateway-test-id", entry.RequestID)
			require.Equal(t, "client-test-id", entry.ClientRequestID)
			require.Equal(t, key.ID, *entry.APIKeyID)
			require.Equal(t, key.User.ID, *entry.UserID)
			require.Equal(t, groupID, *entry.GroupID)
			require.Equal(t, "gpt-blocked", entry.Model)
			require.Equal(t, entry.Model, entry.RequestedModel)
			require.True(t, entry.Stream)
			requestType := service.RequestTypeStream
			if tc.websocket {
				requestType = service.RequestTypeWSV2
			}
			require.Equal(t, int16(requestType), *entry.RequestType)
			require.Equal(t, "192.0.2.10", *entry.ClientIP)
			require.Equal(t, "codex-test-client", entry.UserAgent)
			require.Equal(t, http.StatusForbidden, entry.StatusCode)
			require.Equal(t, "request", entry.ErrorPhase)
			require.Equal(t, "permission_error", entry.ErrorType)
			require.Equal(t, "client", entry.ErrorOwner)
			require.Equal(t, "client_request", entry.ErrorSource)
			require.True(t, entry.IsBusinessLimited, "policy denials must stay outside service failure SLA")
			require.Contains(t, entry.ErrorBody, "subagents_blocked")
			require.Equal(t, middleware2.GroupSubagentsBlockedMessage, entry.ErrorMessage)
			require.NotContains(t, entry.ErrorBody, "private prompt")
			require.NotContains(t, entry.ErrorBody, key.Key)
			require.Nil(t, entry.AccountID)
			require.Nil(t, entry.UpstreamStatusCode)
			require.Empty(t, entry.UpstreamEndpoint)
			require.Empty(t, entry.UpstreamErrorsJSON)
		})
	}
}

func TestOpsErrorLoggerMiddleware_SubagentLoggingRespectsMonitoringSwitch(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			gin.SetMode(gin.TestMode)
			cfg := &config.Config{Ops: config.OpsConfig{Enabled: !disabled}}
			ops := service.NewOpsService(nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 41,
					Group: &service.Group{BlockSubagents: true}})
			})
			router.Use(OpsErrorLoggerMiddleware(ops), middleware2.GroupBlockSubagents())
			router.POST("/v1/responses", func(c *gin.Context) { t.Fatal("denied request reached handler") })
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test"}`))
			req.Header.Set("X-OpenAI-Subagent", "worker")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			if disabled {
				require.Zero(t, OpsErrorLogEnqueuedTotal())
			} else {
				require.Equal(t, int64(1), OpsErrorLogEnqueuedTotal())
			}
		})
	}
}
