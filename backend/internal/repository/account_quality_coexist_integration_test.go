//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityBPSAndQuarantineCoexist(t *testing.T) {
	for _, action := range []string{"remove_groups", "disable_scheduling"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			bps := newQualityBPSFixture(t, `{"unrelated":"kept"}`, &service.QualityPolicy{
				Action: service.QualityActionEnableBPS, AutoRestore: true,
				BPS: &service.QualityBPSPolicy{FailureThreshold: 1, AllModels: true},
			})
			var group int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES('quality-coexist','openai') RETURNING id`).Scan(&group))
			t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group) })
			bps.exec(`INSERT INTO account_groups(account_id,group_id,priority,allowed_models) VALUES($1,$2,7,'["gpt-test"]')`, group)
			svc := service.NewScheduledTestService(bps.plans, NewScheduledTestResultRepository(integrationDB))
			plan, err := svc.CreatePlan(ctx, &service.ScheduledTestPlan{
				AccountID: bps.account, ModelID: "gpt-test", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 100,
				PelicanConfig: &service.PelicanTestConfig{QuestionKind: "candy", Prompt: "test", ReasoningEffort: "high", ParallelCount: 1,
					Quality: &service.QualityPolicy{ExpectedAnswer: "42", Action: action, RemoveGroupIDs: []int64{group}, AutoRestore: true}},
			})
			require.NoError(t, err, "a quarantine rule can coexist with BPS")
			quarantine := &qualityBPSFixture{t: t, plans: bps.plans, account: bps.account, plan: plan}

			// Pausing reserves only the same scope; duplicate creation and action edits
			// cannot install a second owner of either group/scheduling or BPS settings.
			for _, owner := range []*service.ScheduledTestPlan{bps.plan, plan} {
				bps.exec(`UPDATE scheduled_test_plans SET enabled=false WHERE account_id=$1 AND id=$2`, owner.ID)
				duplicate := *owner
				duplicate.ID = 0
				_, err = svc.CreatePlan(ctx, &duplicate)
				require.Error(t, err, "paused rules still reserve their own scope")
				bps.exec(`UPDATE scheduled_test_plans SET enabled=true WHERE account_id=$1 AND id=$2`, owner.ID)
			}
			edited := *plan
			edited.PelicanConfig = bps.plan.PelicanConfig
			_, err = bps.plans.Update(ctx, &edited)
			require.Error(t, err, "editing cannot overwrite an occupied scope")

			// The per-account lease serializes both rules.
			require.NoError(t, bps.plans.TriggerQuality(ctx, plan.ID))
			plan, err = bps.plans.GetByID(ctx, plan.ID)
			require.NoError(t, err)
			now := time.Now().Add(time.Second)
			claimed, err := bps.plans.ClaimPelican(ctx, plan, now, now.Add(15*time.Minute), now.Add(30*time.Minute))
			require.NoError(t, err)
			require.False(t, claimed)
			require.NoError(t, bps.plans.FinishPelican(ctx, bps.plan.ID, bps.until, time.Now()))

			quarantine.claim()
			want := "groups_removed"
			if action == "disable_scheduling" {
				want = "scheduling_disabled"
			}
			require.Equal(t, want, quarantine.apply("failed"))
			require.NoError(t, bps.plans.FinishPelican(ctx, quarantine.plan.ID, quarantine.until, time.Now()))
			bps.claim()
			require.Equal(t, "bps_enabled", bps.apply("failed"))
			require.Equal(t, 2, bps.count(`SELECT count(*) FROM account_quality_states s JOIN scheduled_test_plans p ON p.id=s.plan_id WHERE p.account_id=$1`))
			require.NoError(t, bps.plans.FinishPelican(ctx, bps.plan.ID, bps.until, time.Now()))

			// Passing via the business protocol restores quarantine without closing BPS.
			quarantine.claim()
			require.Equal(t, "restored", quarantine.apply("passed"))
			require.Equal(t, true, bps.extra()["openai_excel_bps"])
			var priority int
			var models string
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT priority,allowed_models::text FROM account_groups WHERE account_id=$1 AND group_id=$2`, bps.account, group).Scan(&priority, &models))
			require.Equal(t, 7, priority)
			require.JSONEq(t, `["gpt-test"]`, models)
			require.Equal(t, 1, bps.count(`SELECT count(*) FROM accounts WHERE id=$1 AND schedulable`))
			require.NoError(t, bps.plans.FinishPelican(ctx, quarantine.plan.ID, quarantine.until, time.Now()))

			bps.claim()
			require.Equal(t, "restored", bps.apply("passed"))
			require.NotContains(t, bps.extra(), "openai_excel_bps")
			require.Equal(t, "kept", bps.extra()["unrelated"])
			require.Zero(t, bps.count(`SELECT count(*) FROM account_quality_states s JOIN scheduled_test_plans p ON p.id=s.plan_id WHERE p.account_id=$1`))
		})
	}
}

func TestQualityCoexistRespectsPendingOwnershipAfterActionChange(t *testing.T) {
	ctx := context.Background()
	owner := newQualityBPSFixture(t, `{}`, &service.QualityPolicy{Action: service.QualityActionEnableBPS, AutoRestore: true,
		BPS: &service.QualityBPSPolicy{FailureThreshold: 1, AllModels: true}})
	require.Equal(t, "bps_enabled", owner.apply("failed"))
	owner.exec(`UPDATE scheduled_test_plans SET pelican_config=jsonb_set(pelican_config,'{quality,action}','"disable_scheduling"'), running_until=NULL, updated_at=NOW() WHERE account_id=$1`)
	svc := service.NewScheduledTestService(owner.plans, NewScheduledTestResultRepository(integrationDB))
	newPlan := *owner.plan
	newPlan.ID = 0
	plan, err := svc.CreatePlan(ctx, &newPlan)
	require.NoError(t, err)
	next := &qualityBPSFixture{t: t, plans: owner.plans, account: owner.account, plan: plan}
	next.claim()
	require.Equal(t, "action_conflict", next.apply("failed"))
	require.Equal(t, true, owner.extra()["openai_excel_bps"])
	require.NoError(t, owner.plans.FinishPelican(ctx, next.plan.ID, next.until, time.Now()))
	owner.claim()
	require.Equal(t, "restored", owner.apply("passed"))
	require.NoError(t, owner.plans.FinishPelican(ctx, owner.plan.ID, owner.until, time.Now()))
	next.claim()
	require.Equal(t, "bps_enabled", next.apply("failed"))
}
