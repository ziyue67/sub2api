//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityJevPersistenceAndIsolationSafety(t *testing.T) {
	f, _, _ := newQualityIsolationFixture(t)
	ctx := t.Context()
	f.plan.PelicanConfig.Quality.Judge = &service.QualityJudgeConfig{Kind: "jev", Prompt: "compare values"}
	_, err := f.plans.Update(ctx, f.plan)
	require.NoError(t, err)
	reloaded, err := f.plans.GetByID(ctx, f.plan.ID)
	require.NoError(t, err)
	require.Equal(t, f.plan.PelicanConfig.Quality.Judge, reloaded.PelicanConfig.Quality.Judge)
	// Editing the profile invalidates the earlier claimed rule version. Prove
	// stale evidence cannot isolate, then use the saved version for new evidence.
	require.Equal(t, "stale_run", isolationRound(f, "failed", "failed"))
	require.Equal(t, 2, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
	f.plan = reloaded
	results := NewScheduledTestResultRepository(integrationDB)
	result, err := results.Create(ctx, &service.ScheduledTestResult{PlanID: f.plan.ID, Status: "failed", ResponseText: "21", ErrorMessage: "judge_inconclusive", LatencyMs: 12, StartedAt: time.Now(), FinishedAt: time.Now(), PelicanConfig: f.plan.PelicanConfig, QualityRoundID: "jev-synthetic-round", QualityJudgment: &service.QualityJudgment{Kind: "jev", ModelID: "jev-latest", Verdict: "unknown", Reason: "judge_jev_credentials_rejected", RulesVersion: "quality-choice-v1", LatencyMS: 53}})
	require.NoError(t, err)
	listed, err := results.ListByPlanID(ctx, f.plan.ID, 10)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, result.QualityJudgment, listed[0].QualityJudgment)
	require.Equal(t, int64(12), listed[0].LatencyMs)
	require.Equal(t, "jev", listed[0].PelicanConfig.Quality.Judge.Kind)
	// Inconclusive Jev evidence neither isolates nor restores either model.
	isolationRound(f, "inconclusive", "inconclusive")
	require.Equal(t, 2, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
	isolationRound(f, "failed", "failed")
	require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
	isolationRound(f, "inconclusive", "inconclusive")
	require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
}
