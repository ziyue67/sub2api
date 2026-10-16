//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newQualityIsolationFixture(t *testing.T) (*qualityBPSFixture, int64, int64) {
	t.Helper()
	ctx := context.Background()
	f := &qualityBPSFixture{t: t, plans: NewScheduledTestPlanRepository(integrationDB)}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable,concurrency) VALUES('isolation-test','openai','oauth','active',true,20) RETURNING id`).Scan(&f.account))
	var group, other int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES('isolation-target','openai') RETURNING id`).Scan(&group))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES('isolation-other','openai') RETURNING id`).Scan(&other))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, f.account)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE account_id=$1`, f.account)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, f.account)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id IN ($1,$2)`, group, other)
	})
	f.exec(`INSERT INTO account_groups(account_id,group_id,priority,allowed_models) VALUES($1,$2,7,'["sol","astra"]'),($1,$3,50,NULL)`, group, other)
	svc := service.NewScheduledTestService(f.plans, NewScheduledTestResultRepository(integrationDB))
	plan, err := svc.CreatePlan(ctx, &service.ScheduledTestPlan{AccountID: f.account, ModelID: "sol", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 100,
		PelicanConfig: &service.PelicanTestConfig{QuestionKind: "candy", ModelIDs: []string{"sol", "astra"}, Prompt: "test", ReasoningEffort: "high", ParallelCount: 1,
			Quality: &service.QualityPolicy{Action: service.QualityActionIsolateModels, ExpectedAnswer: "21", RemoveGroupIDs: []int64{group}, AutoRestore: true}}})
	require.NoError(t, err)
	f.plan = plan
	f.claim()
	return f, group, other
}

func isolationRound(f *qualityBPSFixture, sol, astra string) string {
	f.t.Helper()
	f.plan.QualityModelOutcomes = map[string]string{"sol": sol, "astra": astra}
	return f.apply("failed") // The aggregate failure must never override per-model evidence.
}

func TestQualityIsolationGroupsFollowIndependentModelRecovery(t *testing.T) {
	f, group, other := newQualityIsolationFixture(t)
	countGroup := func(id int64) int {
		return f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1 AND group_id=` + qualityIsolationID(id))
	}
	require.Equal(t, "models_isolated", isolationRound(f, "failed", "passed"))
	require.Equal(t, 1, countGroup(group))
	require.Contains(t, f.extra()[service.QualityModelIsolationKey], "sol")
	require.NotContains(t, f.extra()[service.QualityModelIsolationKey], "astra")
	require.Equal(t, "groups_removed", isolationRound(f, "failed", "failed"))
	require.Zero(t, countGroup(group))
	require.Equal(t, 1, countGroup(other))
	// Worker/process restart: ownership and model blocks come from durable state.
	reloaded, err := f.plans.GetByID(context.Background(), f.plan.ID)
	require.NoError(t, err)
	f.plan = reloaded
	require.Equal(t, "groups_restored_models_isolated", isolationRound(f, "failed", "passed"))
	require.Equal(t, 1, countGroup(group))
	require.NotContains(t, f.extra()[service.QualityModelIsolationKey], "astra")
	var priority, concurrency int
	var allowed string
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT priority,allowed_models::text FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.account, group).Scan(&priority, &allowed))
	require.Equal(t, 7, priority)
	require.JSONEq(t, `["sol","astra"]`, allowed)
	require.Equal(t, "restored", isolationRound(f, "passed", "passed"))
	require.NotContains(t, f.extra(), service.QualityModelIsolationKey)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT concurrency FROM accounts WHERE id=$1`, f.account).Scan(&concurrency))
	require.Equal(t, 20, concurrency)
	require.Positive(t, f.count(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1 AND payload->'group_ids' @> '[`+qualityIsolationID(group)+`]'::jsonb`))
	require.NoError(t, f.plans.Delete(context.Background(), f.plan.ID))
}

func qualityIsolationID(id int64) string { raw, _ := json.Marshal(id); return string(raw) }

func TestQualityIsolationUnknownAndSkippedCannotRemoveOrRestoreGroups(t *testing.T) {
	for _, verdict := range []string{"inconclusive", "skipped"} {
		t.Run(verdict, func(t *testing.T) {
			f, group, _ := newQualityIsolationFixture(t)
			isolationRound(f, "failed", verdict)
			require.Equal(t, 2, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
			require.NotContains(t, f.extra()[service.QualityModelIsolationKey], "astra")
			isolationRound(f, "failed", "failed")
			isolationRound(f, verdict, verdict)
			require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
			require.Contains(t, f.extra()[service.QualityModelIsolationKey], "sol")
			require.Contains(t, f.extra()[service.QualityModelIsolationKey], "astra")
			require.Equal(t, "groups_restored_models_isolated", isolationRound(f, "passed", verdict))
			require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1 AND group_id=`+qualityIsolationID(group)))
		})
	}
}

func TestQualityIsolationConflictsRollbackModelAndGroupChanges(t *testing.T) {
	f, group, _ := newQualityIsolationFixture(t)
	isolationRound(f, "failed", "failed")
	// Manual membership replacement must survive, and failed restoration must not
	// advertise or commit the passing model's release.
	f.exec(`INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,99)`, group)
	before, err := json.Marshal(f.extra())
	require.NoError(t, err)
	require.Equal(t, "restore_conflict", isolationRound(f, "failed", "passed"))
	after, err := json.Marshal(f.extra())
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	var priority int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `SELECT priority FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.account, group).Scan(&priority))
	require.Equal(t, 99, priority)
}

func TestQualityIsolationScopeAndDeleteGuard(t *testing.T) {
	f, _, _ := newQualityIsolationFixture(t)
	isolationRound(f, "failed", "passed")
	ctx := context.Background()
	require.ErrorContains(t, f.plans.Delete(ctx, f.plan.ID), "restore")
	for _, action := range []string{service.QualityActionObserveOnly, "remove_groups"} {
		plan, err := f.plans.GetByID(ctx, f.plan.ID)
		require.NoError(t, err)
		plan.PelicanConfig.Quality.Action = action
		_, err = f.plans.Update(ctx, plan)
		require.ErrorContains(t, err, "restore")
	}
	plan, err := f.plans.GetByID(ctx, f.plan.ID)
	require.NoError(t, err)
	plan.PelicanConfig.ModelIDs = []string{"astra"}
	_, err = f.plans.Update(ctx, plan)
	require.ErrorContains(t, err, "restore")
	plan, err = f.plans.GetByID(ctx, f.plan.ID)
	require.NoError(t, err)
	plan.Enabled = false
	plan.CronExpression = "0 * * * *"
	_, err = f.plans.Update(ctx, plan)
	require.NoError(t, err, "pausing or editing the schedule preserves recovery ownership")
	require.Contains(t, f.extra()[service.QualityModelIsolationKey], "sol")
	require.Equal(t, "stale_run", isolationRound(f, "passed", "passed"))
}

func TestQualityIsolationAutoRestoreOptIn(t *testing.T) {
	f, _, _ := newQualityIsolationFixture(t)
	f.plan.PelicanConfig.Quality.AutoRestore = false
	isolationRound(f, "failed", "failed")
	isolationRound(f, "passed", "passed")
	require.Len(t, f.extra()[service.QualityModelIsolationKey], 2)
	require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
	f.plan.PelicanConfig.Quality.AutoRestore = true
	require.Equal(t, "restored", isolationRound(f, "passed", "passed"))
	require.NotContains(t, f.extra(), service.QualityModelIsolationKey)
}
