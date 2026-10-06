package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type astraSetupUpstream struct {
	HTTPUpstream
	prepare  func(context.Context) error
	snapshot AstraGatewayRuntime
}

func (s *astraSetupUpstream) PrepareAstraGateway(ctx context.Context) error              { return s.prepare(ctx) }
func (s *astraSetupUpstream) SetAstraGatewayPreparer(func(context.Context, int64) error) {}
func (s *astraSetupUpstream) AstraGatewaySnapshot(context.Context) AstraGatewayRuntime {
	return s.snapshot
}
func TestAstraAutomaticSetupFailureAndDisable(t *testing.T) {
	cfg := &config.Config{}
	values := config.AstraRoutingSettings{Revision: "one", CookiePool: config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{299}, TargetAccountIDs: []int64{300}}}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return values })
	svc := &AccountTestService{cfg: cfg, httpUpstream: &astraSetupUpstream{prepare: func(context.Context) error { return errors.New("no_qualified_source_route") }}}
	svc.StartAstraAutomaticSetup(values)
	require.Eventually(t, func() bool {
		svc.astraSetupMu.Lock()
		defer svc.astraSetupMu.Unlock()
		return svc.astraSetupStatus.State == "failed"
	}, time.Second, time.Millisecond)
	svc.astraSetupMu.Lock()
	require.Equal(t, "source", svc.astraSetupStatus.Phase)
	require.Equal(t, "no_qualified_source_route", svc.astraSetupStatus.Reason)
	svc.astraSetupMu.Unlock()
	// Complete worker before mutating the test loader's settings.
	svc.astraGatewayActionMu.Lock()
	values.Revision = "two"
	values.CookiePool.Enabled = false
	svc.astraGatewayActionMu.Unlock()
	svc.StartAstraAutomaticSetup(values)
	svc.astraSetupMu.Lock()
	require.Equal(t, "disabled", svc.astraSetupStatus.State)
	svc.astraSetupMu.Unlock()
}

func TestAstraSetupRequiresAllTargetsOnCurrentRoutes(t *testing.T) {
	provider := &astraSetupUpstream{snapshot: AstraGatewayRuntime{Targets: []AstraRouteStatus{{AccountID: 300, State: "waiting"}, {AccountID: 301, State: "ready"}}}}
	id, ready := astraTargetsReady(t.Context(), provider, []int64{300, 301})
	require.False(t, ready)
	require.Equal(t, int64(300), id)
	provider.snapshot.Targets[0].State = "ready"
	_, ready = astraTargetsReady(t.Context(), provider, []int64{300, 301})
	require.True(t, ready)
}

func TestAstraSetupWaitsForConcurrentPreparation(t *testing.T) {
	calls := 0
	provider := &astraSetupUpstream{prepare: func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("preparation_in_progress")
		}
		return nil
	}}
	require.NoError(t, prepareAstraForSetup(t.Context(), provider))
	require.Equal(t, 2, calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, prepareAstraForSetup(ctx, provider), context.Canceled)
	require.Equal(t, 2, calls)
}
