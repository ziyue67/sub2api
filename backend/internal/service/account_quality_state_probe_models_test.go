package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func stateProbeCooldownPlan(t *testing.T) *ScheduledTestPlan {
	t.Helper()
	plan := multiModelQualityPlan(t, `["gpt-6.1-sol"]`)
	plan.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
	plan.PelicanConfig.Quality.Action = QualityActionRemoveModel
	// Include targets that are deliberately different from the probe model.
	plan.PelicanConfig.Quality.RemoveModels = []string{"gpt-6.1-terra", "gpt-5.6-terra", "gpt-6-luna", "gpt-6.1-sol", "gpt-6-astra", "gpt-5.6-sol"}
	plan.PelicanConfig.Quality.AutoRestore = true
	return plan
}

func TestQualityStateProbeRunnerAppliesAccountVerdictToAllCooldownTargets(t *testing.T) {
	for _, tc := range []struct{ name, status, verdict, message, outcome string }{
		{"degraded", "failed", "incorrect", openAICodexStateDegradedError, "failed"},
		{"healthy", "success", "correct", "", "passed"},
		{"rate_limited", "failed", "unknown", "rate_limited", "inconclusive"},
		{"timeout", "failed", "unknown", "timeout", "inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plans := &qualityPlanRepo{}
			saved := &pelicanResults{}
			runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, saved)}
			calls := 0
			runner.runPelican = func(_ context.Context, _ int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
				calls++
				require.Equal(t, "gpt-6.1-sol", model)
				return &ScheduledTestResult{Status: tc.status, ErrorMessage: tc.message, QualityJudgment: &QualityJudgment{Verdict: tc.verdict}, PelicanConfig: cfg}, nil
			}
			plan := stateProbeCooldownPlan(t)
			require.True(t, runner.runOnePlan(context.Background(), plan))
			require.Equal(t, 1, calls, "keep one account-level probe, irrespective of target count")
			require.Equal(t, []string{tc.outcome}, plans.outcomes)
			require.Len(t, saved.results, 1)
			expected := map[string]string{}
			for _, model := range plan.PelicanConfig.Quality.RemoveModels {
				expected[model] = tc.outcome
			}
			require.Equal(t, expected, plan.QualityModelOutcomes)
			require.Equal(t, expected, saved.results[0].PelicanConfig.QualityModelOutcomes)
		})
	}
}

func TestQualityStateProbeSkippedAndPartialRoundsDoNotRestoreTargets(t *testing.T) {
	plan := stateProbeCooldownPlan(t)
	passed := &ScheduledTestResult{Status: "success", QualityJudgment: &QualityJudgment{Verdict: "correct"}}
	skipped := &ScheduledTestResult{Status: "skipped"}
	for _, tc := range []struct {
		results []*ScheduledTestResult
		want    string
	}{
		{nil, "skipped"},
		{[]*ScheduledTestResult{skipped}, "skipped"},
		{[]*ScheduledTestResult{passed, skipped}, "inconclusive"},
		{[]*ScheduledTestResult{passed, nil}, "inconclusive"},
	} {
		outcomes := qualityPlanModelOutcomes(plan, tc.results)
		require.Len(t, outcomes, 6)
		for _, outcome := range outcomes {
			require.Equal(t, tc.want, outcome)
		}
	}
}

func TestQualityCandyCooldownStillUsesIndependentModelVerdicts(t *testing.T) {
	plan := stateProbeCooldownPlan(t)
	plan.PelicanConfig.QuestionKind = "candy"
	results := []*ScheduledTestResult{
		{Status: "failed", ErrorMessage: "answer_mismatch", QualityJudgment: &QualityJudgment{Verdict: "incorrect"}, PelicanConfig: &PelicanTestConfig{ModelID: "gpt-6.1-sol"}},
		{Status: "success", QualityJudgment: &QualityJudgment{Verdict: "correct"}, PelicanConfig: &PelicanTestConfig{ModelID: "gpt-6-astra"}},
	}
	require.Equal(t, map[string]string{"gpt-6.1-sol": "failed", "gpt-6-astra": "passed", "gpt-6.1-terra": "skipped", "gpt-5.6-terra": "skipped", "gpt-6-luna": "skipped", "gpt-5.6-sol": "skipped"}, qualityPlanModelOutcomes(plan, results))
}
