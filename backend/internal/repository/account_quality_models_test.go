package repository

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestQualityModelCooldownLifecycle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	account := &service.Account{Type: service.AccountTypeOAuth, Concurrency: 20, Extra: map[string]any{}, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}}
	state := qualityState{}
	action, err := transitionQualityModels(account, &state, 7, []string{"alias"}, now.Add(30*time.Minute), "failed", true, now)
	require.NoError(t, err)
	require.Equal(t, "models_cooled", action)
	require.Equal(t, 5, account.Concurrency)
	require.False(t, account.IsSchedulableForModel("alias"))
	require.Equal(t, 20, *state.PreviousConcurrency)
	action, err = transitionQualityModels(account, &state, 7, []string{"alias"}, now.Add(time.Hour), "failed", true, now)
	require.NoError(t, err)
	require.Equal(t, "model_cooldown_refreshed", action)
	require.Equal(t, 20, *state.PreviousConcurrency)
	action, err = transitionQualityModels(account, &state, 7, []string{"alias"}, now.Add(time.Hour), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, "restored", action)
	require.Equal(t, 20, account.Concurrency)
	require.Empty(t, account.Extra["model_rate_limits"])
}

func TestQualityModelCooldownPreservesNativeAndManualChanges(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	old := map[string]any{"reason": "upstream_429", "rate_limit_reset_at": now.Add(time.Hour).Format(time.RFC3339)}
	account := &service.Account{Type: service.AccountTypeOAuth, Concurrency: 20, Extra: map[string]any{"model_rate_limits": map[string]any{"target": old, "other": old}}}
	state := qualityState{}
	_, err := transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "failed", true, now)
	require.NoError(t, err)
	_, err = transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "passed", true, now)
	require.NoError(t, err)
	limits, ok := account.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, old, limits["target"])
	require.Equal(t, old, limits["other"])
	state = qualityState{}
	_, err = transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "failed", true, now)
	require.NoError(t, err)
	account.Concurrency = 9
	limits, ok = account.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	limits["target"] = old
	action, err := transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, "restore_conflict", action)
	require.Equal(t, 9, account.Concurrency)
	require.Equal(t, old, limits["target"])
	_, err = json.Marshal(state)
	require.NoError(t, err)
}

func TestQualityModelInconclusiveRetainsOwnedRestriction(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a := &service.Account{Type: service.AccountTypeOAuth, Concurrency: 12, Extra: map[string]any{}}
	state := qualityState{RecoveryConcurrency: 3, NativeRecovery: true}
	_, err := transitionQualityModels(a, &state, 7, []string{"target"}, now.Add(time.Minute), "failed", true, now)
	require.NoError(t, err)
	_, err = transitionQualityModels(a, &state, 7, []string{"target"}, now.Add(time.Hour), "inconclusive", true, now)
	require.NoError(t, err)
	require.Equal(t, 3, a.Concurrency)
	require.Equal(t, 12, *state.PreviousConcurrency)
	limits, ok := a.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, now.Add(time.Hour), cooldownEntryUntil(limits["target"]))
	_, err = transitionQualityModels(a, &state, 7, []string{"target"}, now.Add(time.Hour), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, 3, a.Concurrency)
	require.Equal(t, 12, *state.RecoveryTarget)
}

func TestQualityModelsIndependentCooldownAndRecovery(t *testing.T) {
	for _, pair := range [][2]string{{"gpt-6-astra", "gpt-6.1-sol"}, {"gpt-6.1-sol", "gpt-6-astra"}} {
		t.Run(pair[0], func(t *testing.T) {
			primary, peer := pair[0], pair[1]

			now := time.Now().UTC().Truncate(time.Second)
			a := &service.Account{Platform: service.PlatformOpenAI, Status: "active", Schedulable: true, Type: service.AccountTypeOAuth, Concurrency: 20, Extra: map[string]any{}}
			state := qualityState{RecoveryConcurrency: 4, NativeRecovery: true}
			_, err := transitionQualityModels(a, &state, 7, []string{primary}, now.Add(time.Hour), "failed", true, now)
			require.NoError(t, err)
			require.False(t, a.IsSchedulableForModel(primary))
			require.True(t, a.IsSchedulableForModel(peer))
			_, err = transitionQualityModels(a, &state, 7, []string{peer}, now.Add(time.Hour), "inconclusive", true, now)
			require.NoError(t, err)
			require.True(t, a.IsSchedulableForModel(peer))
			_, err = transitionQualityModels(a, &state, 7, []string{peer}, now.Add(time.Hour), "failed", true, now)
			require.NoError(t, err)
			require.False(t, a.IsSchedulableForModel(peer))
			_, err = transitionQualityModels(a, &state, 7, []string{primary}, now.Add(time.Hour), "passed", true, now)
			require.NoError(t, err)
			require.True(t, a.IsSchedulableForModel(primary))
			require.False(t, a.IsSchedulableForModel(peer))
			require.Nil(t, state.RecoveryTarget)
			_, err = transitionQualityModels(a, &state, 7, []string{peer}, now.Add(time.Hour), "passed", true, now)
			require.NoError(t, err)
			require.True(t, a.IsSchedulableForModel(peer))
			require.Equal(t, 20, *state.RecoveryTarget)
		})
	}
}
func TestQuality5xxPendingOnlyLowersConcurrency(t *testing.T) {
	now := time.Now().UTC()
	a := &service.Account{Platform: service.PlatformOpenAI, Status: "active", Schedulable: true, Type: service.AccountTypeOAuth, Concurrency: 20, Extra: map[string]any{}}
	state := qualityState{RecoveryConcurrency: 4, NativeRecovery: true}
	action, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra", "gpt-6.1-sol"}, now.Add(time.Hour), "pending", true, now)
	require.NoError(t, err)
	require.Equal(t, "probe_pending", action)
	require.Equal(t, 4, a.Concurrency)
	require.True(t, a.IsSchedulableForModel("gpt-6-astra"))
	require.True(t, a.IsSchedulableForModel("gpt-6.1-sol"))
}

func TestQualityPassingModelDoesNotUnlockRampWithUnknownPeer(t *testing.T) {
	now := time.Now().UTC()
	a := &service.Account{Platform: service.PlatformOpenAI, Status: "active", Schedulable: true, Type: service.AccountTypeOAuth, Concurrency: 20, Extra: map[string]any{}}
	state := qualityState{RecoveryConcurrency: 4, NativeRecovery: true}
	_, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra", "gpt-6.1-sol"}, now.Add(time.Hour), "pending", true, now)
	require.NoError(t, err)
	_, err = transitionQualityModelsScoped(a, &state, 7, []string{"gpt-6-astra"}, now.Add(time.Hour), "passed", true, now, true)
	require.NoError(t, err)
	require.Nil(t, state.RecoveryTarget)
	require.Equal(t, 4, a.Concurrency)
	require.Equal(t, 20, *state.PreviousConcurrency)
}

func TestQualityModelRecoveryAfterExpiredEntriesWereCleared(t *testing.T) {
	for _, outcome := range []string{"passed", "failed", "inconclusive"} {
		t.Run(outcome, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			a := &service.Account{Type: service.AccountTypeAPIKey, Concurrency: 15, Extra: map[string]any{}}
			state := qualityState{}
			_, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-6.1-sol"}, now.Add(-time.Minute), "failed", true, now.Add(-time.Hour))
			require.NoError(t, err)
			delete(a.Extra, "model_rate_limits")
			action, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra"}, now.Add(time.Hour), outcome, true, now)
			require.NoError(t, err)
			switch outcome {
			case "passed":
				require.Equal(t, "restored", action)
				require.Empty(t, state.Action)
			case "failed":
				require.Equal(t, "model_cooldown_refreshed", action)
				require.Len(t, state.ModelRateLimits, 1)
				require.Contains(t, state.ModelRateLimits, "gpt-6-astra")
			case "inconclusive":
				require.Equal(t, "inconclusive", action)
				require.Empty(t, state.ModelRateLimits)
				require.NotContains(t, a.Extra, "model_rate_limits")
			}
			require.Equal(t, 15, a.Concurrency)
		})
	}
}

func TestQualityModelRecoveryRemovesOnlyExpiredOwnedEntries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a := &service.Account{Type: service.AccountTypeAPIKey, Extra: map[string]any{}}
	state := qualityState{}
	_, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-6.1-sol"}, now.Add(-time.Minute), "failed", true, now.Add(-time.Hour))
	require.NoError(t, err)
	limits, ok := a.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	manual := map[string]any{"reason": "upstream_429", "rate_limit_reset_at": now.Add(time.Hour).Format(time.RFC3339)}
	limits["gpt-5.6-sol"] = manual
	action, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra"}, now.Add(time.Hour), "passed", true, now)
	require.NoError(t, err)
	require.NotEqual(t, "restore_conflict", action)
	require.NotContains(t, state.ModelRateLimits, "gpt-6-astra")
	require.Contains(t, state.ModelRateLimits, "gpt-5.6-sol")
	require.NotContains(t, state.ModelRateLimits, "gpt-6.1-sol")
	require.Equal(t, manual, limits["gpt-5.6-sol"])
	require.NotContains(t, limits, "gpt-6-astra")
	require.NotContains(t, limits, "gpt-6.1-sol")
}

func TestQualityModelActiveEntryDeletionStillConflicts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a := &service.Account{Type: service.AccountTypeAPIKey, Extra: map[string]any{}}
	state := qualityState{}
	_, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra"}, now.Add(time.Hour), "failed", true, now)
	require.NoError(t, err)
	delete(a.Extra, "model_rate_limits")
	action, err := transitionQualityModels(a, &state, 7, []string{"gpt-6-astra"}, now.Add(time.Hour), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, "restore_conflict", action)
	require.Contains(t, state.ModelRateLimits, "gpt-6-astra")
}
