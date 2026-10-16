package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQualityIsolationValidationAndTargets(t *testing.T) {
	plan := multiModelQualityPlan(t, `["sol","astra"]`)
	q := &QualityPolicy{Action: QualityActionIsolateModels, ExpectedAnswer: "21", RemoveGroupIDs: []int64{7}, RemoveModels: []string{"stale-cooldown-target"}}
	plan.PelicanConfig.Quality = q
	_, err := nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	require.Nil(t, q.RemoveModels)
	require.Equal(t, []string{"sol", "astra"}, QualityModels(plan))
	plan.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
	plan.PelicanConfig.ModelIDs = []string{"sol"}
	_, err = nextPlanRun(plan, time.Now())
	require.Error(t, err, "account-level state probes cannot decide per-model isolation")
	plan.PelicanConfig.QuestionKind = "candy"
	q.RemoveGroupIDs = nil
	_, err = nextPlanRun(plan, time.Now())
	require.Error(t, err, "the composite action requires explicit target groups")
}

func TestQualityIsolationRunnerPersistsIndependentVerdicts(t *testing.T) {
	plans := &qualityPlanRepo{}
	saved := &pelicanResults{}
	runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, saved)}
	runner.runPelican = func(_ context.Context, _ int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		answer := "21"
		if model == "sol" {
			answer = "17"
		}
		return &ScheduledTestResult{Status: "success", ResponseText: answer, PelicanConfig: cfg}, nil
	}
	runner.judgeQuality = func(_ context.Context, _ int64, _ *PelicanTestConfig, answer string) *QualityJudgment {
		verdict := "correct"
		if answer != "21" {
			verdict = "incorrect"
		}
		return &QualityJudgment{Verdict: verdict}
	}
	plan := multiModelQualityPlan(t, `["sol","astra"]`)
	plan.PelicanConfig.Quality.Action = QualityActionIsolateModels
	plan.PelicanConfig.Quality.RemoveGroupIDs = []int64{7}
	require.True(t, runner.runOnePlan(context.Background(), plan))
	require.Len(t, saved.results, 2)
	for _, result := range saved.results {
		require.Equal(t, map[string]string{"sol": "failed", "astra": "passed"}, result.PelicanConfig.QualityModelOutcomes)
	}
}

func TestQualityModelPartialSkipCannotRestoreIsolation(t *testing.T) {
	cfg := &PelicanTestConfig{ModelID: "sol"}
	results := []*ScheduledTestResult{
		{Status: "success", PelicanConfig: cfg, QualityJudgment: &QualityJudgment{Verdict: "correct"}},
		{Status: "skipped", PelicanConfig: cfg},
	}
	require.Equal(t, "inconclusive", qualityModelOutcomes(results, []string{"sol"})["sol"])
	results[0].Status = "failed"
	results[0].ErrorMessage = "answer_mismatch"
	results[0].QualityJudgment.Verdict = "incorrect"
	require.Equal(t, "failed", qualityModelOutcomes(results, []string{"sol"})["sol"])
}
