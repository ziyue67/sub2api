package handler

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRegionalBillingStandardModeUsesFundsReadiness(t *testing.T) {
	calls := 0
	funds := &service.RegionalMetering{Ledger: &ledger.Local{}, Admit: func(context.Context, *service.APIKey, *service.Account, []byte) (ledger.Reservation, ledger.Price, error) {
		panic("admit only after account selection")
	}, Readiness: func(context.Context) error { calls++; return nil }}
	gateway := &service.OpenAIGatewayService{}
	service.WithRegionalMetering(funds)(gateway)
	h := &OpenAIGatewayHandler{gatewayService: gateway, cfg: &config.Config{RunMode: config.RunModeStandard}}
	// Nil legacy billing and user objects ensure no old cache path is consulted.
	key := &service.APIKey{ID: 1}
	require.NoError(t, h.checkRegionalBillingEligibility(context.Background(), key, nil))
	_, done, err := reserveInflightBalanceCtx(context.Background(), nil, gateway, key, nil, service.InflightEstimateRequest{})
	require.NoError(t, err)
	done()
	require.Equal(t, 2, calls)
	failure := errors.New("funds unavailable")
	funds.Readiness = func(context.Context) error { return failure }
	require.ErrorIs(t, h.checkRegionalBillingEligibility(context.Background(), key, nil), failure)
	_, _, err = reserveInflightBalanceCtx(context.Background(), nil, gateway, key, nil, service.InflightEstimateRequest{})
	require.ErrorIs(t, err, failure)
}
