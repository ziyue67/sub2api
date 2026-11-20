//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func groupPersonalSettings(t *testing.T) *SettingService {
	t.Helper()
	cfg := &config.Config{}
	cfg.JWT.Secret = "isolated-personal-group-test"
	return NewSettingService(&personalQuestionSettingsRepo{values: map[string]string{}}, cfg)
}

func TestPelicanGroupPersonalPlanOwnership(t *testing.T) {
	ctx := context.Background()
	repo := newGroupTestRepoFake()
	svc, _ := newGroupTestService(repo, nil, nil)
	svc.personalQuestions = groupPersonalSettings(t)
	_, err := svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 71, []PelicanPersonalQuestion{{Title: "Rain", Prompt: "Draw rain", Enabled: true}})
	require.NoError(t, err)
	input := PelicanGroupTestPlanInput{GroupID: 4, ModelID: "fixture-model", PromptMode: PelicanPromptModePersonal, ReasoningEffort: "high", ParallelCount: 3, Enabled: true}
	_, err = svc.CreatePlan(ctx, input)
	require.Error(t, err, "personal bank requires trusted session identity")
	input.PersonalBankOwnerID = 72
	_, err = svc.CreatePlan(ctx, input)
	require.ErrorIs(t, err, errPelicanPersonalEmpty, "cannot silently use the other administrator's bank")
	input.PersonalBankOwnerID = 71
	input.Prompt = "ignore this stale custom prompt"
	plan, err := svc.CreatePlan(ctx, input)
	require.NoError(t, err)
	require.Equal(t, int64(71), plan.PelicanConfig.PersonalBankOwnerID)
	require.Empty(t, plan.PelicanConfig.Prompt)
	require.Nil(t, plan.PelicanConfig.PelicanQuestion, "a plan stores a bank, not a preselected question")
	input.PersonalBankOwnerID, input.Enabled = 72, false
	plan, err = svc.UpdatePlan(ctx, plan.ID, input)
	require.NoError(t, err)
	require.Equal(t, int64(71), plan.PelicanConfig.PersonalBankOwnerID, "quick pause keeps the existing bank")
	input.UseMyQuestionBank, input.Enabled = true, true
	_, err = svc.UpdatePlan(ctx, plan.ID, input)
	require.ErrorIs(t, err, errPelicanPersonalEmpty)
	_, err = svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 72, []PelicanPersonalQuestion{{Title: "Clock", Prompt: "Draw clock", Enabled: true}})
	require.NoError(t, err)
	plan, err = svc.UpdatePlan(ctx, plan.ID, input)
	require.NoError(t, err)
	require.Equal(t, int64(72), plan.PelicanConfig.PersonalBankOwnerID, "explicit editor save binds the current administrator")
	input.PromptMode, input.Prompt = "custom", "Fixed custom question"
	plan, err = svc.UpdatePlan(ctx, plan.ID, input)
	require.NoError(t, err)
	require.Zero(t, plan.PelicanConfig.PersonalBankOwnerID)
	require.Equal(t, input.Prompt, plan.PelicanConfig.Prompt)
}

func TestPelicanGroupPersonalRoundFrozenAcrossSamplesAndBankEdits(t *testing.T) {
	ctx := context.Background()
	plan := groupTestPlan(3)
	plan.PelicanConfig.PromptMode = PelicanPromptModePersonal
	plan.PelicanConfig.PersonalBankOwnerID = 71
	repo := newGroupTestRepoFake(plan)
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}, {account: account(13, "c")}}}
	seen := make(chan *PelicanTestConfig, 3)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	svc, _ := newGroupTestService(repo, router, func(_ context.Context, _ int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		seen <- cfg
		<-release
		return &ScheduledTestResult{Status: "success", ResponseText: "<svg></svg>"}, nil
	})
	svc.personalQuestions = groupPersonalSettings(t)
	items, err := svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 71, []PelicanPersonalQuestion{
		{Title: "Rain", Prompt: "Draw rain", Enabled: true}, {Title: "Clock", Prompt: "Draw clock", Enabled: true},
		{Title: "Disabled", Prompt: "Never draw this", Enabled: false},
	})
	require.NoError(t, err)
	require.NoError(t, svc.RunNow(ctx, plan.ID))
	var configs []*PelicanTestConfig
	for range 3 {
		select {
		case cfg := <-seen:
			configs = append(configs, cfg)
		case <-time.After(5 * time.Second):
			t.Fatal("parallel sample did not start")
		}
	}
	frozen := *configs[0].PelicanQuestion
	for _, cfg := range configs {
		require.Equal(t, frozen, *cfg.PelicanQuestion)
		require.Equal(t, 3, cfg.ParallelCount)
		require.Equal(t, "high", cfg.ReasoningEffort)
		require.Contains(t, cfg.ActualPrompt, frozen.Prompt)
	}
	require.NotEqual(t, items[2].ID, frozen.ID)
	require.Contains(t, frozen.RoundID, "group:7:manual-group:")
	_, err = svc.personalQuestions.ResolvePelicanPersonalRound(71, &frozen)
	require.NoError(t, err, "round ID is covered by a valid owner-bound signature")
	for i := range items {
		items[i].Prompt, items[i].Enabled = "Changed after draw", false
	}
	_, err = svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 71, items)
	require.NoError(t, err)
	once.Do(func() { close(release) })
	svc.runs.Wait()
	require.Len(t, repo.results, 3)
	for _, result := range repo.results {
		require.Equal(t, frozen, *result.PelicanConfig.PelicanQuestion)
		require.Contains(t, result.PelicanConfig.ActualPrompt, frozen.Prompt)
	}
	require.Len(t, repo.finished, 1)
}

func TestPelicanGroupPersonalScheduledRoundsAndEmptyBank(t *testing.T) {
	ctx := context.Background()
	plan := groupTestPlan(1)
	plan.PelicanConfig.PromptMode = PelicanPromptModePersonal
	plan.PelicanConfig.PersonalBankOwnerID = 71
	repo := newGroupTestRepoFake(plan)
	svc, _ := newGroupTestService(repo, nil, nil)
	svc.personalQuestions = groupPersonalSettings(t)
	items, err := svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 71, []PelicanPersonalQuestion{
		{Title: "Rain", Prompt: "Rain", Enabled: true}, {Title: "Clock", Prompt: "Clock", Enabled: true},
	})
	require.NoError(t, err)
	first, err := svc.prepareGroupQuestion(ctx, plan, "scheduled:first")
	require.NoError(t, err)
	repo.listed = []*PelicanGroupTestResult{{PelicanConfig: first}}
	second, err := svc.prepareGroupQuestion(ctx, plan, "scheduled:second")
	require.NoError(t, err)
	require.NotEqual(t, first.PelicanQuestion.ID, second.PelicanQuestion.ID)
	require.NotEqual(t, first.PelicanQuestion.RoundID, second.PelicanQuestion.RoundID)
	repo.listed = []*PelicanGroupTestResult{{Status: "failed", PelicanConfig: plan.PelicanConfig}, {PelicanConfig: first}}
	afterFailure, err := svc.prepareGroupQuestion(ctx, plan, "scheduled:after-preparation-failure")
	require.NoError(t, err)
	require.NotEqual(t, first.PelicanQuestion.ID, afterFailure.PelicanQuestion.ID, "a preparation failure does not erase the last drawn question")
	items[1].Enabled = false
	_, err = svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 71, items)
	require.NoError(t, err)
	single, err := svc.prepareGroupQuestion(ctx, plan, "scheduled:single")
	require.NoError(t, err)
	require.Equal(t, items[0].ID, single.PelicanQuestion.ID)
	items[0].Enabled = false
	_, err = svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 71, items)
	require.NoError(t, err)
	require.ErrorIs(t, svc.RunNow(ctx, plan.ID), errPelicanPersonalEmpty)
	require.Empty(t, repo.claims, "manual empty-bank rejection does not acquire a lease or start a model")
	svc.runScheduled(ctx, plan, groupTestNow)
	require.Len(t, repo.results, 1, "scheduled preparation failure remains visible")
	require.Equal(t, "failed", repo.results[0].Status)
	require.Contains(t, repo.results[0].ErrorMessage, "personal_question_unavailable")
	require.NotNil(t, repo.results[0].CostUSD)
	require.Zero(t, *repo.results[0].CostUSD)
	require.Len(t, repo.finished, 1, "scheduled failure releases the lease")
}

func TestPelicanGroupPersonalScheduledExecution(t *testing.T) {
	ctx := context.Background()
	plan := groupTestPlan(3)
	plan.NextRunAt = &groupTestNow
	plan.PelicanConfig.PromptMode = PelicanPromptModePersonal
	plan.PelicanConfig.PersonalBankOwnerID = 71
	repo := newGroupTestRepoFake(plan)
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}, {account: account(13, "c")}}}
	seen := make(chan PelicanQuestion, 3)
	svc, _ := newGroupTestService(repo, router, func(_ context.Context, _ int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		seen <- *cfg.PelicanQuestion
		return &ScheduledTestResult{Status: "success", ResponseText: "<svg></svg>"}, nil
	})
	svc.personalQuestions = groupPersonalSettings(t)
	_, err := svc.personalQuestions.SavePelicanPersonalQuestions(ctx, 71, []PelicanPersonalQuestion{
		{Title: "Rain", Prompt: "Draw rain", Enabled: true}, {Title: "Clock", Prompt: "Draw clock", Enabled: true},
	})
	require.NoError(t, err)
	svc.runScheduled(ctx, plan, groupTestNow)
	require.Len(t, repo.claims, 1)
	require.NotNil(t, repo.claims[0].next, "scheduled execution advances its next due time")
	require.Len(t, seen, 3)
	first := <-seen
	require.Contains(t, first.RoundID, "group:7:scheduled:")
	for range 2 {
		require.Equal(t, first, <-seen, "parallel scheduled samples use one frozen question")
	}
	require.Len(t, repo.results, 3)
	for _, result := range repo.results {
		require.Equal(t, "success", result.Status)
		require.Equal(t, first, *result.PelicanConfig.PelicanQuestion)
	}
	require.Len(t, repo.finished, 1)
}
