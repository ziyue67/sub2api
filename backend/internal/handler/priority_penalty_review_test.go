//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type reviewFailoverSlots struct {
	fakeConcurrencyCache
	beforeSecond       func()
	failed             bool
	acquired, released []int64
}

func (c *reviewFailoverSlots) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	c.acquired = append(c.acquired, id)
	if id == 2 && c.failed && c.beforeSecond != nil {
		f := c.beforeSecond
		c.beforeSecond = nil
		f()
	}
	return true, nil
}
func (c *reviewFailoverSlots) ReleaseAccountSlot(_ context.Context, id int64, _ string) error {
	c.released = append(c.released, id)
	return nil
}

func TestPriorityPenaltyReviewHandlerFailover(t *testing.T) {
	for _, chat := range []bool{false, true} {
		t.Run(fmt.Sprintf("chat=%t", chat), func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeSimple}
			penalty := service.DefaultPrioritySchedulingConfig()
			penalty.Enabled = true
			settings := service.NewSettingService(&penaltyModeSettings{}, cfg)
			require.NoError(t, settings.SavePrioritySchedulingConfig(context.Background(), penalty))
			slots := &reviewFailoverSlots{}
			concurrency := service.NewConcurrencyService(slots)
			repo := &initialAdmissionRepo{}
			for id := int64(1); id <= 2; id++ {
				repo.accounts = append(repo.accounts, service.Account{ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{3131}, Priority: int(id), Concurrency: 1, Credentials: map[string]any{"access_token": fmt.Sprintf("test-token-%d", id), "chatgpt_account_id": fmt.Sprintf("test-account-%d", id)}, Extra: map[string]any{"openai_excel_bps": true}})
			}
			upstream := &excelBPSFailoverUpstream{answer: func(int) *http.Response {
				// Responses uses BPS's explicit failover classification. Chat
				// 429 follows its existing same-account retry policy, so use
				// an ordinary retryable 503 to reach its account-switch seam.
				if chat {
					return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"previous synthetic supplier failure"}}`))}
				}
				response := excelBPS429("1")
				response.Body = io.NopCloser(strings.NewReader(`{"error":{"code":"basispoints_rate_limited","message":"previous synthetic supplier failure"}}`))
				return response
			}, onDo: func(int) { slots.failed = true }}
			gateway := service.NewOpenAIGatewayService(repo, nil, penaltyModeUsage{}, nil, nil, nil, nil, nil, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, settings, nil)
			t.Cleanup(func() { _ = gateway.CloseExcelBPSImages() })
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			h.maxAccountSwitches = 2
			group := int64(3131)
			ctx := service.WithPrioritySchedulingObservation(context.Background(), &group, "gpt-6-astra", "/v1/responses")
			selected, _, err := gateway.SelectAccountWithSchedulerForCapability(ctx, &group, "", "", "gpt-6-astra", nil, service.OpenAIUpstreamTransportAny, service.OpenAIEndpointCapabilityChatCompletions, false, false, true, service.PlatformOpenAI)
			require.NoError(t, err)
			require.NotNil(t, selected)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
			require.Eventually(t, func() bool {
				snapshot := gateway.PrioritySchedulingSnapshot()
				return snapshot != nil && snapshot.HistoryReady
			}, time.Second, time.Millisecond)
			slots.beforeSecond = func() {
				hard := penalty
				hard.LatencyPenaltyMode = service.PriorityLatencyStop
				require.NoError(t, settings.SavePrioritySchedulingConfig(context.Background(), hard))
			}
			requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, rec := newExcelBPSFailoverTestContext(requestCtx, false)
			if chat {
				body := `{"model":"gpt-6-astra","messages":[{"role":"user","content":"synthetic"}],"stream":false}`
				c.Request.Body = io.NopCloser(strings.NewReader(body))
				c.Request.ContentLength = int64(len(body))
				c.Request.URL.Path = "/v1/chat/completions"
				h.ChatCompletions(c)
			} else {
				h.Responses(c)
			}
			require.Contains(t, slots.acquired, int64(2), "precondition: first supplier failure must enter second account acquisition")
			require.Equal(t, http.StatusServiceUnavailable, rec.Code, "late policy rejection must replace previous supplier error: %s", rec.Body.String())
			require.Contains(t, rec.Body.String(), "temporarily_unavailable", "late policy rejection must replace previous supplier error")
			require.NotContains(t, rec.Body.String(), "previous synthetic supplier failure")
			require.Equal(t, "5", rec.Header().Get("Retry-After"))
			require.Equal(t, []int64{1}, upstream.calls(), "second stopped account must never receive an upstream request")
			require.Contains(t, slots.released, int64(2), "second account slot must be released")
		})
	}
}
