package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func presetInput() *QualityRulePreset {
	return &QualityRulePreset{Name: "Reusable check", ModelID: "test-model", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 100,
		PelicanConfig: &PelicanTestConfig{QuestionKind: "candy", Prompt: "Configured question", ReasoningEffort: "high", ParallelCount: 2,
			Quality: &QualityPolicy{ExpectedAnswer: "42", Action: "disable_scheduling", AutoRestore: true}}}
}

func TestQualityPresetSavesOnlyReusableConfiguration(t *testing.T) {
	plans := newTemplatePlanRepo()
	repo := newFakeTemplateRepo(plans, QualityTemplateAccount{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	// No plan/result/account service: any accidental dispatch must fail this test.
	svc := ProvideScheduledTestService(nil, nil, repo, nil)
	input := presetInput()
	input.Name = "  Saved template  "
	input.ID = 99
	input.PelicanConfig.RuleName = "Source rule"
	input.PelicanConfig.ModelID = "result-only"
	input.PelicanConfig.TriggerSource = "upstream_5xx"
	input.PelicanConfig.QualityModelActions = map[string]string{"test-model": "cooled"}
	input.PelicanConfig.QualityModelOutcomes = map[string]string{"test-model": "failed"}
	input.PelicanConfig.QualityRestoreDiagnostic = &QualityRestoreDiagnostic{}
	saved, err := svc.CreateQualityPreset(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, int64(1), saved.ID)
	require.Equal(t, "Saved template", saved.Name)
	require.Empty(t, saved.PelicanConfig.RuleName)
	require.Empty(t, saved.PelicanConfig.ModelID)
	require.Empty(t, saved.PelicanConfig.TriggerSource)
	require.Nil(t, saved.PelicanConfig.QualityModelActions)
	require.Nil(t, saved.PelicanConfig.QualityModelOutcomes)
	require.Nil(t, saved.PelicanConfig.QualityRestoreDiagnostic)
	require.Equal(t, "Configured question", saved.PelicanConfig.Prompt)
	require.True(t, saved.PelicanConfig.Quality.AutoRestore)
	require.Empty(t, repo.templates)
	require.Empty(t, plans.plans)
	require.Empty(t, repo.filters)
	saved.PelicanConfig.Quality.ExpectedAnswer = "changed copy"
	require.Equal(t, "42", input.PelicanConfig.Quality.ExpectedAnswer)
	listed, err := svc.ListQualityPresets(context.Background())
	require.NoError(t, err)
	require.Len(t, listed, 1)
}

func TestQualityPresetValidatesNameAndPlanConfiguration(t *testing.T) {
	for _, change := range []func(*QualityRulePreset){
		func(p *QualityRulePreset) { p.Name = " " },
		func(p *QualityRulePreset) { p.Name = strings.Repeat("模", 101) },
		func(p *QualityRulePreset) { p.CronExpression = "invalid" },
		func(p *QualityRulePreset) { p.PelicanConfig.ParallelCount = 9 },
		func(p *QualityRulePreset) { p.PelicanConfig.Quality.Action = QualityActionOpenAITwoFAAdmission },
	} {
		repo := newFakeTemplateRepo(newTemplatePlanRepo())
		svc := ProvideScheduledTestService(nil, nil, repo, nil)
		input := presetInput()
		change(input)
		_, err := svc.CreateQualityPreset(context.Background(), input)
		require.Error(t, err)
		require.Empty(t, repo.presets)
	}
}

func TestQualityPresetKeepsBPSOptionsAndDropsGeneratedQuestionData(t *testing.T) {
	repo := newFakeTemplateRepo(newTemplatePlanRepo())
	svc := ProvideScheduledTestService(nil, nil, repo, nil)
	input := presetInput()
	input.Name = strings.Repeat("模", 100)
	input.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
	input.PelicanConfig.ParallelCount = 1
	interval := 95
	input.PelicanConfig.Quality.Action = QualityActionEnableBPS
	input.PelicanConfig.Quality.BPS = &QualityBPSPolicy{FailureThreshold: 4, UsagePercent: 85, RequireAll: true,
		Models: []string{"bps-model"}, PassThreshold: 6, HoldOnUsage: true, AutoDisableOn403: true,
		AutoRecoverOn403: true, RecoveryIntervalMinutes: &interval, AutoMoveOn403: true, TargetGroupID: 21,
		SessionProxy: true, ProxySource: "ip_pool", CacheCreationAsInput: true}
	saved, err := svc.CreateQualityPreset(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, input.PelicanConfig.Quality.BPS, saved.PelicanConfig.Quality.BPS)
	saved.PelicanConfig.Quality.BPS.Models[0] = "changed-model"
	*saved.PelicanConfig.Quality.BPS.RecoveryIntervalMinutes = 60
	require.Equal(t, "bps-model", input.PelicanConfig.Quality.BPS.Models[0])
	require.Equal(t, 95, interval)

	input = presetInput()
	input.PelicanConfig.QuestionKind = QualityRandomQuestionKind
	input.PelicanConfig.RandomQuestion = &QualityRandomQuestion{}
	saved, err = svc.CreateQualityPreset(context.Background(), input)
	require.NoError(t, err)
	require.Nil(t, saved.PelicanConfig.RandomQuestion)
	require.Empty(t, saved.PelicanConfig.Prompt)
	require.Empty(t, saved.PelicanConfig.Quality.ExpectedAnswer)
	require.Equal(t, "Configured question", input.PelicanConfig.Prompt)
}

func TestQualityTemplateCompleteLegacyPresetDefaultsToObservation(t *testing.T) {
	repo := newFakeTemplateRepo(newTemplatePlanRepo())
	svc := ProvideScheduledTestService(nil, nil, repo, nil)
	input := presetInput()
	input.PelicanConfig.Quality.Action = ""
	input.PelicanConfig.Quality.RemoveGroupIDs = []int64{21}
	input.PelicanConfig.Quality.AutoRestore = true
	saved, err := svc.CreateQualityPreset(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, QualityActionObserveOnly, saved.PelicanConfig.Quality.Action)
	require.Empty(t, saved.PelicanConfig.Quality.RemoveGroupIDs)
	require.False(t, saved.PelicanConfig.Quality.AutoRestore)
	// Legacy stored rows pass through the same safe read normalization, while the
	// source stays unchanged (fake repositories may return pointers they retain).
	legacy := presetInput()
	legacy.PelicanConfig.Quality.Action = ""
	legacy.ID = 42
	repo.presets = append(repo.presets, legacy)
	listed, err := svc.ListQualityPresets(context.Background())
	require.NoError(t, err)
	require.Equal(t, QualityActionObserveOnly, listed[len(listed)-1].PelicanConfig.Quality.Action)
	require.Empty(t, repo.templates)
}

func TestQualityTemplateCompletePolicyWhitelistAndDependencies(t *testing.T) {
	for _, action := range []string{QualityActionObserveOnly, "remove_groups", QualityActionIsolateModels, "disable_scheduling", QualityActionRemoveModel, QualityActionEnableBPS} {
		t.Run(action, func(t *testing.T) {
			input := presetInput()
			q := input.PelicanConfig.Quality
			q.Action = action
			q.RemoveGroupIDs = []int64{21}
			q.RemoveModels = []string{"test-model"}
			q.RecoveryConcurrency = 9
			q.TriggerOnUpstream5xx = true
			q.Judge = &QualityJudgeConfig{Kind: "jev", Prompt: "Grade", GroupID: 0}
			if action == QualityActionEnableBPS {
				input.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
				input.PelicanConfig.ParallelCount = 1
				q.BPS = &QualityBPSPolicy{FailureThreshold: 2, AllModels: true, PassThreshold: 3}
			}
			input.PelicanConfig.ModelIDs = []string{"test-model"}
			input.PelicanConfig.TriggerSource = "must-not-copy"
			input.PelicanConfig.QualityModelOutcomes = map[string]string{"test-model": "failed"}
			repo := newFakeTemplateRepo(newTemplatePlanRepo())
			svc := ProvideScheduledTestService(nil, nil, repo, nil)
			saved, err := svc.CreateQualityPreset(context.Background(), input)
			require.NoError(t, err)
			cfg := saved.PelicanConfig
			require.Equal(t, action, cfg.Quality.Action)
			require.Empty(t, cfg.TriggerSource)
			require.Nil(t, cfg.QualityModelOutcomes)
			require.Equal(t, action != "observe_only", cfg.Quality.AutoRestore)
			if action == "remove_groups" || action == QualityActionIsolateModels {
				require.Equal(t, []int64{21}, cfg.Quality.RemoveGroupIDs)
			} else {
				require.Empty(t, cfg.Quality.RemoveGroupIDs)
			}
			if action != QualityActionRemoveModel {
				require.Empty(t, cfg.Quality.RemoveModels)
				require.Zero(t, cfg.Quality.RecoveryConcurrency)
			}
			if action == QualityActionEnableBPS {
				require.Nil(t, cfg.Quality.Judge)
			} else {
				require.Equal(t, "jev", cfg.Quality.Judge.Kind)
			}
		})
	}
}
