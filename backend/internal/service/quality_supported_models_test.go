package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestQualitySupportedModelsRejectSingleAccountConfiguration(t *testing.T) {
	plans := &templatePlanRepo{plans: map[int64]*ScheduledTestPlan{}}
	svc := NewScheduledTestService(plans, &pelicanResults{})
	svc.qualityModels = func(context.Context, int64) ([]string, error) { return []string{"gpt-6-sol"}, nil }
	plan := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
	_, err := svc.CreatePlan(context.Background(), plan)
	require.ErrorContains(t, err, "gpt-6-astra")
	require.Empty(t, plans.plans)
}
func TestQualitySupportedModelsFilterTemplatePerAccount(t *testing.T) {
	plans := &templatePlanRepo{plans: map[int64]*ScheduledTestPlan{}}
	repo := newFakeTemplateRepo(plans, QualityTemplateAccount{ID: 42})
	svc := NewScheduledTestService(plans, &pelicanResults{})
	svc.templateRepo = repo
	svc.qualityModels = func(context.Context, int64) ([]string, error) { return []string{"gpt-6-sol"}, nil }
	plan := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
	tpl := &QualityRuleTemplate{ModelID: plan.ModelID, PelicanConfig: plan.PelicanConfig, CronExpression: plan.CronExpression, Enabled: true, MaxResults: 100}
	saved, n, err := svc.CreateQualityTemplate(context.Background(), tpl)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"gpt-6-astra", "gpt-6-sol"}, saved.PelicanConfig.ModelIDs)
	for _, p := range plans.plans {
		require.Equal(t, []string{"gpt-6-sol"}, p.PelicanConfig.ModelIDs)
		require.Equal(t, "gpt-6-sol", p.ModelID)
	}
}
func TestQualitySupportedModelsCatalogFailureDoesNotMeanUnsupported(t *testing.T) {
	svc := NewScheduledTestService(nil, nil)
	svc.qualityModels = func(context.Context, int64) ([]string, error) { return nil, errors.New("catalog timeout") }
	_, err := svc.CreatePlan(context.Background(), multiModelQualityPlan(t, `["gpt-6-astra"]`))
	require.ErrorContains(t, err, "catalog timeout")
}
func TestQualitySupportedModelsSkipDoesNotRestoreOrQuarantine(t *testing.T) {
	plans := &qualityPlanRepo{}
	saved := &pelicanResults{}
	svc := NewScheduledTestService(plans, saved)
	svc.qualityModels = func(context.Context, int64) ([]string, error) { return []string{}, nil }
	runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: svc}
	runner.runPelican = func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
		t.Fatal("unsupported model must not be called")
		return nil, nil
	}
	plan := multiModelQualityPlan(t, `["gpt-6-astra"]`)
	require.True(t, runner.runOnePlan(context.Background(), plan))
	require.Empty(t, plans.outcomes)
	require.True(t, plans.finished)
	require.Equal(t, "skipped", saved.results[0].Status)
}
func TestQualityUnsupportedResponseClassification(t *testing.T) {
	for _, message := range []string{"API returned 400: {\"error\":{\"code\":\"model_not_found\"}}", "This model is not supported", "The model A does not exist"} {
		require.True(t, qualityModelUnsupported(message))
	}
	for _, message := range []string{"API returned 503: overloaded", "context deadline exceeded", "unsupported reasoning effort", "API returned 401: unauthorized"} {
		require.False(t, qualityModelUnsupported(message))
	}
	passed := &ScheduledTestResult{Status: "success", QualityJudgment: &QualityJudgment{Verdict: "correct"}}
	skip := &ScheduledTestResult{Status: "skipped", ErrorMessage: "model_unsupported"}
	require.Equal(t, "passed", qualityRoundOutcome([]*ScheduledTestResult{skip, passed}, true))
	require.Equal(t, "inconclusive", qualityRoundOutcome([]*ScheduledTestResult{skip}, true))
}

type qualitySupportAccounts struct {
	AccountRepository
	account *Account
}

func (r *qualitySupportAccounts) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *qualitySupportAccounts) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.account.Extra == nil {
		r.account.Extra = map[string]any{}
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}
func TestQualitySupportedModelsRememberRuntimeRejectionAcrossRules(t *testing.T) {
	accounts := &qualitySupportAccounts{account: &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "a", "gpt-6-sol": "b"}}}}
	plans := &qualityPlanRepo{}
	saved := &pelicanResults{}
	svc := NewScheduledTestService(plans, saved)
	svc.accountTests = &AccountTestService{accountRepo: accounts}
	svc.qualityModels = svc.accountQualityModels
	runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: svc}
	runner.runPelican = func(_ context.Context, _ int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		if model == "gpt-6-astra" {
			return nil, errors.New("API returned 400: model_not_supported")
		}
		return &ScheduledTestResult{Status: "success", ResponseText: "21", PelicanConfig: cfg}, nil
	}
	runner.judgeQuality = func(context.Context, int64, *PelicanTestConfig, string) *QualityJudgment {
		return &QualityJudgment{Verdict: "correct"}
	}
	require.True(t, runner.runOnePlan(context.Background(), multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)))
	require.Equal(t, "gpt-6-astra", accounts.account.Extra[qualityUnsupportedModelKey("gpt-6-astra")])
	require.Equal(t, []string{"passed"}, plans.outcomes)
	catalog, err := svc.accountQualityModels(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-sol"}, catalog)
	next := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
	ok, err := svc.filterQualityTemplatePlan(context.Background(), next)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"gpt-6-sol"}, next.PelicanConfig.ModelIDs)
	plans.claimed = false
	runner.runPelican = func(_ context.Context, _ int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		require.Equal(t, "gpt-6-sol", model)
		return &ScheduledTestResult{Status: "success", ResponseText: "21", PelicanConfig: cfg}, nil
	}
	require.True(t, runner.runOnePlan(context.Background(), multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)))
}

func TestQualitySupportedModelsWildcardDoesNotReenableRejectedModel(t *testing.T) {
	accounts := &qualitySupportAccounts{account: &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-*": "*"}}, Extra: map[string]any{qualityUnsupportedModelKey("gpt-6-astra"): "gpt-6-astra"}}}
	svc := NewScheduledTestService(nil, nil)
	svc.accountTests = &AccountTestService{accountRepo: accounts}
	svc.qualityModels = svc.accountQualityModels
	supported, unsupported, err := svc.supportedQualityModels(context.Background(), multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`))
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-sol"}, supported)
	require.Equal(t, []string{"gpt-6-astra"}, unsupported)
}
func TestQualitySupportedModelsHistoryExcludesSkippedSamples(t *testing.T) {
	for _, passed := range []int{0, 1} {
		item := &QualityHistoryResult{PassedCount: passed, TotalCount: 2, SkippedCount: 2 - passed}
		item.PelicanConfig = multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`).PelicanConfig
		svc := NewScheduledTestService(nil, &multiModelHistoryRepo{item: item})
		page, err := svc.ListQualityHistory(context.Background(), 0)
		require.NoError(t, err)
		require.Equal(t, passed, page.Items[0].TotalCount)
		if passed == 0 {
			require.Equal(t, "skipped", page.Items[0].Status)
		} else {
			require.Equal(t, "success", page.Items[0].Status)
		}
	}
}

func TestQualityModelVerdictsDoNotSpreadToHealthyOrUntestedPeers(t *testing.T) {
	results := []*ScheduledTestResult{
		{Status: "failed", ErrorMessage: "answer_mismatch", PelicanConfig: &PelicanTestConfig{ModelID: "gpt-6-astra"}, QualityJudgment: &QualityJudgment{Verdict: "incorrect"}},
		{Status: "success", PelicanConfig: &PelicanTestConfig{ModelID: "gpt-6.1-sol"}, QualityJudgment: &QualityJudgment{Verdict: "correct"}},
	}
	require.Equal(t, map[string]string{"gpt-6-astra": "failed", "gpt-6.1-sol": "passed", "missing": "skipped"}, qualityModelOutcomes(results, []string{"gpt-6-astra", "gpt-6.1-sol", "missing"}))
	results[0], results[1] = results[1], results[0]
	require.Equal(t, "passed", qualityModelOutcomes(results, []string{"gpt-6.1-sol"})["gpt-6.1-sol"])
}

func TestQualitySupportedModelsTemplateSkipsAccountWithNoSupportedModels(t *testing.T) {
	plans := newTemplatePlanRepo()
	repo := newFakeTemplateRepo(plans, QualityTemplateAccount{ID: 42})
	svc := NewScheduledTestService(plans, &pelicanResults{})
	svc.templateRepo = repo
	svc.qualityModels = func(context.Context, int64) ([]string, error) { return []string{"another-model"}, nil }
	plan := multiModelQualityPlan(t, `["gpt-6-astra", "gpt-6-sol"]`)
	tpl := &QualityRuleTemplate{ModelID: plan.ModelID, PelicanConfig: plan.PelicanConfig, CronExpression: plan.CronExpression, Enabled: true, MaxResults: 100}
	_, created, err := svc.CreateQualityTemplate(context.Background(), tpl)
	require.NoError(t, err)
	require.Zero(t, created)
	require.Empty(t, plans.plans)
}
