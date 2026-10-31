//go:build integration

package repository

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityCooldownDeadlinePersistedRecovery(t *testing.T) {
	for _, outcome := range []string{"failed", "inconclusive", "skipped", "passed"} {
		t.Run(outcome, func(t *testing.T) {
			models := []string{"gpt-6.1-terra", "gpt-5.6-terra", "gpt-6-luna", "gpt-6.1-sol", "gpt-6-astra", "gpt-5.6-sol"}
			f := newQualityBPSFixture(t, `{"unrelated":"kept"}`, &service.QualityPolicy{Action: service.QualityActionRemoveModel, RemoveModels: models, AutoRestore: true})
			require.Equal(t, "models_cooled", f.apply("failed"))
			var raw []byte
			require.NoError(t, integrationDB.QueryRow(`SELECT state FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&raw))
			var state qualityState
			require.NoError(t, json.Unmarshal(raw, &state))
			extra := f.extra()
			limits, ok := extra["model_rate_limits"].(map[string]any)
			require.True(t, ok)
			// Advance beyond the scheduled check without sleeping: preserve exact
			// ownership while moving both stored copies of the deadline into the past.
			for _, model := range models {
				entry, ok := limits[model].(map[string]any)
				require.True(t, ok)
				entry["rate_limit_reset_at"] = "2020-01-01T00:00:00Z"
				encoded, err := json.Marshal(entry)
				require.NoError(t, err)
				state.ModelApplied[model] = encoded
			}
			encoded, err := json.Marshal(extra)
			require.NoError(t, err)
			f.exec(`UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, string(encoded))
			encoded, err = json.Marshal(state)
			require.NoError(t, err)
			_, err = integrationDB.Exec(`UPDATE account_quality_states SET state=$2::jsonb WHERE plan_id=$1`, f.plan.ID, string(encoded))
			require.NoError(t, err)
			account := &service.Account{Platform: service.PlatformOpenAI, Status: "active", Schedulable: true, Extra: f.extra()}
			for _, model := range models {
				require.False(t, account.IsSchedulableForModel(model), "pending probe must not reopen "+model)
			}
			f.plan.QualityModelOutcomes = map[string]string{}
			for _, model := range models {
				f.plan.QualityModelOutcomes[model] = outcome
			}
			require.NotContains(t, f.apply(outcome), "conflict")
			account.Extra = f.extra()
			for _, model := range models {
				require.Equal(t, outcome == "passed", account.IsSchedulableForModel(model), model)
			}
			require.True(t, account.IsSchedulableForModel("other-model"))
			require.Equal(t, "kept", account.Extra["unrelated"])
		})
	}
}
