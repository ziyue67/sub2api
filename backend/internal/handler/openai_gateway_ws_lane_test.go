package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// These probes embed the base cache contracts because this regression only
// exercises capability detection.  The slot methods are deliberately not
// called: the handler helper must decide the namespace before an acquisition
// is attempted.
type openAIWSLegacyCacheProbe struct {
	service.ConcurrencyCache
}

type openAIWSLaneCacheProbe struct {
	service.ConcurrencyCache
	service.LaneConcurrencyCache
}

var _ service.ConcurrencyCache = (*openAIWSLegacyCacheProbe)(nil)
var _ service.ConcurrencyCache = (*openAIWSLaneCacheProbe)(nil)
var _ service.LaneConcurrencyCache = (*openAIWSLaneCacheProbe)(nil)

func TestOpenAIWSLaneIDForAccountUsesLaneCapability(t *testing.T) {
	lane := &service.AccountProxyLane{ID: 101}
	account := &service.Account{
		ID:                7,
		ProxyLanes:        []service.AccountProxyLane{*lane},
		SelectedProxyLane: lane,
	}

	laneService := service.NewConcurrencyService(&openAIWSLaneCacheProbe{})
	require.True(t, laneService.LaneConcurrencySupported())
	require.Equal(t, int64(101), openAIWSLaneIDForAccount(account, laneService),
		"lane-capable caches may use the selected lane namespace")

	legacyService := service.NewConcurrencyService(&openAIWSLegacyCacheProbe{})
	require.False(t, legacyService.LaneConcurrencySupported())
	require.Zero(t, openAIWSLaneIDForAccount(account, legacyService),
		"legacy caches must stay on the account namespace")

	require.Zero(t, openAIWSLaneIDForAccount(account, nil),
		"a missing concurrency service must not select a lane namespace")
	require.Zero(t, openAIWSLaneIDForAccount(nil, laneService),
		"a missing account must not select a lane namespace")
	require.Zero(t, (*OpenAIGatewayHandler)(nil).openAIWSLaneIDForHandler(account),
		"a nil handler must retain the legacy namespace")
	require.Zero(t, (&OpenAIGatewayHandler{}).openAIWSLaneIDForHandler(account),
		"a handler without a concurrency helper must retain the legacy namespace")
}

func TestOpenAIWSLaneIDForAccountRejectsInvalidLaneID(t *testing.T) {
	account := &service.Account{
		SelectedProxyLane: &service.AccountProxyLane{ID: 0},
	}
	laneService := service.NewConcurrencyService(&openAIWSLaneCacheProbe{})

	require.Zero(t, openAIWSLaneIDForAccount(account, laneService))
}

func TestOpenAIWSAccountMaxConcurrencyPreservesZeroLegacyWaitPlan(t *testing.T) {
	// Hydration projects the selected lane onto Account and therefore changes
	// Account.Concurrency.  An old cache still admits through the account
	// namespace, whose WaitPlan retains the aggregate limit (zero = unlimited).
	lane := &service.AccountProxyLane{ID: 101, Concurrency: 2}
	account := &service.Account{
		ID:                7,
		Concurrency:       2, // request-local lane projection
		ProxyLanes:        []service.AccountProxyLane{*lane},
		SelectedProxyLane: lane,
	}
	h := &OpenAIGatewayHandler{
		concurrencyHelper: NewConcurrencyHelper(
			service.NewConcurrencyService(&openAIWSLegacyCacheProbe{}),
			SSEPingFormatClaude,
			0,
		),
	}
	selection := &service.AccountSelectionResult{
		Account: account,
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      account.ID,
			LaneID:         0, // legacy account namespace during rolling upgrade
			MaxConcurrency: 0, // unlimited is intentional
		},
	}

	require.Zero(t, h.openAIWSAccountMaxConcurrencyForSelection(account, selection),
		"a zero wait-plan limit must not be replaced by the projected lane cap")
}

func TestOpenAIWSAccountMaxConcurrencyRefreshesLanePlan(t *testing.T) {
	oldLane := &service.AccountProxyLane{ID: 101, Concurrency: 2}
	newLane := &service.AccountProxyLane{ID: 101, Concurrency: 5}
	account := &service.Account{
		ID:                7,
		Concurrency:       newLane.Concurrency,
		ProxyLanes:        []service.AccountProxyLane{*newLane},
		SelectedProxyLane: newLane,
	}
	h := &OpenAIGatewayHandler{
		concurrencyHelper: NewConcurrencyHelper(
			service.NewConcurrencyService(&openAIWSLaneCacheProbe{}),
			SSEPingFormatClaude,
			0,
		),
	}
	selection := &service.AccountSelectionResult{
		Account: account,
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      account.ID,
			LaneID:         oldLane.ID,
			MaxConcurrency: oldLane.Concurrency,
		},
	}

	require.Equal(t, newLane.Concurrency,
		h.openAIWSAccountMaxConcurrencyForSelection(account, selection),
		"a lane-capable plan should use the refreshed selected-lane cap")
}

func TestOpenAIWSAccountMaxConcurrencyFallsBackToAccountWithoutWaitPlan(t *testing.T) {
	account := &service.Account{ID: 7, Concurrency: 3}
	h := &OpenAIGatewayHandler{}

	require.Equal(t, 3, h.openAIWSAccountMaxConcurrencyForSelection(account, nil))
	require.Zero(t, h.openAIWSAccountMaxConcurrencyForSelection(nil, nil))
}

func TestOpenAIWSAccountMaxConcurrencyKeepsLaneCapWhenProjectionMissing(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	selection := &service.AccountSelectionResult{
		WaitPlan: &service.AccountWaitPlan{
			AccountID:                  7,
			LaneID:                     101,
			MaxConcurrency:             10,
			AggregateMaxConcurrency:    20,
			AggregateMaxConcurrencySet: true,
		},
		// Composite admission metadata is the parent account ceiling. It must
		// not replace the lane cap when a request-local lane projection is absent.
		AdmissionMaxConcurrency:    20,
		AdmissionMaxConcurrencySet: true,
	}
	require.Equal(t, 10, h.openAIWSAccountMaxConcurrencyForSelection(&service.Account{ID: 7}, selection))
}
