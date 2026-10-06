package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestQualityResultMetadataCannotConfigureRuleActions(t *testing.T) {
	plan := &ScheduledTestPlan{ModelID: "gpt-6-astra", CronExpression: "*/10 * * * *", MaxResults: 100, PelicanConfig: &PelicanTestConfig{QuestionKind: OpenAICodexStateProbeQuestionKind, ParallelCount: 1, ReasoningEffort: "high", Quality: &QualityPolicy{Action: "disable_scheduling"}}}
	plan.PelicanConfig.QualityModelOutcomes = map[string]string{"gpt-6-astra": "passed"}
	plan.PelicanConfig.QualityModelActions = map[string]string{"gpt-6-astra": "restored"}
	_, err := nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	require.Nil(t, plan.PelicanConfig.QualityModelOutcomes)
	require.Nil(t, plan.PelicanConfig.QualityModelActions)
}
