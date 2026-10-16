package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityPersistentIsolationSharesTransitionWithoutNativeCooldownOrConcurrency(t *testing.T) {
	now := time.Now()
	native := map[string]any{"rate_limit_reset_at": now.Add(time.Hour).UTC().Format(time.RFC3339)}
	a := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: "active", Schedulable: true, Concurrency: 20,
		Credentials: map[string]any{"model_mapping": map[string]any{"sol-alias": "sol"}}, Extra: map[string]any{"model_rate_limits": map[string]any{"sol": native}}}
	state := &qualityState{PersistentModels: true}
	actions, action, err := transitionQualityModelRound(a, state, 7, []string{"sol-alias", "astra"}, map[string]string{"sol-alias": "failed", "astra": "passed"}, time.Time{}, true, now, true)
	require.NoError(t, err)
	require.Equal(t, "models_cooled", action)
	require.Equal(t, "passed", actions["astra"])
	require.True(t, a.IsQualityModelIsolated("sol-alias"))
	require.True(t, a.IsQualityModelIsolated("sol"))
	require.True(t, a.IsSchedulableForModel("astra"))
	require.Equal(t, 20, a.Concurrency)
	nativeLimits, ok := a.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, native, nativeLimits["sol"])
	// Advancing wall time must not clear a persistent restriction.
	pruneExpiredQualityModels(a, state, now.Add(365*24*time.Hour))
	require.True(t, a.IsQualityModelIsolated("sol"))
	_, _, err = transitionQualityModelRound(a, state, 7, []string{"sol-alias"}, map[string]string{"sol-alias": "inconclusive"}, time.Time{}, true, now, true)
	require.NoError(t, err)
	require.True(t, a.IsQualityModelIsolated("sol"))
	_, _, err = transitionQualityModelRound(a, state, 7, []string{"sol-alias"}, map[string]string{"sol-alias": "passed"}, time.Time{}, false, now, true)
	require.NoError(t, err)
	require.True(t, a.IsQualityModelIsolated("sol"), "automatic restoration remains opt-in")
	_, _, err = transitionQualityModelRound(a, state, 7, []string{"sol-alias"}, map[string]string{"sol-alias": "passed"}, time.Time{}, true, now, true)
	require.NoError(t, err)
	require.False(t, a.IsQualityModelIsolated("sol"))
	nativeLimits, ok = a.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, native, nativeLimits["sol"], "quality recovery must preserve native cooldowns")
}

func TestQualityModelAliasesCannotUndoFailedOrUnknownPeer(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		for _, peer := range []string{"failed", "inconclusive", "skipped"} {
			a := &service.Account{Credentials: map[string]any{"model_mapping": map[string]any{"alias": "upstream"}}}
			state := &qualityState{PersistentModels: persistent}
			_, err := transitionQualityModels(a, state, 7, []string{"alias"}, time.Now().Add(time.Hour), "failed", true, time.Now())
			require.NoError(t, err)
			_, _, err = transitionQualityModelRound(a, state, 7, []string{"alias", "upstream"}, map[string]string{"alias": "passed", "upstream": peer}, time.Now().Add(time.Hour), true, time.Now(), true)
			require.NoError(t, err)
			entries, ok := a.Extra[qualityModelStorage(state)].(map[string]any)
			require.True(t, ok)
			require.Contains(t, entries, "upstream", peer)
		}
	}
}

func TestQualityIsolationSchedulerProjectionAndManualConflict(t *testing.T) {
	a := &service.Account{Platform: service.PlatformOpenAI, Status: "active", Schedulable: true}
	state := &qualityState{PersistentModels: true}
	_, err := transitionQualityModels(a, state, 9, []string{"sol"}, time.Time{}, "failed", true, time.Now())
	require.NoError(t, err)
	a.Extra = filterSchedulerExtra(a.Extra)
	require.False(t, a.IsSchedulableForModel("sol"), "candidate snapshots must retain persistent blocks")
	require.True(t, a.IsSchedulableForModel("astra"))
	entries, ok := a.Extra[service.QualityModelIsolationKey].(map[string]any)
	require.True(t, ok)
	entries["sol"] = map[string]any{"reason": "manual"}
	action, err := transitionQualityModels(a, state, 9, []string{"sol"}, time.Time{}, "passed", true, time.Now())
	require.NoError(t, err)
	require.Equal(t, "restore_conflict", action)
	entry, ok := entries["sol"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "manual", entry["reason"])
}
