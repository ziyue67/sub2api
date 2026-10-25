//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayHandlerRealHTTPDeliveryFailureDoesNotInventUsageOrRecallModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, phase := range []string{"stage", "execute"} {
		t.Run(phase, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"resp_billing_boundary","object":"response","status":"completed","model":"gpt-5.1","output":[],"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`)
			}))
			defer upstream.Close()
			cfg := &config.Config{}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.AllowPrivateHosts = true
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			groupID := int64(9201)
			group := &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1}
			account := service.Account{ID: 9202, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
				Credentials: map[string]any{"api_key": "synthetic", "base_url": upstream.URL}}
			accounts := &openAIWSUsageHandlerAccountRepoStub{account: account}
			logs := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
			billing := &httpDeliveryBilling{}
			if phase == "stage" {
				billing.stageFailure = errors.New("injected durable stage failure")
			} else {
				billing.failure = errors.New("injected execution failure")
			}
			billingCache := service.NewBillingCacheService(newHandlerInflightCache(100), nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			})
			gateway := service.NewOpenAIGatewayService(accounts, nil, logs, billing, nil, nil, nil, nil, cfg, nil, concurrency,
				service.NewBillingService(cfg, nil), nil, nil, repository.NewHTTPUpstream(cfg), &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			t.Cleanup(gateway.CloseOpenAIWSPool)
			h := &OpenAIGatewayHandler{gatewayService: gateway, cfg: cfg, billingCacheService: billingCache,
				apiKeyService: &service.APIKeyService{}, concurrencyHelper: NewConcurrencyHelper(concurrency, SSEPingFormatNone, 0)}
			key := &service.APIKey{ID: 9203, UserID: 9204, GroupID: &groupID, Group: group,
				User: &service.User{ID: 9204, Status: service.StatusActive, Balance: 100}}
			router := gin.New()
			router.Use(middleware.ClientRequestID(), func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 1})
				c.Next()
			})
			finished := make(chan struct{})
			router.POST("/openai/v1/responses", func(c *gin.Context) {
				defer close(finished)
				h.Responses(c)
			})
			ingress := httptest.NewServer(router)
			defer ingress.Close()
			client := &http.Client{Timeout: 5 * time.Second}
			res, err := client.Post(ingress.URL+"/openai/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5.1","input":"synthetic","stream":false}`))
			require.NoError(t, err)
			body, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			require.NoError(t, err)
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("actual OpenAI handler did not finish its delivery boundary")
			}
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Contains(t, string(body), "resp_billing_boundary")
			require.Equal(t, int32(1), calls.Load())
			require.Empty(t, logs.created, "neither a stage failure nor an execution failure is a successful zero-cost usage row")
			if phase == "stage" {
				require.Nil(t, billing.event, "pre-storage failure is an explicit unrecoverable observation boundary")
				return
			}
			require.NotNil(t, billing.event, "the actual OpenAI handler must stage synchronously before it returns")
			require.Equal(t, 12, billing.event.Usage.InputTokens)
			require.Equal(t, 7, billing.event.Usage.OutputTokens)
			require.Greater(t, billing.event.Usage.ActualCost, 0.0)
			billing.failure = nil
			eventKey := service.UsageBillingEventKey{RequestID: billing.event.Command.RequestID, APIKeyID: key.ID}
			for repeat := 0; repeat < 2; repeat++ {
				result, err := billing.ApplyStagedUsageBilling(context.Background(), eventKey)
				require.NoError(t, err)
				require.Equal(t, repeat == 0, result.Result.Applied)
			}
			require.Equal(t, int32(1), calls.Load())
		})
	}
}
