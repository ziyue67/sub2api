//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func legacyQualityActionFixture(t *testing.T, action string) (*qualityBPSFixture, int64, int64) {
	t.Helper()
	f, group, other := newQualityIsolationFixture(t)
	f.plan.PelicanConfig.Quality.Action = action
	f.plan.PelicanConfig.Quality.AutoRestore = false
	plan, err := f.plans.Update(context.Background(), f.plan)
	require.NoError(t, err)
	f.plan = plan
	want := "groups_removed"
	if action == "disable_scheduling" {
		want = "scheduling_disabled"
	}
	require.Equal(t, want, f.apply("failed"))
	return f, group, other
}

func TestQualityIsolationSwitchRestoresLegacyAccountActionAtomically(t *testing.T) {
	ctx := context.Background()
	for _, action := range []string{"remove_groups", "disable_scheduling"} {
		t.Run(action, func(t *testing.T) {
			f, group, other := legacyQualityActionFixture(t, action)
			// Independent restrictions and settings must survive the policy change.
			f.exec(`UPDATE accounts SET extra='{"openai_excel_bps":true,"model_rate_limits":{"native":{"reason":"native"}}}'::jsonb WHERE id=$1`)
			f.exec(`DELETE FROM scheduler_outbox WHERE account_id=$1`)
			var originalCreated string
			if action == "remove_groups" {
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state->'removed'->0->>'created_at' FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&originalCreated))
			}
			stale := *f.plan
			plan, err := f.plans.GetByID(ctx, f.plan.ID)
			require.NoError(t, err)
			plan.PelicanConfig.Quality.Action = service.QualityActionIsolateModels
			// Restore the old targets, even when the new policy selects another group.
			plan.PelicanConfig.Quality.RemoveGroupIDs = []int64{other}
			updated, err := f.plans.Update(ctx, plan)
			require.NoError(t, err)
			require.Equal(t, service.QualityActionIsolateModels, updated.PelicanConfig.Quality.Action)
			require.False(t, updated.PelicanConfig.Quality.AutoRestore)
			var remainingStates int
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM account_quality_states WHERE plan_id=$1`, plan.ID).Scan(&remainingStates))
			require.Zero(t, remainingStates)
			require.Equal(t, 2, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
			var priority, concurrency int
			var allowed string
			var schedulable bool
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT priority,allowed_models::text FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.account, group).Scan(&priority, &allowed))
			require.Equal(t, 7, priority)
			require.JSONEq(t, `["sol","astra"]`, allowed)
			if action == "remove_groups" {
				var sameCreated bool
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT created_at=$3::timestamptz FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.account, group, originalCreated).Scan(&sameCreated))
				require.True(t, sameCreated)
			}
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT schedulable,concurrency FROM accounts WHERE id=$1`, f.account).Scan(&schedulable, &concurrency))
			require.True(t, schedulable)
			require.Equal(t, 20, concurrency)
			require.Equal(t, true, f.extra()["openai_excel_bps"])
			require.Contains(t, f.extra()["model_rate_limits"], "native")
			require.NotContains(t, f.extra(), service.QualityModelIsolationKey, "saving must not replay historical failures")
			require.Positive(t, f.count(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1 AND payload->'group_ids' @> '[`+qualityIsolationID(group)+`]'::jsonb`))
			got, err := f.plans.ApplyQualityOutcome(ctx, &stale, f.until, "failed")
			require.NoError(t, err)
			require.Equal(t, "stale_run", got)
			f.plan = updated
			require.Equal(t, "models_isolated", isolationRound(f, "failed", "passed"))
			require.Equal(t, 2, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
		})
	}
}

func TestQualityIsolationSwitchConflictsPreserveOldPolicyAndOwnership(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, action, mutation, message string
	}{
		{"manual membership", "remove_groups", `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,99)`, "人工修改"},
		{"deleted group", "remove_groups", `UPDATE groups SET deleted_at=NOW() WHERE id=$2 AND $1::bigint>0`, "原分组已删除"},
		{"inactive account", "disable_scheduling", `UPDATE accounts SET status='error' WHERE id=$1 AND $2::bigint>0`, "账号不是正常状态"},
		{"expired account", "disable_scheduling", `UPDATE accounts SET expires_at=NOW()-interval '1 hour' WHERE id=$1 AND $2::bigint>0`, "账号已过期"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, group, _ := legacyQualityActionFixture(t, tc.action)
			f.exec(tc.mutation, group)
			var before string
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&before))
			f.exec(`DELETE FROM scheduler_outbox WHERE account_id=$1`)
			plan, err := f.plans.GetByID(ctx, f.plan.ID)
			require.NoError(t, err)
			plan.PelicanConfig.Quality.Action = service.QualityActionIsolateModels
			_, err = f.plans.Update(ctx, plan)
			require.ErrorContains(t, err, tc.message)
			saved, err := f.plans.GetByID(ctx, f.plan.ID)
			require.NoError(t, err)
			require.Equal(t, tc.action, saved.PelicanConfig.Quality.Action)
			require.True(t, saved.UpdatedAt.Equal(f.plan.UpdatedAt))
			var after string
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&after))
			require.JSONEq(t, before, after)
			require.Zero(t, f.count(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`))
			if tc.action == "disable_scheduling" {
				require.Equal(t, 1, f.count(`SELECT count(*) FROM accounts WHERE id=$1 AND NOT schedulable`))
			} else if tc.name == "manual membership" {
				require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1 AND priority=99`))
			} else {
				require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
			}
		})
	}
}
