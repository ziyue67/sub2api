package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func initialQualityRuleFixture() *OAuthInitialQualityRule {
	return &OAuthInitialQualityRule{ModelID: "gpt-5.4", CronExpression: "*/5 * * * *", Enabled: true, MaxResults: 100,
		PelicanConfig: &PelicanTestConfig{QuestionKind: "state_probe", ParallelCount: 1, ReasoningEffort: "high", Quality: &QualityPolicy{Action: QualityActionObserveOnly}}}
}

func TestOAuthAutoConfigQualityScopeAndCopies(t *testing.T) {
	for _, tc := range []struct {
		name, platform, kind    string
		enabled, observer, want bool
	}{
		{"new oauth", PlatformOpenAI, AccountTypeOAuth, true, false, true},
		{"disabled", PlatformOpenAI, AccountTypeOAuth, false, false, false},
		{"other platform", PlatformAnthropic, AccountTypeOAuth, true, false, false},
		{"api key", PlatformOpenAI, AccountTypeAPIKey, true, false, false},
		{"observer", PlatformOpenAI, AccountTypeOAuth, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultOAuthAutoConfig()
			c.Enabled = tc.enabled
			c.GroupIDs = []int64{2}
			c.QualityRule = initialQualityRuleFixture()
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			svc := &adminServiceImpl{settingService: NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil), groupRepo: autoConfigGroups{group: &Group{ID: 2, Platform: PlatformOpenAI, Status: StatusActive}}}
			ctx := t.Context()
			if tc.observer {
				ctx = WithObserverScope(ctx, []int64{2})
			}
			in := &CreateAccountInput{Platform: tc.platform, Type: tc.kind, GroupIDs: []int64{2}, InitialQualityPlan: &ScheduledTestPlan{ID: 999}}
			require.NoError(t, svc.ApplyOAuthAutoConfig(ctx, in))
			if !tc.want {
				require.Nil(t, in.InitialQualityPlan)
				return
			}
			p := in.InitialQualityPlan
			require.NotNil(t, p)
			require.Zero(t, p.ID)
			require.Zero(t, p.AccountID)
			require.NotNil(t, p.NextRunAt)
			require.True(t, p.Enabled)
			require.Equal(t, QualityActionObserveOnly, p.PelicanConfig.Quality.Action)
			a, err := buildAccountForCreate(in, in.Extra)
			require.NoError(t, err)
			require.Same(t, p, a.InitialQualityPlan)
			p.PelicanConfig.Quality.Action = "disable_scheduling"
			require.NoError(t, svc.ApplyOAuthAutoConfig(ctx, in))
			require.Equal(t, QualityActionObserveOnly, in.InitialQualityPlan.PelicanConfig.Quality.Action)
		})
	}
}

func TestOAuthAutoConfigQualityValidation(t *testing.T) {
	c := DefaultOAuthAutoConfig()
	c.QualityRule = initialQualityRuleFixture()
	require.NoError(t, ValidateOAuthAutoConfig(c))
	c.Platform = PlatformAnthropic
	require.Error(t, ValidateOAuthAutoConfig(c))
	c.Platform = PlatformOpenAI
	c.QualityRule.CronExpression = "invalid"
	require.Error(t, ValidateOAuthAutoConfig(c))
	c.QualityRule = initialQualityRuleFixture()
	c.QualityRule.PelicanConfig.Quality = nil
	require.Error(t, ValidateOAuthAutoConfig(c))
	c.QualityRule = nil
	require.NoError(t, ValidateOAuthAutoConfig(c))
}

func TestOAuthAutoConfigQualitySaveSnapshot(t *testing.T) {
	s := NewAccountOpsService(&accountOpsSettingsStub{}, nil, nil)
	s.autoGroups = autoConfigGroups{}
	c := DefaultOAuthAutoConfig()
	c.QualityRule = initialQualityRuleFixture()
	c.QualityRule.PelicanConfig.TriggerSource = "upstream_5xx"
	c.QualityRule.PelicanConfig.QualityModelOutcomes = map[string]string{"gpt-5.4": "failed"}
	c.QualityRule.Enabled = false
	saved, err := s.SaveOAuthAutoConfig(t.Context(), c)
	require.NoError(t, err)
	require.False(t, saved.QualityRule.Enabled)
	require.Empty(t, saved.QualityRule.PelicanConfig.TriggerSource)
	require.Empty(t, saved.QualityRule.PelicanConfig.QualityModelOutcomes)
	require.NotSame(t, c.QualityRule.PelicanConfig, saved.QualityRule.PelicanConfig)
}
