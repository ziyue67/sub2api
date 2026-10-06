package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func multiModelQualityPlan(t *testing.T, models string) *ScheduledTestPlan {
	t.Helper()
	plan := pelicanPlan()
	plan.PelicanConfig.ParallelCount = 1
	plan.PelicanConfig.QuestionKind = "candy"
	plan.PelicanConfig.Quality = &QualityPolicy{ExpectedAnswer: "21", Action: "disable_scheduling"}
	require.NoError(t, json.Unmarshal([]byte(`{"model_ids":`+models+`}`), plan.PelicanConfig))
	return plan
}

func TestQualityModelsRejectInvalidSelections(t *testing.T) {
	for _, models := range []string{`["gpt-6-astra", " "]`, `["gpt-6-astra", "` + strings.Repeat("x", 101) + `"]`} {
		_, err := nextPlanRun(multiModelQualityPlan(t, models), time.Now())
		require.Error(t, err)
	}
	probe := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
	probe.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
	_, err := nextPlanRun(probe, time.Now())
	require.Error(t, err, "state probes must keep the account-level single-probe guard")
}

func TestQualityModelsWaitForSlowModelBeforeOutcome(t *testing.T) {
	for _, failureModel := range []string{"", "gpt-6-astra", "gpt-6-sol"} {
		t.Run("fails_"+failureModel, func(t *testing.T) {
			plans := &qualityPlanRepo{}
			saved := &pelicanResults{}
			runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, saved)}
			started := make(chan string, 2)
			fastDone := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			runner.runPelican = func(ctx context.Context, _ int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
				started <- model
				if model == "gpt-6-sol" {
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				} else {
					defer close(fastDone)
				}
				if model == failureModel {
					return nil, errors.New("upstream failure")
				}
				return &ScheduledTestResult{Status: "success", ResponseText: "21", PelicanConfig: cfg}, nil
			}
			runner.judgeQuality = func(context.Context, int64, *PelicanTestConfig, string) *QualityJudgment {
				return &QualityJudgment{Verdict: "correct"}
			}
			plan := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
			done := make(chan bool, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			go func() { done <- runner.runOnePlan(ctx, plan) }()
			models := []string{}
			for i := 0; i < 2; i++ {
				select {
				case m := <-started:
					models = append(models, m)
				case <-ctx.Done():
					t.Fatal("both models must start concurrently")
				}
			}
			require.ElementsMatch(t, []string{"gpt-6-astra", "gpt-6-sol"}, models)
			<-fastDone
			select {
			case <-done:
				t.Fatal("round finished while slow model was still running")
			default:
			}
			release <- struct{}{}
			require.True(t, <-done)
			expected := "passed"
			if failureModel != "" {
				expected = "failed"
			}
			require.Equal(t, []string{expected}, plans.outcomes)
			require.True(t, plans.finished)
			require.Len(t, saved.results, 2)
			require.Equal(t, "gpt-6-astra", saved.results[0].PelicanConfig.ModelID)
			require.Equal(t, "gpt-6-sol", saved.results[1].PelicanConfig.ModelID)
			require.Equal(t, saved.results[0].QualityRoundID, saved.results[1].QualityRoundID)
		})
	}
}

func TestQualityMultiModel5xxUsesCandyInsteadOfConcurrentStateProbes(t *testing.T) {
	plan := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	cfg, apply := quality5xxTestConfig(account, plan.ModelID, *plan.PelicanConfig)
	require.Equal(t, "candy", cfg.QuestionKind)
	require.True(t, apply)
	require.Equal(t, CandyPrompt, cfg.Prompt)
}

type multiModelHistoryRepo struct {
	ScheduledTestResultRepository
	item *QualityHistoryResult
}

func (r *multiModelHistoryRepo) ListQualityHistory(context.Context, int64, int) ([]*QualityHistoryResult, error) {
	return []*QualityHistoryResult{r.item}, nil
}
func TestQualityMultiModelHistoryDoesNotPassAPartiallySavedRound(t *testing.T) {
	plan := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
	item := &QualityHistoryResult{PassedCount: 1, TotalCount: 1}
	item.Status = "success"
	item.PelicanConfig = plan.PelicanConfig
	svc := NewScheduledTestService(nil, &multiModelHistoryRepo{item: item})
	page, err := svc.ListQualityHistory(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, 2, page.Items[0].TotalCount)
	require.Equal(t, "failed", page.Items[0].Status)
}
