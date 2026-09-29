package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExcelBPSDefaultsLegacyAndSaveRoundTrip(t *testing.T) {
	repo := &accountOpsSettingsStub{raw: `{"enabled":false,"platform":"openai"}`}
	c, err := GetOAuthAutoConfig(t.Context(), repo)
	require.NoError(t, err)
	require.Equal(t, DefaultExcelBPSDefaults(), c.ExcelBPS)
	c.ExcelBPS.Models = []string{"custom-model"}
	c.ExcelBPS.SessionProxy = true
	c.ExcelBPS.ProxySource = "ip_pool"
	s := NewAccountOpsService(repo, nil, nil)
	s.autoGroups = autoConfigGroups{}
	saved, err := s.SaveOAuthAutoConfig(t.Context(), c)
	require.NoError(t, err)
	loaded, err := GetOAuthAutoConfig(t.Context(), repo)
	require.NoError(t, err)
	require.Equal(t, saved.ExcelBPS, loaded.ExcelBPS)
	require.NotEmpty(t, loaded.Revision)
}

func TestExcelBPSDefaultsValidateAndTranslate(t *testing.T) {
	for _, mutate := range []func(*ExcelBPSDefaults){
		func(b *ExcelBPSDefaults) { b.Models = nil },
		func(b *ExcelBPSDefaults) { b.Models = []string{" "} },
		func(b *ExcelBPSDefaults) { b.RecoveryIntervalMinutes = 0 },
		func(b *ExcelBPSDefaults) { b.RecoveryIntervalMinutes = 10081 },
		func(b *ExcelBPSDefaults) { b.ProxySource = "unknown" },
		func(b *ExcelBPSDefaults) { b.AutoDisableOn403 = false; b.AutoRecoverOn403 = true },
		func(b *ExcelBPSDefaults) { b.AutoMoveOn403 = true; b.TargetGroupID = -1 },
	} {
		c := DefaultOAuthAutoConfig()
		mutate(&c.ExcelBPS)
		require.Error(t, ValidateOAuthAutoConfig(c))
	}
	b := DefaultExcelBPSDefaults()
	b.Models = []string{" custom-model ", "custom-model"}
	b.SessionProxy, b.AutoRecoverOn403, b.AutoMoveOn403 = true, true, true
	b.ProxySource, b.TargetGroupID, b.RecoveryIntervalMinutes = "ip_pool", 0, 15
	require.NoError(t, validateExcelBPSDefaults(b))
	extra := b.extra()
	require.Equal(t, []string{"custom-model"}, extra["openai_excel_bps_models"])
	require.Equal(t, "ip_pool", extra["openai_excel_bps_proxy_source"])
	require.Equal(t, int64(0), extra["openai_excel_bps_403_target_group_id"])
	require.Equal(t, 15, extra[ExcelBPS403RecoveryIntervalMinutesKey])
	b.AllModels = true
	require.NotContains(t, b.extra(), "openai_excel_bps_models")
}

func TestExcelBPSDefaultsNeverEnableNewAccounts(t *testing.T) {
	for _, configureFields := range []bool{false, true} {
		c := DefaultOAuthAutoConfig()
		c.Enabled, c.GroupIDs = configureFields, []int64{2}
		c.ExcelBPS.Models = []string{"custom-template-model"}
		raw, err := json.Marshal(c)
		require.NoError(t, err)
		svc := &adminServiceImpl{settingService: NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil), groupRepo: autoConfigGroups{group: &Group{ID: 2, Platform: PlatformOpenAI, Status: StatusActive}}}
		in := &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"keep": "original"}}
		require.NoError(t, svc.ApplyOAuthAutoConfig(t.Context(), in))
		require.NotContains(t, in.Extra, "openai_excel_bps")
		require.NotContains(t, in.Extra, "openai_excel_bps_models")
		require.Equal(t, "original", in.Extra["keep"])
	}
}
