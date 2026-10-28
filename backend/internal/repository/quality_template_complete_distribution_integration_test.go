//go:build integration

package repository

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityTemplateCompleteDistributionKeepsItsOwnSemantics(t *testing.T) {
	ctx := context.Background()
	for _, action := range []string{service.QualityActionObserveOnly, "remove_groups", service.QualityActionIsolateModels, "disable_scheduling"} {
		t.Run(action, func(t *testing.T) {
			f, group, _ := newQualityIsolationFixture(t)
			require.NoError(t, f.plans.FinishPelican(ctx, f.plan.ID, f.until, time.Now()))
			require.NoError(t, f.plans.Delete(ctx, f.plan.ID))
			f.exec(`UPDATE accounts SET name=$2 WHERE id=$1`, fmt.Sprintf("complete-distribution-%d", f.account))
			repo := NewQualityRuleTemplateRepository(integrationEntClient, integrationDB)
			svc := service.ProvideScheduledTestService(f.plans, NewScheduledTestResultRepository(integrationDB), repo, nil)
			tpl := &service.QualityRuleTemplate{ModelID: "first", CronExpression: "7 */2 * * *", Enabled: true, MaxResults: 137,
				AccountFilter: service.QualityRuleAccountFilter{Group: strconv.FormatInt(group, 10), Search: fmt.Sprintf("complete-distribution-%d", f.account)},
				PelicanConfig: &service.PelicanTestConfig{RuleName: "Distribution display", QuestionKind: "candy", TestChannel: "account", Prompt: "Synthetic question", ReasoningEffort: "xhigh", ParallelCount: 3, ModelIDs: []string{"first", "second"}, TriggerSource: "runtime-not-reusable", Quality: &service.QualityPolicy{Action: action, ExpectedAnswer: "42", RemoveGroupIDs: []int64{group}, AutoRestore: true, TriggerOnUpstream5xx: true, Judge: &service.QualityJudgeConfig{Kind: "jev", Prompt: "Compare"}}}}
			saved, created, err := svc.CreateQualityTemplate(ctx, tpl)
			require.NoError(t, err)
			require.Equal(t, 1, created, "enabled distribution still creates rules for matching accounts")
			t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM quality_rule_templates WHERE id=$1`, saved.ID) })
			require.Len(t, saved.PlanIDs, 1)
			reloaded, err := repo.Get(ctx, saved.ID)
			require.NoError(t, err)
			plan, err := f.plans.GetByID(ctx, reloaded.PlanIDs[0])
			require.NoError(t, err)
			require.Equal(t, reloaded.PelicanConfig, plan.PelicanConfig)
			require.Equal(t, action, plan.PelicanConfig.Quality.Action)
			require.Empty(t, plan.PelicanConfig.TriggerSource)
			require.Equal(t, 2, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`), "creating a rule does not apply penalties")
			saved.Enabled = false
			saved.CronExpression = "11 */3 * * *"
			saved.MaxResults = 151
			saved.PelicanConfig.Quality.AutoRestore = false
			updated, err := svc.UpdateQualityTemplate(ctx, saved)
			require.NoError(t, err)
			require.Equal(t, 1, updated.Updated)
			require.Zero(t, updated.Failed)
			plan, err = f.plans.GetByID(ctx, plan.ID)
			require.NoError(t, err)
			require.False(t, plan.Enabled)
			require.Equal(t, "11 */3 * * *", plan.CronExpression)
			require.Equal(t, 151, plan.MaxResults)
			require.False(t, plan.PelicanConfig.Quality.AutoRestore)
			require.Equal(t, 2, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
		})
	}
}

func TestQualityTemplateCompleteDistributionCannotOverrideIsolatedScope(t *testing.T) {
	ctx := context.Background()
	f, group, _ := newQualityIsolationFixture(t)
	require.NoError(t, f.plans.FinishPelican(ctx, f.plan.ID, f.until, time.Now()))
	require.NoError(t, f.plans.Delete(ctx, f.plan.ID))
	f.exec(`UPDATE accounts SET name=$2 WHERE id=$1`, fmt.Sprintf("complete-distribution-guard-%d", f.account))
	repo := NewQualityRuleTemplateRepository(integrationEntClient, integrationDB)
	svc := service.ProvideScheduledTestService(f.plans, NewScheduledTestResultRepository(integrationDB), repo, nil)
	tpl := &service.QualityRuleTemplate{ModelID: "sol", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 100, AccountFilter: service.QualityRuleAccountFilter{Group: strconv.FormatInt(group, 10), Search: fmt.Sprintf("complete-distribution-guard-%d", f.account)},
		PelicanConfig: &service.PelicanTestConfig{QuestionKind: "candy", Prompt: "Synthetic", ReasoningEffort: "high", ParallelCount: 1, ModelIDs: []string{"sol", "astra"}, Quality: &service.QualityPolicy{Action: service.QualityActionIsolateModels, ExpectedAnswer: "42", RemoveGroupIDs: []int64{group}, AutoRestore: true}}}
	saved, created, err := svc.CreateQualityTemplate(ctx, tpl)
	require.NoError(t, err)
	require.Equal(t, 1, created)
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM quality_rule_templates WHERE id=$1`, saved.ID) })
	f.plan, err = f.plans.GetByID(ctx, saved.PlanIDs[0])
	require.NoError(t, err)
	f.claim()
	require.Equal(t, "groups_removed", isolationRound(f, "failed", "failed"))
	require.NoError(t, f.plans.FinishPelican(ctx, f.plan.ID, f.until, time.Now()))
	var beforeState, beforeAccount string
	require.NoError(t, integrationDB.QueryRow(`SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&beforeState))
	require.NoError(t, integrationDB.QueryRow(`SELECT to_jsonb(a)::text FROM accounts a WHERE id=$1`, f.account).Scan(&beforeAccount))
	saved.PelicanConfig.Quality.Action = service.QualityActionObserveOnly
	result, err := svc.UpdateQualityTemplate(ctx, saved)
	require.NoError(t, err)
	require.Equal(t, 1, result.Failed)
	require.Zero(t, result.Updated)
	after, err := f.plans.GetByID(ctx, f.plan.ID)
	require.NoError(t, err)
	require.Equal(t, service.QualityActionIsolateModels, after.PelicanConfig.Quality.Action)
	var afterState, afterAccount string
	require.NoError(t, integrationDB.QueryRow(`SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&afterState))
	require.NoError(t, integrationDB.QueryRow(`SELECT to_jsonb(a)::text FROM accounts a WHERE id=$1`, f.account).Scan(&afterAccount))
	require.JSONEq(t, beforeState, afterState)
	require.JSONEq(t, beforeAccount, afterAccount)
}
