package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type candyConfiguredPlanRepo struct{ *templatePlanRepo }

func (r *candyConfiguredPlanRepo) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	stored, ok := r.add(plan)
	if !ok {
		return nil, errors.New("duplicate quality rule")
	}
	return stored, nil
}

func TestCandyConfiguredModelsCreateEditAndTemplateAcrossAccountTypes(t *testing.T) {
	for _, tc := range []struct{ platform, accountType string }{
		{PlatformOpenAI, AccountTypeAPIKey},
		{PlatformOpenAI, AccountTypeOAuth},
		{PlatformOpenAI, AccountTypeSetupToken},
		{PlatformAnthropic, AccountTypeAPIKey},
		{PlatformAnthropic, AccountTypeOAuth},
		{PlatformGemini, AccountTypeAPIKey},
		{PlatformGemini, AccountTypeOAuth},
		{PlatformGrok, AccountTypeAPIKey},
	} {
		t.Run(tc.platform+"/"+tc.accountType, func(t *testing.T) {
			account := &Account{ID: 42, Platform: tc.platform, Type: tc.accountType,
				Credentials: map[string]any{"model_mapping": map[string]any{
					"gpt-6-astra": "gpt-6-astra", "gpt-6.1-sol": "upstream-sol",
				}}}
			plans := &candyConfiguredPlanRepo{newTemplatePlanRepo()}
			templates := newFakeTemplateRepo(plans.templatePlanRepo, QualityTemplateAccount{ID: 42, Platform: tc.platform, Type: tc.accountType})
			svc := ProvideScheduledTestService(plans, &pelicanResults{}, templates,
				&AccountTestService{accountRepo: &qualitySupportAccounts{account: account}})
			svc.qualityModels = func(context.Context, int64) ([]string, error) {
				t.Fatal("configured candy models must not depend on upstream discovery")
				return nil, nil
			}
			ctx := context.Background()
			plan := multiModelQualityPlan(t, `["gpt-6-astra"]`)
			plan.AccountID = account.ID
			created, err := svc.CreatePlan(ctx, plan)
			require.NoError(t, err)
			created.Enabled = true
			created.PelicanConfig.ModelIDs = []string{"gpt-6-astra", "gpt-6.1-sol"}
			updated, err := svc.UpdatePlan(ctx, created)
			require.NoError(t, err)
			require.Equal(t, []string{"gpt-6-astra", "gpt-6.1-sol"}, updated.PelicanConfig.ModelIDs)
			supported, unsupported, err := svc.supportedQualityModels(ctx, updated)
			require.NoError(t, err)
			require.Equal(t, updated.PelicanConfig.ModelIDs, supported)
			require.Empty(t, unsupported)

			require.NoError(t, svc.DeletePlan(ctx, updated.ID))
			template := &QualityRuleTemplate{ModelID: updated.ModelID, PelicanConfig: updated.PelicanConfig,
				CronExpression: updated.CronExpression, Enabled: true, MaxResults: 100}
			_, count, err := svc.CreateQualityTemplate(ctx, template)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			require.Equal(t, supported, plansByAccount(plans.templatePlanRepo)[account.ID].PelicanConfig.ModelIDs)
		})
	}
}

func TestCandyConfiguredModelsRetainDiscoveryForOtherModels(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"configured-*": "upstream"}}}
	svc := ProvideScheduledTestService(nil, nil, nil, &AccountTestService{accountRepo: &qualitySupportAccounts{account: account}})
	calls := 0
	svc.qualityModels = func(context.Context, int64) ([]string, error) {
		calls++
		return []string{"discovered-model"}, nil
	}
	plan := multiModelQualityPlan(t, `["configured-model", "discovered-model", "absent-model"]`)
	supported, unsupported, err := svc.supportedQualityModels(context.Background(), plan)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []string{"configured-model", "discovered-model"}, supported)
	require.Equal(t, []string{"absent-model"}, unsupported)

	// An explicit runtime rejection still overrides a configured wildcard.
	account.Extra = map[string]any{qualityUnsupportedModelKey("configured-model"): "configured-model"}
	plan.PelicanConfig.ModelIDs = []string{"configured-model"}
	supported, unsupported, err = svc.supportedQualityModels(context.Background(), plan)
	require.NoError(t, err)
	require.Empty(t, supported)
	require.Equal(t, []string{"configured-model"}, unsupported)
}

func TestCandyConfiguredModelsDoNotOverrideOtherRoutes(t *testing.T) {
	for _, route := range []string{"state_probe", "bps_observation", "bps_account", "passthrough", "unmapped"} {
		t.Run(route, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}}, Extra: map[string]any{}}
			plan := multiModelQualityPlan(t, `["gpt-6.1-sol"]`)
			switch route {
			case "state_probe":
				plan.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
			case "bps_observation":
				plan.PelicanConfig.TestChannel = "bps"
			case "bps_account":
				account.Extra["openai_excel_bps"] = true
			case "passthrough":
				account.Extra["openai_passthrough"] = true
			case "unmapped":
				account.Credentials = nil
			}
			svc := ProvideScheduledTestService(nil, nil, nil, &AccountTestService{accountRepo: &qualitySupportAccounts{account: account}})
			calls := 0
			svc.qualityModels = func(context.Context, int64) ([]string, error) {
				calls++
				return []string{"gpt-6-astra"}, nil
			}
			supported, unsupported, err := svc.supportedQualityModels(context.Background(), plan)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Empty(t, supported)
			require.Equal(t, []string{"gpt-6.1-sol"}, unsupported)
		})
	}
}
