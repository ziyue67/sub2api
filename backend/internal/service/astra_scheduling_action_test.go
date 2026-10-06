package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAstraSchedulingModeRequestScope(t *testing.T) {
	s := config.AstraRoutingSettings{SchedulingMode: "model"}
	a := &Account{Credentials: map[string]any{"model_mapping": map[string]any{"alias": "gpt-6-astra"}}}
	require.True(t, astraSchedulingAppliesToRequest(s, a, "alias", 1))
	require.False(t, astraSchedulingAppliesToRequest(s, a, "gpt-5", 1))
	s.SchedulingMode = "groups"
	s.SchedulingGroupIDs = []int64{2}
	require.True(t, astraSchedulingAppliesToRequest(s, a, "gpt-5", 2))
	require.False(t, astraSchedulingAppliesToRequest(s, a, "gpt-6-astra", 1))
}
func TestAstraModelDisabledDoesNotFallThrough(t *testing.T) {
	for _, mapping := range []map[string]any{nil, {"*": "*"}, {"gpt-6-*": "gpt-6-astra"}} {
		a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"model_mapping": mapping}, Extra: map[string]any{"astra_model_disabled": true}}
		require.False(t, a.IsModelSupported("gpt-6-astra"))
	}
	a := &Account{Credentials: map[string]any{"model_mapping": map[string]any{}}, Extra: map[string]any{"astra_model_disabled": true, "astra_model_empty_mapping": true}}
	require.False(t, a.IsModelSupported("gpt-5"))
	a.Extra["astra_model_empty_mapping"] = false
	a.Extra["astra_model_blocked_keys"] = []any{"alias"}
	require.False(t, a.IsModelSupported("alias"))
	require.True(t, a.IsModelSupported("gpt-5"))
}

type astraModeRepo struct {
	astraSchedulingRepo
	calls int
}

func (r *astraModeRepo) ApplyAstraScheduling(_ context.Context, _ int64, s config.AstraRoutingSettings, ready bool) (AstraSchedulingActionResult, error) {
	r.calls++
	return AstraSchedulingActionResult{Changed: true, Allowed: ready, Action: s.EffectiveSchedulingMode()}, nil
}
func TestAstraModeSyncDoesNotSkipUnchangedAccountFlag(t *testing.T) {
	s := config.AstraRoutingSettings{AccountScheduling: true, SchedulingMode: "model", Revision: "test", CookiePool: config.CodexGatewayPinConfig{Enabled: true, TargetAccountIDs: []int64{300}}}
	cfg := &config.Config{}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return s })
	a := &Account{ID: 300, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: false}
	repo := &astraModeRepo{astraSchedulingRepo: astraSchedulingRepo{accounts: map[int64]*Account{300: a}}}
	svc := &AccountTestService{cfg: cfg, accountRepo: repo, httpUpstream: &astraSetupUpstream{snapshot: AstraGatewayRuntime{Revision: "test"}}}
	svc.syncAstraAccountScheduling(t.Context())
	require.Equal(t, 1, repo.calls)
	require.Empty(t, repo.writes)
	s.AccountScheduling = false
	svc.syncAstraAccountScheduling(t.Context())
	require.Equal(t, 1, repo.calls)
}

func TestAstraModelBlockPreservesSpecificOtherModelWithWildcard(t *testing.T) {
	a := &Account{Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-*": "gpt-6-astra", "gpt-6-sol": "gpt-6-sol"}}, Extra: map[string]any{"astra_model_disabled": true}}
	require.True(t, a.IsModelSupported("gpt-6-sol"))
	require.False(t, a.IsModelSupported("gpt-6-other"))
}
