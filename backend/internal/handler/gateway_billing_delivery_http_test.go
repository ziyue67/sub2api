//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type httpDeliveryBilling struct {
	service.UsageBillingRepository
	event        *service.UsageBillingEvent
	failure      error
	stageFailure error
	applied      bool
}

func (r *httpDeliveryBilling) StageUsageBilling(_ context.Context, c *service.UsageBillingCommand, u *service.UsageLog) error {
	if r.stageFailure != nil {
		return r.stageFailure
	}
	r.event = &service.UsageBillingEvent{Version: 1, Command: *c, Usage: *u}
	return nil
}
func (r *httpDeliveryBilling) ApplyStagedUsageBilling(context.Context, service.UsageBillingEventKey) (*service.UsageBillingRecovery, error) {
	if r.failure != nil {
		return nil, r.failure
	}
	fresh := !r.applied
	r.applied = true
	return &service.UsageBillingRecovery{Event: *r.event, Result: service.UsageBillingApplyResult{Applied: fresh}, NeedsCache: true}, nil
}
func (*httpDeliveryBilling) PendingUsageBilling(context.Context, int) ([]service.UsageBillingEventKey, error) {
	return nil, nil
}
func (*httpDeliveryBilling) AcknowledgeUsageBillingCache(context.Context, service.UsageBillingEventKey) error {
	return nil
}

// Real HTTP transport + production protocol adapter + GatewayHandler. The SQL
// crash/atomicity half is exercised separately with PostgreSQL integration tests.
func TestGatewayHTTPAdapterStagesUsageBeforeExecutionAndNeverRecallsModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions"} {
		t.Run(endpoint, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, compatStreamUsagePartialStream+compatStreamUsageCompletion)
			}))
			defer upstream.Close()
			billing := &httpDeliveryBilling{failure: errors.New("injected execution failure after durable stage")}
			run := runCompatStreamUsageHandlerWithDelivery(t, endpoint, "", true, billing, upstream.URL)
			require.Equal(t, int32(1), calls.Load())
			require.Contains(t, run.rec.Body.String(), "partial")
			require.NotNil(t, billing.event, "handler must stage before returning, not queue an in-memory closure")
			require.Equal(t, 12, billing.event.Usage.InputTokens)
			require.Equal(t, 7, billing.event.Usage.OutputTokens)
			require.Greater(t, billing.event.Usage.ActualCost, 0.0)
			require.Empty(t, run.usageRepo.created, "failure is not a fake zero-cost success")
			billing.failure = nil
			key := service.UsageBillingEventKey{RequestID: billing.event.Command.RequestID, APIKeyID: billing.event.Command.APIKeyID}
			first, err := billing.ApplyStagedUsageBilling(context.Background(), key)
			require.NoError(t, err)
			require.True(t, first.Result.Applied)
			second, err := billing.ApplyStagedUsageBilling(context.Background(), key)
			require.NoError(t, err)
			require.False(t, second.Result.Applied)
			require.Equal(t, int32(1), calls.Load(), "billing recovery cannot invoke a model transport")
		})
	}
}
