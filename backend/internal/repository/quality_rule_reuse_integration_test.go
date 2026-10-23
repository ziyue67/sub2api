//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityReusePreservesTargetOwnershipWithoutImplicitRestoration(t *testing.T) {
	ctx := context.Background()
	for _, action := range []string{"remove_groups", "disable_scheduling"} {
		t.Run(action, func(t *testing.T) {
			f, _, _ := legacyQualityActionFixture(t, action)
			f.exec(`DELETE FROM scheduler_outbox WHERE account_id=$1`)
			var beforeState, beforeAccount string
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&beforeState))
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT row_to_json(a)::text FROM accounts a WHERE id=$1`, f.account).Scan(&beforeAccount))
			groupCount := f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`)
			plan, err := f.plans.GetByID(ctx, f.plan.ID)
			require.NoError(t, err)
			plan.PreserveQualityState = true
			plan.PelicanConfig.Quality.Action = service.QualityActionIsolateModels
			_, err = f.plans.Update(ctx, plan)
			require.ErrorContains(t, err, "restore this rule's previous account action")
			persisted, err := f.plans.GetByID(ctx, plan.ID)
			require.NoError(t, err)
			require.Equal(t, action, persisted.PelicanConfig.Quality.Action, "unsafe switch rolls back")

			persisted.PreserveQualityState = true
			persisted.PelicanConfig.Quality.Action = service.QualityActionObserveOnly
			persisted.Enabled = false
			updated, err := f.plans.Update(ctx, persisted)
			require.NoError(t, err)
			require.Equal(t, plan.ID, updated.ID)
			require.Equal(t, f.account, updated.AccountID)
			require.Equal(t, persisted.LastRunAt, updated.LastRunAt)
			require.Equal(t, persisted.RunningUntil, updated.RunningUntil)
			var afterState, afterAccount string
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, plan.ID).Scan(&afterState))
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT row_to_json(a)::text FROM accounts a WHERE id=$1`, f.account).Scan(&afterAccount))
			require.JSONEq(t, beforeState, afterState)
			require.JSONEq(t, beforeAccount, afterAccount)
			require.Equal(t, groupCount, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
			require.Zero(t, f.count(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`))
		})
	}
}
