//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Exercise persisted, deadline-expired quality ownership, not a synthetic scope
// snapshot. The native deadline must not turn an edit into implicit recovery.
func newQualityCooldownScopeFixture(t *testing.T) *qualityBPSFixture {
	t.Helper()
	f := newQualityBPSFixture(t, `{"unrelated":"kept"}`, &service.QualityPolicy{
		Action: service.QualityActionRemoveModel, RemoveModels: []string{"sol", "astra"}, AutoRestore: true,
	})
	f.exec(`UPDATE accounts SET concurrency=20 WHERE id=$1`)
	f.plan.QualityModelOutcomes = map[string]string{"sol": "failed", "astra": "passed"}
	require.Equal(t, "models_cooled", f.apply("failed"))
	state := cooldownScopeState(t, f)
	var decoded qualityState
	require.NoError(t, json.Unmarshal([]byte(state), &decoded))
	extra := f.extra()
	entry := extra["model_rate_limits"].(map[string]any)["sol"].(map[string]any)
	entry["rate_limit_reset_at"] = "2020-01-01T00:00:00Z"
	raw, err := json.Marshal(entry)
	require.NoError(t, err)
	decoded.ModelApplied["sol"] = raw
	raw, err = json.Marshal(extra)
	require.NoError(t, err)
	f.exec(`UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, string(raw))
	raw, err = json.Marshal(decoded)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE account_quality_states SET state=$2::jsonb WHERE plan_id=$1`, f.plan.ID, string(raw))
	require.NoError(t, err)
	f.exec(`DELETE FROM scheduler_outbox WHERE account_id=$1`)
	return f
}

func cooldownScopeState(t *testing.T, f *qualityBPSFixture) string {
	t.Helper()
	var raw string
	require.NoError(t, integrationDB.QueryRow(`SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&raw))
	return raw
}

func requireCooldownScopeBlocked(t *testing.T, f *qualityBPSFixture) {
	t.Helper()
	account := &service.Account{Platform: service.PlatformOpenAI, Status: "active", Schedulable: true, Extra: f.extra()}
	require.False(t, account.IsSchedulableForModel("sol"))
	require.True(t, account.IsSchedulableForModel("astra"))
	require.Equal(t, "kept", account.Extra["unrelated"])
	require.Equal(t, 1, f.count(`SELECT count(*) FROM accounts WHERE id=$1 AND concurrency=5`))
}

func TestQualityCooldownOwnedScopeRejectsEdits(t *testing.T) {
	ctx := context.Background()
	for _, preserve := range []bool{false, true} {
		for _, edit := range []string{"remove target", "replace targets", "empty targets", "observe_only", "disable_scheduling", "enable_bps", "isolate_models", "remove quality", "remove config"} {
			t.Run(edit+"/preserve="+strconv.FormatBool(preserve), func(t *testing.T) {
				f := newQualityCooldownScopeFixture(t)
				beforeState, beforeExtra := cooldownScopeState(t, f), f.extra()
				plan, err := f.plans.GetByID(ctx, f.plan.ID)
				require.NoError(t, err)
				plan.PreserveQualityState = preserve
				switch edit {
				case "remove target":
					plan.PelicanConfig.Quality.RemoveModels = []string{"astra"}
				case "replace targets":
					plan.PelicanConfig.Quality.RemoveModels = []string{"other"}
				case "empty targets":
					plan.PelicanConfig.Quality.RemoveModels = nil
				case "remove quality":
					plan.PelicanConfig.Quality = nil
				case "remove config":
					plan.PelicanConfig = nil
				default:
					plan.PelicanConfig.Quality.Action = edit
				}
				_, err = f.plans.Update(ctx, plan)
				require.Error(t, err, "owned cooldown edit must be rejected")
				require.Equal(t, service.QualityScopeConflictReason, infraerrors.Reason(err))
				saved, err := f.plans.GetByID(ctx, f.plan.ID)
				require.NoError(t, err)
				require.Equal(t, service.QualityActionRemoveModel, saved.PelicanConfig.Quality.Action)
				require.Equal(t, []string{"sol", "astra"}, saved.PelicanConfig.Quality.RemoveModels)
				require.True(t, saved.UpdatedAt.Equal(f.plan.UpdatedAt))
				require.JSONEq(t, beforeState, cooldownScopeState(t, f))
				require.Equal(t, beforeExtra, f.extra())
				requireCooldownScopeBlocked(t, f)
				require.Zero(t, f.events())
			})
		}
	}
}

func TestQualityCooldownOrdinaryEditRetainsOwnershipUntilPassingRecovery(t *testing.T) {
	ctx := context.Background()
	f := newQualityCooldownScopeFixture(t)
	before := cooldownScopeState(t, f)
	stale := *f.plan
	plan, err := f.plans.GetByID(ctx, f.plan.ID)
	require.NoError(t, err)
	plan.CronExpression = "*/15 * * * *"
	plan.MaxResults = 42
	plan.Enabled = false
	plan.PelicanConfig.Quality.AutoRestore = false
	// A healthy, unowned target may be removed while the owned target remains.
	plan.PelicanConfig.Quality.RemoveModels = []string{"sol"}
	f.plan, err = f.plans.Update(ctx, plan)
	require.NoError(t, err)
	require.JSONEq(t, before, cooldownScopeState(t, f))
	requireCooldownScopeBlocked(t, f)
	require.Zero(t, f.events())
	action, err := f.plans.ApplyQualityOutcome(ctx, &stale, f.until, "passed")
	require.NoError(t, err)
	require.Equal(t, "stale_run", action)
	f.plan.Enabled = true
	f.plan, err = f.plans.Update(ctx, f.plan)
	require.NoError(t, err)
	f.plan.QualityModelOutcomes = map[string]string{"sol": "passed"}
	require.Equal(t, "passed", f.apply("passed"))
	require.JSONEq(t, before, cooldownScopeState(t, f), "auto_restore=false retains ownership even on pass")
	f.plan.PelicanConfig.Quality.AutoRestore = true
	f.plan, err = f.plans.Update(ctx, f.plan)
	require.NoError(t, err)
	for _, verdict := range []string{"skipped", "inconclusive"} {
		f.plan.QualityModelOutcomes = map[string]string{"sol": verdict}
		f.apply("inconclusive")
		var owned qualityState
		require.NoError(t, json.Unmarshal([]byte(cooldownScopeState(t, f)), &owned))
		require.Equal(t, service.QualityActionRemoveModel, owned.Action)
		require.Contains(t, owned.ModelApplied, "sol")
		require.Contains(t, owned.ModelRateLimits, "sol")
		requireCooldownScopeBlocked(t, f)
	}
	f.plan.QualityModelOutcomes = map[string]string{"sol": "passed"}
	require.Equal(t, "restored", f.apply("passed"))
	require.NotContains(t, f.extra(), "model_rate_limits")
	require.Equal(t, 1, f.count(`SELECT count(*) FROM accounts WHERE id=$1 AND concurrency=20`))
	require.Zero(t, f.count(`SELECT count(*) FROM account_quality_states s JOIN scheduled_test_plans p ON p.id=s.plan_id WHERE p.account_id=$1`))
	f.plan.PelicanConfig.Quality.Action = service.QualityActionObserveOnly
	_, err = f.plans.Update(ctx, f.plan)
	require.NoError(t, err, "changing action is safe only after confirmed recovery")
}

func TestQualityCooldownExternalOwnerSurvivesEditsAndRecovery(t *testing.T) {
	for _, reason := range []string{"manual", "quality_rule:999999"} {
		t.Run(reason, func(t *testing.T) {
			f := newQualityCooldownScopeFixture(t)
			before := cooldownScopeState(t, f)
			extra := f.extra()
			entry := extra["model_rate_limits"].(map[string]any)["sol"].(map[string]any)
			entry["reason"] = reason
			entry["rate_limit_reset_at"] = "2099-01-01T00:00:00Z"
			raw, err := json.Marshal(extra)
			require.NoError(t, err)
			f.exec(`UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, string(raw))
			f.plan.MaxResults++
			f.plan, err = f.plans.Update(context.Background(), f.plan)
			require.NoError(t, err)
			require.JSONEq(t, before, cooldownScopeState(t, f))
			for _, verdict := range []string{"failed", "passed"} {
				f.plan.QualityModelOutcomes = map[string]string{"sol": verdict, "astra": "passed"}
				expected := "action_conflict"
				if verdict == "passed" {
					expected = "restore_conflict"
				}
				require.Equal(t, expected, f.apply(verdict))
				require.Equal(t, extra, f.extra())
				require.JSONEq(t, before, cooldownScopeState(t, f))
			}
			require.Zero(t, f.events())
		})
	}
}

func TestQualityCooldownMappingChangeCannotStrandOwnedTarget(t *testing.T) {
	f := newQualityCooldownScopeFixture(t)
	beforeState, beforeExtra := cooldownScopeState(t, f), f.extra()
	f.exec(`UPDATE accounts SET credentials='{"model_mapping":{"sol":"other"}}'::jsonb WHERE id=$1`)
	f.plan.MaxResults++
	_, err := f.plans.Update(context.Background(), f.plan)
	require.Error(t, err, "owned cooldown edit must be rejected after mapping changes")
	f.plan.QualityModelOutcomes = map[string]string{"sol": "passed", "astra": "passed"}
	require.Equal(t, "action_conflict", f.apply("passed"), "a pass for another upstream cannot recover the owned key")
	require.JSONEq(t, beforeState, cooldownScopeState(t, f))
	require.Equal(t, beforeExtra, f.extra())
	require.Zero(t, f.events())
	// Selecting the actual owned upstream restores a valid recovery path.
	// sol still maps to other; include the owned key via an unambiguous alias.
	f.exec(`UPDATE accounts SET credentials='{"model_mapping":{"sol":"other","owned-alias":"sol"}}'::jsonb WHERE id=$1`)
	f.plan.PelicanConfig.Quality.RemoveModels = []string{"owned-alias", "astra"}
	f.plan, err = f.plans.Update(context.Background(), f.plan)
	require.NoError(t, err)
	f.plan.QualityModelOutcomes = map[string]string{"owned-alias": "passed", "astra": "passed"}
	require.Equal(t, "restored", f.apply("passed"))
	require.NotContains(t, f.extra(), "model_rate_limits")
}

func TestQualityCooldownChangedActionOutcomeRetainsOwnership(t *testing.T) {
	// Old binaries could already have saved these incompatible policies. A new
	// probe must fail closed rather than delete ownership or invent a recovery.
	for _, action := range []string{service.QualityActionObserveOnly, "disable_scheduling", service.QualityActionEnableBPS, service.QualityActionIsolateModels} {
		t.Run(action, func(t *testing.T) {
			f := newQualityCooldownScopeFixture(t)
			beforeState, beforeExtra := cooldownScopeState(t, f), f.extra()
			f.exec(`UPDATE scheduled_test_plans SET pelican_config=jsonb_set(pelican_config,'{quality,action}',to_jsonb($2::text)),running_until=NULL,updated_at=clock_timestamp() WHERE account_id=$1`, action)
			f.claim()
			for _, verdict := range []string{"failed", "passed", "inconclusive"} {
				f.plan.QualityModelOutcomes = map[string]string{"sol": verdict, "astra": "passed"}
				require.Equal(t, "action_conflict", f.apply(verdict))
				require.JSONEq(t, beforeState, cooldownScopeState(t, f))
				require.Equal(t, beforeExtra, f.extra())
				requireCooldownScopeBlocked(t, f)
			}
			require.Zero(t, f.events())
		})
	}
}
