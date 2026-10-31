//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPriorityLatencyAvailabilityHandlerSlots(t *testing.T) {
	for _, path := range []string{"quick", "queued", "acquired"} {
		t.Run(path, func(t *testing.T) {
			cfg := service.DefaultPrioritySchedulingConfig()
			cfg.Enabled = true
			cfg.LatencyPenaltyMode = service.PriorityLatencyStop
			settings := service.NewSettingService(&penaltyModeSettings{}, nil)
			require.NoError(t, settings.SavePrioritySchedulingConfig(context.Background(), cfg))
			slots := &penaltySlotCache{full: path != "acquired"}
			account := service.Account{ID: 901, Name: "synthetic", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{11}}
			gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}, nil, penaltyModeUsage{}, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil, service.NewConcurrencyService(slots), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil)
			t.Cleanup(func() { _ = gateway.CloseExcelBPSImages() })
			group := int64(11)
			ctx := service.WithPrioritySchedulingObservation(context.Background(), &group, "public-model", "/v1/responses")
			selectAccount := func() *service.AccountSelectionResult {
				selected, _, err := gateway.SelectAccountWithSchedulerForCapability(ctx, &group, "", "", "gpt-test", nil, service.OpenAIUpstreamTransportAny, service.OpenAIEndpointCapabilityChatCompletions, false, false, true, service.PlatformOpenAI)
				require.NoError(t, err)
				require.NotNil(t, selected)
				return selected
			}
			selected := selectAccount()
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
			require.Eventually(t, func() bool {
				snapshot := gateway.PrioritySchedulingSnapshot()
				return snapshot != nil && snapshot.HistoryReady
			}, time.Second, time.Millisecond)
			selected = selectAccount()
			snapshot := gateway.PrioritySchedulingSnapshot()
			require.NotNil(t, snapshot)
			require.True(t, snapshot.Candidates[0].LatencyStopped, "fallback must retain measured penalty")
			cache := &penaltySlotCache{wait: path == "queued"}
			h := &OpenAIGatewayHandler{gatewayService: gateway, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0)}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			started := false
			release, result := h.acquireResponsesAccountSlot(c, &group, "", selected, false, &started, zap.NewNop())
			require.Equal(t, openAISlotAcquireOK, result, "same fallback must survive slot rechecks: %s", recorder.Body.String())
			require.NotNil(t, release)
			release()
			require.NotContains(t, recorder.Body.String(), "temporarily_unavailable")
			require.NoError(t, gateway.CheckPriorityLatencySelection(c.Request.Context(), selected))
			if path == "acquired" {
				require.Equal(t, 2, slots.releases)
			} else {
				require.Equal(t, 1, cache.releases)
			}
		})
	}
}
