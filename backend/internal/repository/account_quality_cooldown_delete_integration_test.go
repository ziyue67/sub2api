//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func cooldownDeleteSnapshot(t *testing.T, f *qualityBPSFixture) string {
	t.Helper()
	var snapshot string
	require.NoError(t, integrationDB.QueryRow(`SELECT jsonb_build_object(
 'plan',(SELECT to_jsonb(p) FROM scheduled_test_plans p WHERE id=$1),
 'state',(SELECT state FROM account_quality_states WHERE plan_id=$1),
 'account',(SELECT to_jsonb(a) FROM accounts a WHERE id=$2),
 'outbox',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY o.id),'[]'::jsonb) FROM scheduler_outbox o WHERE account_id=$2)
 )::text`, f.plan.ID, f.account).Scan(&snapshot))
	return snapshot
}

func requireCooldownDeleteConflictPreserves(t *testing.T, f *qualityBPSFixture) {
	t.Helper()
	before := cooldownDeleteSnapshot(t, f)
	err := f.plans.Delete(context.Background(), f.plan.ID)
	require.Error(t, err, "owned cooldown delete must be rejected")
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, service.QualityScopeConflictReason, infraerrors.Reason(err))
	require.JSONEq(t, before, cooldownDeleteSnapshot(t, f), "delete must preserve the plan, ownership, account and outbox")
}

func TestQualityCooldownDeletePreservesOwnedState(t *testing.T) {
	for _, layout := range []string{"full", "action", "model_applied", "model_rate_limits", "previous_concurrency", "applied_concurrency", "recovery_target", "manual owner", "other rule owner"} {
		t.Run(layout, func(t *testing.T) {
			f := newQualityCooldownScopeFixture(t)
			if layout == "manual owner" || layout == "other rule owner" {
				reason := "manual"
				if layout == "other rule owner" {
					reason = "quality_rule:999999"
				}
				f.exec(`UPDATE accounts SET extra=jsonb_set(extra,'{model_rate_limits,sol,reason}',to_jsonb($2::text)) WHERE id=$1`, reason)
			} else if layout != "full" {
				// Isolate each durable ownership field from both Action and the
				// live marker so the snapshot guard cannot rely on either one.
				var state map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(cooldownScopeState(t, f)), &state))
				value := state[layout]
				if layout == "recovery_target" {
					value = json.RawMessage(`20`)
				}
				require.NotEmpty(t, value)
				raw, err := json.Marshal(map[string]json.RawMessage{layout: value})
				require.NoError(t, err)
				_, err = integrationDB.Exec(`UPDATE account_quality_states SET state=$2::jsonb WHERE plan_id=$1`, f.plan.ID, string(raw))
				require.NoError(t, err)
				f.exec(`UPDATE accounts SET extra=extra-'model_rate_limits' WHERE id=$1`)
			}
			requireCooldownDeleteConflictPreserves(t, f)
		})
	}
}

func TestQualityCooldownDeleteChecksLiveMarkersWithoutSnapshot(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "empty snapshot"
		if missing {
			name = "missing snapshot"
		}
		t.Run(name, func(t *testing.T) {
			f := newQualityCooldownScopeFixture(t)
			query := `UPDATE account_quality_states SET state='{}'::jsonb WHERE plan_id=$1`
			if missing {
				query = `DELETE FROM account_quality_states WHERE plan_id=$1`
			}
			_, err := integrationDB.Exec(query, f.plan.ID)
			require.NoError(t, err)
			// Simulate an old edit and account remapping: the owned live key is
			// no longer among current targets. Deletion must inspect all keys.
			f.exec(`UPDATE scheduled_test_plans SET pelican_config=jsonb_set(pelican_config,'{quality,remove_models}','["astra"]'::jsonb) WHERE account_id=$1`)
			f.exec(`UPDATE accounts SET credentials='{"model_mapping":{"sol":"other"}}'::jsonb WHERE id=$1`)
			requireCooldownDeleteConflictPreserves(t, f)
			requireCooldownScopeBlocked(t, f)
		})
	}
}

func TestQualityCooldownDeleteAfterRecoveryPreservesExternalRestrictions(t *testing.T) {
	f := newQualityCooldownScopeFixture(t)
	f.exec(`UPDATE accounts SET extra=jsonb_set(extra,'{model_rate_limits,native}','{"reason":"native","rate_limit_reset_at":"2099-01-01T00:00:00Z"}'::jsonb) WHERE id=$1`)
	f.exec(`UPDATE accounts SET extra=jsonb_set(extra,'{model_rate_limits,other-owner}','{"reason":"quality_rule:999999","rate_limit_reset_at":"2099-01-01T00:00:00Z"}'::jsonb) WHERE id=$1`)
	requireCooldownDeleteConflictPreserves(t, f)
	f.plan.QualityModelOutcomes = map[string]string{"sol": "passed", "astra": "passed"}
	require.Equal(t, "restored", f.apply("passed"))
	require.Zero(t, f.count(`SELECT count(*) FROM account_quality_states s JOIN scheduled_test_plans p ON p.id=s.plan_id WHERE p.account_id=$1`))
	require.Equal(t, 1, f.count(`SELECT count(*) FROM accounts WHERE id=$1 AND concurrency=20`))
	extra := f.extra()
	limits := extra["model_rate_limits"].(map[string]any)
	require.NotContains(t, limits, "sol")
	require.Contains(t, limits, "native")
	require.Contains(t, limits, "other-owner")
	require.NoError(t, f.plans.Delete(context.Background(), f.plan.ID))
	require.Zero(t, f.count(`SELECT count(*) FROM scheduled_test_plans WHERE account_id=$1`))
	require.Equal(t, extra, f.extra(), "deleting a recovered rule must preserve external restrictions")
}
