package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOAuthAutoConfigObserverKeepsExplicitConfiguration(t *testing.T) {
	for _, defaults := range [][]int64{{2}, {25}} {
		t.Run(fmt.Sprint(defaults), func(t *testing.T) {
			cfg := DefaultOAuthAutoConfig()
			cfg.Enabled = true
			cfg.GroupIDs = defaults
			cfg.Concurrency = 9
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			svc := &adminServiceImpl{
				settingService: NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil),
				groupRepo:      autoConfigGroups{group: &Group{Platform: PlatformOpenAI, Status: StatusActive}},
			}
			originalExtra := map[string]any{"auto_config_initial_revision": "imported", AccountCostMultiplierExtraKey: 0.9}
			input := &CreateAccountInput{
				Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				GroupIDs: []int64{25}, Priority: 42, Concurrency: 3,
				Credentials: map[string]any{"model_mapping": map[string]any{"custom-model": "custom-target"}},
				Extra:       originalExtra,
			}
			require.NoError(t, svc.ApplyOAuthAutoConfig(WithObserverScope(t.Context(), []int64{25}), input))
			require.Equal(t, []int64{25}, input.GroupIDs)
			require.Equal(t, 42, input.Priority)
			require.Equal(t, 3, input.Concurrency)
			require.Nil(t, input.LoadFactor)
			require.Equal(t, map[string]any{"model_mapping": map[string]any{"custom-model": "custom-target"}}, input.Credentials)
			require.Equal(t, 0.9, input.Extra[AccountCostMultiplierExtraKey])
			require.NotContains(t, input.Extra, "auto_config_initial_revision")
			require.Equal(t, "imported", originalExtra["auto_config_initial_revision"])
		})
	}
}

func TestCreateAccountObserverRejectsInvalidGroupsWithAutoConfig(t *testing.T) {
	cfg := DefaultOAuthAutoConfig()
	cfg.Enabled = true
	cfg.GroupIDs = []int64{25}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	svc := &adminServiceImpl{
		settingService: NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil),
		groupRepo:      autoConfigGroups{group: &Group{Platform: PlatformOpenAI, Status: StatusActive}},
	}
	for _, groups := range [][]int64{nil, {2}, {25, 2}} {
		// No account repository: a rejected request must not reach persistence.
		_, err := svc.CreateAccount(WithObserverScope(t.Context(), []int64{25}), &CreateAccountInput{
			Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: groups,
		})
		require.ErrorIs(t, err, ErrObserverScope)
	}
}

func TestCreateAccountObserverBindsSelectedGroupWithAutoConfig(t *testing.T) {
	cfg := DefaultOAuthAutoConfig()
	cfg.Enabled = true
	cfg.GroupIDs = []int64{2}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &autoConfigAccountRepo{}
	svc := &adminServiceImpl{
		settingService: NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil),
		accountRepo:    repo,
	}
	created, err := svc.CreateAccount(WithObserverScope(t.Context(), []int64{25}), &CreateAccountInput{
		Name: "observer-import", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		GroupIDs: []int64{25}, Concurrency: 3,
		SkipDefaultGroupBind: true, SkipMixedChannelCheck: true,
	})
	require.NoError(t, err)
	require.Same(t, repo.created, created)
	require.Equal(t, []int64{25}, repo.groups)
	require.NotContains(t, created.Extra, "auto_config_initial_revision")
}
