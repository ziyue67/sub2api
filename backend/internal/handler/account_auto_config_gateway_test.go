package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type autoConfigStreamUpstream struct {
	service.HTTPUpstream
	body string
}

func (u *autoConfigStreamUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

func TestAutoConfigGatewayLargeCompletedStream(t *testing.T) {
	for _, name := range []string{"native", "passthrough", "oauth", "bps"} {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			groupID := int64(7)
			accounts := []service.Account{{ID: 71, Name: "mock-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 10, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "mock-key", "base_url": "https://api.example.test"}, Extra: map[string]any{"openai_passthrough": name == "passthrough"}}}
			if name == "oauth" || name == "bps" {
				accounts[0].Type = service.AccountTypeOAuth
				accounts[0].Credentials = map[string]any{"access_token": "mock-access-token", "chatgpt_account_id": "mock-account"}
				accounts[0].Extra["openai_excel_bps"] = name == "bps"
			}

			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.MaxAccountSwitches = 1
			accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
			upstream := &autoConfigStreamUpstream{body: "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_mock\",\"status\":\"completed\",\"model\":\"gpt-5.6-sol\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"" + strings.Repeat("x", 32*1024) + "\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":10}}}\n\n"}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			gateway := service.NewOpenAIGatewayService(accountRepo, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			t.Cleanup(gateway.StopOpenAICodexTicketHarvester)
			t.Cleanup(gateway.CloseOpenAIWSPool)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			ops := service.NewOpsService(nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
			var got []service.AccountConcurrencyResult
			policy := service.DefaultOAuthAutoConfig()
			policy.Revision = "mock-rule"
			current := 10
			state := service.AutoConfigConcurrencyState{}
			ops.SetAutoConfigObserver(func(r service.AccountConcurrencyResult) {
				got = append(got, r)
				state, current = service.AdvanceConcurrency(state, current, policy, r, time.Now().UTC())
			})
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 17, GroupID: &groupID, User: &service.User{ID: 19, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 19, Concurrency: 0})
				c.Next()
			})
			router.POST("/v1/responses", h.Responses)
			for i := 0; i < policy.SuccessesPerStep; i++ {
				req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{\"model\":\"gpt-5.6-sol\",\"input\":\"hello\",\"stream\":true}"))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "response.completed")
				require.Len(t, got, i+1)
				require.Equal(t, int64(71), got[i].AccountID)
				require.True(t, got[i].Success, "a completed generation must increment progress beyond the error-log probe limit")
				if i == 0 {
					require.Equal(t, 1, state.Successes)
				}
			}
			require.Equal(t, 11, current)
			require.Zero(t, state.Successes)
			require.NotNil(t, state.LastUpgradeAt)

		})
	}
}

func (u *autoConfigStreamUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}
