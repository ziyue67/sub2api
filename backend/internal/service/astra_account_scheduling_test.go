package service

import (
	"context"
	"errors"
	"go.uber.org/zap/zapcore"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type astraSchedulingRepo struct {
	AccountRepository
	accounts map[int64]*Account
	writes   []bool
	fail     bool
}

func (r *astraSchedulingRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.accounts[id], nil
}
func (r *astraSchedulingRepo) SetSchedulable(_ context.Context, id int64, enabled bool) error {
	r.writes = append(r.writes, enabled)
	if r.fail {
		return errors.New("offline")
	}
	r.accounts[id].Schedulable = enabled
	return nil
}
func TestAstraAccountSchedulingIgnoresProbeState(t *testing.T) {
	for _, mode := range []string{"account", "model", "groups"} {
		t.Run(mode, func(t *testing.T) {
			settings := config.AstraRoutingSettings{AccountScheduling: true, SchedulingMode: mode, Revision: "one", CookiePool: config.CodexGatewayPinConfig{Enabled: true, TargetAccountIDs: []int64{300}}}
			cfg := &config.Config{}
			cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
			target := &Account{ID: 300, Schedulable: true, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive}
			repo := &astraModeRepo{astraSchedulingRepo: astraSchedulingRepo{accounts: map[int64]*Account{300: target}}}
			upstream := &astraSetupUpstream{}
			svc := &AccountTestService{cfg: cfg, accountRepo: repo, httpUpstream: upstream}
			for _, reason := range []string{"target_not_verified", "source_probe_cooldown", "target_probe_failed", "target_probe_degraded", "route_expired", "target_probe_passed"} {
				upstream.snapshot = AstraGatewayRuntime{Revision: "one", Targets: []AstraRouteStatus{{AccountID: 300, Reason: reason}}}
				svc.syncAstraAccountScheduling(t.Context())
				require.True(t, target.Schedulable)
				target.Schedulable = false
				svc.syncAstraAccountScheduling(t.Context())
				require.False(t, target.Schedulable)
				target.Schedulable = true
			}
			require.Zero(t, repo.calls)
			require.Empty(t, repo.writes)
		})
	}
}

func TestAstraSchedulingTogglePreservesRevisionAndSkipsPreparation(t *testing.T) {
	repo := &astraSettingsRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{}}}
	cfg := &config.Config{}
	svc := NewSettingService(repo, cfg)
	settings, err := svc.SetAstraRouting(t.Context(), config.AstraRoutingSettings{CookiePool: config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{299}, TargetAccountIDs: []int64{300}}})
	require.NoError(t, err)
	svc.SetAstraRoutingOnSaved(func(config.AstraRoutingSettings) { t.Fatal("toggle must not trigger model preparation") })
	revision := settings.Revision
	for _, enabled := range []bool{true, false} {
		settings.AccountScheduling = enabled
		settings, err = svc.SetAstraRouting(t.Context(), settings)
		require.NoError(t, err)
		require.Equal(t, revision, settings.Revision)
		require.Equal(t, enabled, cfg.AstraRouting(t.Context()).AccountScheduling)
	}
}

func TestAstraSchedulingStaleSaveAndShutdown(t *testing.T) {
	settings := config.AstraRoutingSettings{AccountScheduling: true, Revision: "one", CookiePool: config.CodexGatewayPinConfig{Enabled: true, TargetAccountIDs: []int64{300}}}
	cfg := &config.Config{}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	newer := settings
	newer.AccountScheduling = false
	repo := &astraSchedulingRepo{accounts: map[int64]*Account{300: {ID: 300, Schedulable: true}}}
	svc := &AccountTestService{cfg: cfg, accountRepo: repo, settingService: &SettingService{astraRoutingCache: &newer}}
	svc.syncAstraAccountScheduling(t.Context())
	require.Empty(t, repo.writes, "off save supersedes stale work")
	stop := svc.startAstraAccountScheduling()
	stop()
	stop() // Idempotent lifecycle cleanup.
	require.Empty(t, repo.writes)
}

func TestAstraSchedulingLogReasonsAndRedaction(t *testing.T) {
	now := time.Now()
	expiry := now.Add(time.Minute)
	settings := config.AstraRoutingSettings{Revision: "current", CookiePool: config.CodexGatewayPinConfig{Enabled: true}}
	account := &Account{ID: 300, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "secret"}}
	snapshot := AstraGatewayRuntime{Revision: "current", Targets: []AstraRouteStatus{{AccountID: 300, State: "ready", Reason: "target_probe_passed", Gateway: "chat.gateway.unified-95.api.openai.com", ExpiresAt: &expiry}}}
	fields := func(ready bool) map[string]any {
		enc := zapcore.NewMapObjectEncoder()
		for _, field := range astraSchedulingLogFields(settings, snapshot, account, ready, now) {
			field.AddTo(enc)
		}
		require.NotContains(t, enc.Fields, "credentials")
		require.NotContains(t, enc.Fields, "cookie")
		return enc.Fields
	}
	require.Equal(t, "target_probe_passed", fields(true)["reason"])
	require.Equal(t, int64(60), fields(true)["route_remaining_seconds"])
	expiry = now.Add(-time.Second)
	require.Equal(t, "route_expired", fields(false)["reason"])
	require.Equal(t, int64(0), fields(false)["route_remaining_seconds"])
	snapshot.Revision = "old"
	require.Equal(t, "configuration_changed", fields(false)["reason"])
	settings.CookiePool.Enabled = false
	require.Equal(t, "cookie_pool_disabled", fields(false)["reason"])
}

func (r *astraSchedulingRepo) ListByPlatform(_ context.Context, _ string) ([]Account, error) {
	var accounts []Account
	for _, a := range r.accounts {
		accounts = append(accounts, *a)
	}
	return accounts, nil
}
func TestTicketPoolSchedulingSelfAndReuseWithoutCookiePool(t *testing.T) {
	for _, mode := range []string{"account", "model", "groups"} {
		for _, acquisition := range []string{"self", "reuse"} {
			t.Run(mode+"/"+acquisition, func(t *testing.T) {
				gateway, _, seed := unifiedAstraFixture(t)
				settings := config.AstraRoutingSettings{TicketPoolScheduling: true, SchedulingMode: mode, SchedulingGroupIDs: []int64{9}, Revision: "pool"}
				gateway.cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
				a := ticketTestAccount(1)
				a.Status = StatusActive
				a.Schedulable = true
				repo := &poolSchedulingTestRepo{astraSchedulingRepo: astraSchedulingRepo{accounts: map[int64]*Account{1: a}}}
				gateway.accountRepo = repo
				ticket := *seed
				ticket.Acquisition = acquisition
				ticket.Identity = ticketIdentity(a)
				gateway.openaiCodexTickets.Store(openAICodexTicketKey(1, "gpt-6-astra"), &ticket)
				svc := &AccountTestService{cfg: gateway.cfg, accountRepo: repo, openaiGatewayService: gateway}
				svc.syncAstraAccountScheduling(t.Context())
				require.Equal(t, []bool{true}, repo.ready)
				require.NoError(t, gateway.checkAstraSchedulingRoute(t.Context(), a))
				ticket.ExpiresAt = time.Now().Add(-time.Second)
				gateway.openaiCodexTickets.Store(openAICodexTicketKey(1, "gpt-6-astra"), &ticket)
				svc.syncAstraAccountScheduling(t.Context())
				require.Equal(t, []bool{true, false}, repo.ready)
				require.NoError(t, gateway.checkAstraSchedulingRoute(t.Context(), a), "expired tickets must not synthesize a route 503")
				settings.TicketPoolScheduling = false
				svc.syncAstraAccountScheduling(t.Context())
				require.Len(t, repo.ready, 2, "disabling ticket scheduling preserves account state")
				settings.TicketPoolScheduling = true
				a.Extra = map[string]any{OpenAICodexSkipHarvestExtraKey: true}
				svc.syncAstraAccountScheduling(t.Context())
				require.Len(t, repo.ready, 2, "manual skip-harvest remains outside pool control")
			})
		}
	}
}

type poolSchedulingTestRepo struct {
	astraSchedulingRepo
	ready []bool
}

func (r *poolSchedulingTestRepo) ApplyAstraScheduling(_ context.Context, _ int64, settings config.AstraRoutingSettings, ready bool) (AstraSchedulingActionResult, error) {
	r.ready = append(r.ready, ready)
	return AstraSchedulingActionResult{Changed: true, Allowed: ready, Action: settings.EffectiveSchedulingMode()}, nil
}
