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

func TestQualityRuleTemplateFollowsGroupAccounts(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	var group, otherGroup int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, fmt.Sprintf("quality-template-%d", suffix)).Scan(&group))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, fmt.Sprintf("quality-template-other-%d", suffix)).Scan(&otherGroup))
	var accounts []int64
	addAccount := func(inGroup int64, status string) int64 {
		var id int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable,extra) VALUES($1,'openai','oauth',$2,true,'{}'::jsonb) RETURNING id`,
			fmt.Sprintf("quality-template-%d-%d", suffix, len(accounts)), status).Scan(&id))
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)`, id, inGroup)
		require.NoError(t, err)
		accounts = append(accounts, id)
		return id
	}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM quality_rule_templates WHERE account_filter->>'group' = $1`, strconv.FormatInt(group, 10))
		for _, id := range accounts {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, id)
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE account_id=$1`, id)
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1`, id)
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
		}
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id IN ($1, $2)`, group, otherGroup)
	})
	first := addAccount(group, service.StatusActive)
	disabled := addAccount(group, service.StatusDisabled)
	outside := addAccount(otherGroup, service.StatusActive)

	plans := NewScheduledTestPlanRepository(integrationDB)
	templates := NewQualityRuleTemplateRepository(integrationEntClient, integrationDB)
	svc := service.ProvideScheduledTestService(plans, NewScheduledTestResultRepository(integrationDB), templates)
	countPlans := func(accountID int64) int {
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_plans WHERE account_id=$1`, accountID).Scan(&n))
		return n
	}

	template, created, err := svc.CreateQualityTemplate(ctx, &service.QualityRuleTemplate{
		AccountFilter:  service.QualityRuleAccountFilter{Group: strconv.FormatInt(group, 10), Statuses: []string{"active"}},
		ModelID:        "gpt-test",
		CronExpression: "*/30 * * * *",
		Enabled:        true,
		PelicanConfig: &service.PelicanTestConfig{QuestionKind: service.OpenAICodexStateProbeQuestionKind, ReasoningEffort: "medium", ParallelCount: 1,
			Quality: &service.QualityPolicy{Action: "disable_scheduling", AutoRestore: true}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, created)
	require.Len(t, template.PlanIDs, 1)
	require.NotNil(t, template.LastSyncedAt)
	require.Equal(t, 1, countPlans(first))
	require.Zero(t, countPlans(disabled), "status filter excludes disabled accounts")
	require.Zero(t, countPlans(outside), "group filter excludes other groups")

	// A new account in the group and a re-enabled one are both covered on the next sync.
	joined := addAccount(group, service.StatusActive)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET status='active' WHERE id=$1`, disabled)
	require.NoError(t, err)
	added, err := svc.SyncQualityTemplates(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, added)
	require.Equal(t, 1, countPlans(joined))
	require.Equal(t, 1, countPlans(disabled))
	added, err = svc.SyncQualityTemplates(ctx)
	require.NoError(t, err)
	require.Zero(t, added, "sync is idempotent")

	// A hand-deleted rule is not recreated; its link row stays with plan_id cleared.
	joinedPlans, err := plans.ListByAccountID(ctx, joined)
	require.NoError(t, err)
	require.NoError(t, svc.DeletePlan(ctx, joinedPlans[0].ID))
	_, err = svc.SyncQualityTemplates(ctx)
	require.NoError(t, err)
	require.Zero(t, countPlans(joined))
	var cleared int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM quality_rule_template_accounts WHERE template_id=$1 AND account_id=$2 AND plan_id IS NULL`, template.ID, joined).Scan(&cleared))
	require.Equal(t, 1, cleared)

	// A sync holding an older template version writes nothing.
	stale, err := templates.Get(ctx, template.ID)
	require.NoError(t, err)
	latest := *stale
	latest.CronExpression = "0 * * * *"
	result, err := svc.UpdateQualityTemplate(ctx, &latest)
	require.NoError(t, err)
	require.Equal(t, 2, result.Updated)
	require.Zero(t, result.Failed)
	late := addAccount(group, service.StatusActive)
	_, err = templates.CreateLinkedPlan(ctx, stale, &service.ScheduledTestPlan{AccountID: late, ModelID: "gpt-test", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 100, PelicanConfig: stale.PelicanConfig})
	require.ErrorIs(t, err, service.ErrQualityTemplateSkipped)
	require.Zero(t, countPlans(late))
	firstPlans, err := plans.ListByAccountID(ctx, first)
	require.NoError(t, err)
	require.Equal(t, "0 * * * *", firstPlans[0].CronExpression)

	// Deleting the group rule can take its rules with it.
	deleted, err := svc.DeleteQualityTemplate(ctx, template.ID, true)
	require.NoError(t, err)
	require.Equal(t, 2, deleted)
	require.Zero(t, countPlans(first))
	require.Zero(t, countPlans(disabled))
	_, err = templates.Get(ctx, template.ID)
	require.ErrorIs(t, err, service.ErrQualityTemplateNotFound)
}
