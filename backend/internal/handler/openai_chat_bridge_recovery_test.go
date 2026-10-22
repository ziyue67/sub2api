//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type chatBridgeRecoveryUpstream struct {
	service.HTTPUpstream
	bodies     []string
	accountIDs []int64
}

func (u *chatBridgeRecoveryUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	index := len(u.accountIDs)
	u.accountIDs = append(u.accountIDs, accountID)
	if index >= len(u.bodies) {
		index = len(u.bodies) - 1
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(u.bodies[index]))}, nil
}

func (u *chatBridgeRecoveryUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

// Exercise the actual Chat handler and Ops middleware, not only the converter:
// a pre-output failure may recover, but a final in-band failure must affect SLA.
func TestChatBridgeHandlerRecoveryAndOpsOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	created := `data: {"type":"response.created","response":{"id":"resp_mock","model":"gpt-6-luna"}}` + "\n\n"
	partial := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n"
	failed := `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"backend unavailable"}}}` + "\n\n"
	completed := `data: {"type":"response.output_text.delta","delta":"OK"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":4,"output_tokens":1}}}` + "\n\n"
	for _, tc := range []struct {
		name      string
		bodies    []string
		wantCalls int
		recovered bool
	}{
		{"recovered_before_output", []string{created + failed, created + completed}, 2, true},
		{"failed_after_output", []string{created + partial + failed}, 1, false},
		{"exhausted_before_output", []string{created + failed}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 4)
			groupID := int64(77)
			accounts := []service.Account{
				{ID: 71, Name: "mock-first", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
					Status: service.StatusActive, Schedulable: true, Priority: 1, GroupIDs: []int64{groupID},
					Credentials: map[string]any{"access_token": "mock-token"}},
				{ID: 72, Name: "mock-second", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
					Status: service.StatusActive, Schedulable: true, Priority: 2, GroupIDs: []int64{groupID},
					Credentials: map[string]any{"access_token": "mock-token"}},
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Ops.Enabled = true
			cfg.Default.RateMultiplier = 1
			cfg.Gateway.MaxAccountSwitches = 1
			repo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
			upstream := &chatBridgeRecoveryUpstream{bodies: tc.bodies}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			t.Cleanup(gateway.StopOpenAICodexTicketHarvester)
			t.Cleanup(gateway.CloseOpenAIWSPool)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			h.maxAccountSwitches = 1
			opsRepo := &ingressRejectOpsRepo{}
			ops := service.NewOpsService(opsRepo, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
			require.True(t, ops.IsMonitoringEnabled(context.Background()), "Ops must be enabled for outcome assertions")
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 17, GroupID: &groupID,
					User:  &service.User{ID: 19, Status: service.StatusActive},
					Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}})
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 19})
				c.Next()
			})
			router.POST("/v1/chat/completions", h.ChatCompletions)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
				`{"model":"gpt-6-luna","messages":[{"role":"user","content":"hello"}],"stream":true}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			require.Len(t, upstream.accountIDs, tc.wantCalls, rec.Body.String())
			if tc.wantCalls == 2 {
				require.NotEqual(t, upstream.accountIDs[0], upstream.accountIDs[1], "recoverable failure must reach account switching")
			}
			for len(opsErrorLogQueue) > 0 {
				job := <-opsErrorLogQueue
				flushOpsErrorLogBatch([]opsErrorLogJob{job})
			}
			require.Len(t, opsRepo.entries, 1)
			entry := opsRepo.entries[0]
			if tc.recovered {
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, rec.Body.String(), `"content":"OK"`)
				require.NotContains(t, rec.Body.String(), "backend unavailable")
				require.Equal(t, 1, strings.Count(rec.Body.String(), `"role":"assistant"`))
				require.Equal(t, http.StatusOK, entry.StatusCode)
				require.Contains(t, entry.ErrorMessage, "Recovered upstream error")
			} else {
				require.GreaterOrEqual(t, entry.StatusCode, 400, "final failures must count toward SLA")
				require.NotContains(t, entry.ErrorMessage, "Recovered")
				require.Equal(t, "provider", entry.ErrorOwner)
				if tc.wantCalls == 1 {
					require.Equal(t, http.StatusOK, rec.Code, "cannot rewrite an already committed stream")
					require.Contains(t, rec.Body.String(), "partial")
				}
			}
		})
	}
}
