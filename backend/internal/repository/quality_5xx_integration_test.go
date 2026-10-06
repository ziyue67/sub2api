//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestQuality5xxClaimKeepsCronAndCoalescesScheduledRun(t *testing.T) {
	ctx := context.Background()
	var account int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('5xx trigger','openai','oauth','active',true) RETURNING id`).Scan(&account))
	defer integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account)
	plans := NewScheduledTestPlanRepository(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	future := now.Add(time.Hour)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{AccountID: account, ModelID: "gpt-6-astra", CronExpression: "0 * * * *", Enabled: true, MaxResults: 10, NextRunAt: &future, PelicanConfig: &service.PelicanTestConfig{QuestionKind: "candy", Prompt: "test", ParallelCount: 1, Quality: &service.QualityPolicy{TriggerOnUpstream5xx: true, Action: "disable_scheduling", ExpectedAnswer: "21"}}})
	require.NoError(t, err)
	until := now.Add(15 * time.Minute)
	ok, err := plans.ClaimPelican(ctx, plan, now, until, future)
	require.NoError(t, err)
	require.False(t, ok, "timer cannot claim a future plan")
	plan.TriggerSource = "upstream_5xx"
	ok, err = plans.ClaimPelican(ctx, plan, now, until, future)
	require.NoError(t, err)
	require.True(t, ok)
	stored, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.True(t, future.Equal(*stored.NextRunAt))
	ok, err = plans.ClaimPelican(ctx, plan, now, until, future)
	require.NoError(t, err)
	require.False(t, ok, "running plan must not duplicate")
	require.NoError(t, plans.FinishPelican(ctx, plan.ID, until, now))
	stored, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	stored.TriggerSource = "upstream_5xx"
	stored.TriggerObservedAt = &now
	ok, err = plans.ClaimPelican(ctx, stored, now.Add(time.Second), until, future)
	require.NoError(t, err)
	require.False(t, ok, "completed cron covers an earlier signal atomically")
	newSignal := now.Add(time.Second)
	stored.TriggerObservedAt = &newSignal
	ok, err = plans.ClaimPelican(ctx, stored, now.Add(time.Second), until, future)
	require.NoError(t, err)
	require.True(t, ok, "new 5xx must run even immediately after a completed test")
	require.NoError(t, plans.FinishPelican(ctx, plan.ID, until, now.Add(time.Second)))
	later := now.Add(61 * time.Second)
	stored.TriggerObservedAt = &later
	ok, err = plans.ClaimPelican(ctx, stored, later, until, future)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, plans.FinishPelican(ctx, plan.ID, until, later))
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_plans SET pelican_config=jsonb_set(pelican_config,'{quality,trigger_on_upstream_5xx}','false'),updated_at=clock_timestamp() WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	disabled, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	disabled.TriggerSource = "upstream_5xx"
	ok, err = plans.ClaimPelican(ctx, disabled, later.Add(time.Minute), until, future)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestQuality5xxFinishConsumesCrossedCronWithoutOverwritingEdits(t *testing.T) {
	ctx := context.Background()
	var account int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('crossing trigger','openai','oauth','active',true) RETURNING id`).Scan(&account))
	defer integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account)
	plans := NewScheduledTestPlanRepository(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(time.Second)
	until := now.Add(time.Minute)
	following := now.Add(time.Hour)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{AccountID: account, ModelID: "test", CronExpression: "0 * * * *", Enabled: true, MaxResults: 10, NextRunAt: &due, PelicanConfig: &service.PelicanTestConfig{ParallelCount: 1, Quality: &service.QualityPolicy{TriggerOnUpstream5xx: true}}})
	require.NoError(t, err)
	plan.TriggerSource = "upstream_5xx"
	ok, err := plans.ClaimPelican(ctx, plan, now, until, due)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, plans.FinishTriggeredQuality(ctx, plan, until, due.Add(time.Second), following))
	stored, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.True(t, following.Equal(*stored.NextRunAt))
	// Simulate an in-flight run followed by an administrator schedule change.
	editNext := following.Add(time.Hour)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_plans SET next_run_at=$2,updated_at=clock_timestamp(),running_until=$3 WHERE id=$1`, plan.ID, editNext, until)
	require.NoError(t, err)
	require.NoError(t, plans.FinishTriggeredQuality(ctx, plan, until, editNext.Add(time.Minute), following))
	stored, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.True(t, editNext.Equal(*stored.NextRunAt))
}
