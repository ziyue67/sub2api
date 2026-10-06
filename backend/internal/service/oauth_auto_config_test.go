package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type autoConfigGroups struct {
	GroupRepository
	group *Group
	err   error
}

func (s autoConfigGroups) GetByID(context.Context, int64) (*Group, error) { return s.group, s.err }
func TestOAuthAutoConfigInitialFieldsAndScope(t *testing.T) {
	cfg := DefaultOAuthAutoConfig()
	cfg.Enabled = true
	cfg.GroupIDs = []int64{2}
	cfg.Priority = 0
	cfg.LoadFactor = 90
	cfg.Concurrency = 7
	cfg.Revision = "r1"
	raw, _ := json.Marshal(cfg)
	settings := NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil)
	svc := &adminServiceImpl{settingService: settings, groupRepo: autoConfigGroups{group: &Group{ID: 2, Platform: PlatformOpenAI, Status: StatusActive}}}
	for _, tt := range []struct {
		kind, platform string
		apply          bool
	}{{AccountTypeOAuth, PlatformOpenAI, true}, {AccountTypeAPIKey, PlatformOpenAI, false}, {AccountTypeOAuth, PlatformAnthropic, false}} {
		in := &CreateAccountInput{Platform: tt.platform, Type: tt.kind, Priority: 50, Concurrency: 3, GroupIDs: []int64{1}, Extra: map[string]any{"keep": "unchanged"}}
		require.NoError(t, svc.ApplyOAuthAutoConfig(context.Background(), in))
		if tt.apply {
			require.Equal(t, 0, in.Priority)
			require.Equal(t, 7, in.Concurrency)
			require.Equal(t, 90, *in.LoadFactor)
			require.Equal(t, []int64{2}, in.GroupIDs)
		} else {
			require.Equal(t, 50, in.Priority)
			require.Equal(t, []int64{1}, in.GroupIDs)
		}
		require.Equal(t, "unchanged", in.Extra["keep"])
	}
	svc.groupRepo = autoConfigGroups{err: errors.New("deleted")}
	require.Error(t, svc.ApplyOAuthAutoConfig(context.Background(), &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.ErrorIs(t, svc.ApplyOAuthAutoConfig(context.Background(), nil), ErrAccountNilInput)
}
func TestAutoConfigIndependentSwitchesAndValidation(t *testing.T) {
	c := DefaultOAuthAutoConfig()
	c.UpgradeEnabled = true
	c.UpgradeGroupIDs = []int64{3}
	require.NoError(t, ValidateOAuthAutoConfig(c))
	c.Enabled = true
	require.Error(t, ValidateOAuthAutoConfig(c))
	c.GroupIDs = []int64{2}
	require.NoError(t, ValidateOAuthAutoConfig(c))
	c.GroupIDs = []int64{2, 2}
	require.Error(t, ValidateOAuthAutoConfig(c))
	c.GroupIDs = []int64{-1}
	require.Error(t, ValidateOAuthAutoConfig(c))
}

func TestAutoConfigInitialCostMultiplierScope(t *testing.T) {
	c := DefaultOAuthAutoConfig()
	c.Enabled, c.GroupIDs, c.CostMultiplier = true, []int64{2}, 0.07
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	svc := &adminServiceImpl{settingService: NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil), groupRepo: autoConfigGroups{group: &Group{ID: 2, Platform: PlatformOpenAI, Status: StatusActive}}}
	input := &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{AccountCostMultiplierExtraKey: 0.9}}
	require.NoError(t, svc.ApplyOAuthAutoConfig(t.Context(), input))
	require.Equal(t, 0.07, input.Extra[AccountCostMultiplierExtraKey])
	apiKey := &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{AccountCostMultiplierExtraKey: 0.9}}
	require.NoError(t, svc.ApplyOAuthAutoConfig(t.Context(), apiKey))
	require.Equal(t, 0.9, apiKey.Extra[AccountCostMultiplierExtraKey])
}
func TestAutoConfigAdvanceFailureCooldownCapAndManualChange(t *testing.T) {
	c := DefaultOAuthAutoConfig()
	c.Revision = "r1"
	c.SuccessesPerStep = 2
	c.UpgradeStep = 2
	c.MaxConcurrency = 4
	now := time.Now()
	good := AccountConcurrencyResult{StartedAt: now, Success: true}
	bad := good
	bad.Success = false
	state, n := AdvanceConcurrency(AutoConfigConcurrencyState{}, 3, c, good, now)
	require.Equal(t, 3, n)
	require.Equal(t, 1, state.Successes)
	state, n = AdvanceConcurrency(state, n, c, bad, now)
	require.Zero(t, state.Successes)
	state, n = AdvanceConcurrency(state, n, c, good, now)
	require.Zero(t, state.Successes)
	now = now.Add(61 * time.Second)
	good.StartedAt = now
	state, n = AdvanceConcurrency(state, n, c, good, now)
	state, n = AdvanceConcurrency(state, n, c, good, now)
	require.Equal(t, 4, n)
	require.Zero(t, state.Successes)
	require.NotNil(t, state.LastUpgradeAt)
	state, n = AdvanceConcurrency(state, n, c, good, now)
	require.Equal(t, 4, n)
	state, n = AdvanceConcurrency(state, 9, c, good, now)
	require.Equal(t, 9, n, "never decrease a manually raised concurrency")
	c.Revision = "r2"
	state, _ = AdvanceConcurrency(state, 3, c, good, now)
	require.Equal(t, 1, state.Successes)
	require.Equal(t, "r2", state.Revision)
}

func TestAutoConfigRecoveryRampStopsAtCapturedTarget(t *testing.T) {
	c := DefaultOAuthAutoConfig()
	c.Revision = "r1"
	c.SuccessesPerStep = 1
	c.UpgradeStep = 2
	c.MaxConcurrency = 100
	now := time.Now()
	state := AutoConfigConcurrencyState{Revision: "r1", Concurrency: 5, RecoveryTarget: 9}
	good := AccountConcurrencyResult{StartedAt: now, Success: true}
	state, n := AdvanceConcurrency(state, 5, c, good, now)
	require.Equal(t, 7, n)
	state.PausedUntil = now.Add(-time.Second)
	state, n = AdvanceConcurrency(state, 7, c, good, now)
	require.Equal(t, 9, n)
	require.Equal(t, 9, state.RecoveryTarget)
}

func TestAutoConfigPendingQualityFenceSurvivesCooldown(t *testing.T) {
	c := DefaultOAuthAutoConfig()
	c.Revision, c.SuccessesPerStep, c.MaxConcurrency = "r1", 1, 100
	now := time.Now()
	state := AutoConfigConcurrencyState{Revision: "r1", Concurrency: 5, RecoveryTarget: 5, PausedUntil: now.Add(-time.Hour)}
	state, n := AdvanceConcurrency(state, 5, c, AccountConcurrencyResult{StartedAt: now, Success: true}, now)
	require.Equal(t, 5, n)
	require.Equal(t, 5, state.RecoveryTarget)
}
func TestAutoConfigQueueOverflowFailsClosed(t *testing.T) {
	s := NewAccountOpsService(nil, nil, nil)
	c := DefaultOAuthAutoConfig()
	c.UpgradeEnabled = true
	s.autoConfig.Store(c)
	for i := 0; i < cap(s.autoResults)+1; i++ {
		s.ObserveConcurrencyResult(AccountConcurrencyResult{AccountID: 1})
	}
	require.True(t, s.AutoConfigBlocked())
	require.Equal(t, cap(s.autoResults), len(s.autoResults))
}
func TestAutoConfigSaveRejectsCrossPlatformGroups(t *testing.T) {
	s := NewAccountOpsService(&accountOpsSettingsStub{}, nil, nil)
	s.autoGroups = autoConfigGroups{group: &Group{Platform: PlatformAnthropic, Status: StatusActive}}
	c := DefaultOAuthAutoConfig()
	c.Enabled = true
	c.GroupIDs = []int64{2}
	_, err := s.SaveOAuthAutoConfig(context.Background(), c)
	require.Error(t, err)
}

type autoConfigAccountRepo struct {
	AccountRepository
	created *Account
	groups  []int64
}

func (r *autoConfigAccountRepo) Create(_ context.Context, a *Account) error {
	a.ID = 8
	r.created = a
	return nil
}
func (r *autoConfigAccountRepo) BindGroups(_ context.Context, _ int64, ids []int64) error {
	r.groups = ids
	return nil
}
func TestAutoConfigCRSNewAccountsOnly(t *testing.T) {
	cfg := DefaultOAuthAutoConfig()
	cfg.Enabled = true
	cfg.Priority = 7
	cfg.Concurrency = 9
	cfg.GroupIDs = []int64{5}
	raw, _ := json.Marshal(cfg)
	settings := NewSettingService(&accountOpsSettingsStub{raw: string(raw)}, nil)
	policy := &adminServiceImpl{settingService: settings, groupRepo: autoConfigGroups{group: &Group{ID: 5, Platform: PlatformOpenAI, Status: StatusActive}}}
	repo := &autoConfigAccountRepo{}
	syncer := &CRSSyncService{accountRepo: repo, autoConfigure: policy.ApplyOAuthAutoConfig}
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 50, Concurrency: 3, Status: StatusError, Schedulable: false}
	require.NoError(t, syncer.createSyncedAccount(context.Background(), a))
	require.Equal(t, 9, repo.created.Concurrency)
	require.Equal(t, 7, repo.created.Priority)
	require.Equal(t, []int64{5}, repo.groups)
	require.Equal(t, StatusError, repo.created.Status)
	require.False(t, repo.created.Schedulable)
}

func TestAutoConfigCanDisableAfterGroupDeleted(t *testing.T) {
	s := NewAccountOpsService(&accountOpsSettingsStub{}, nil, nil)
	s.autoGroups = autoConfigGroups{err: ErrGroupNotFound}
	cfg := DefaultOAuthAutoConfig()
	cfg.GroupIDs = []int64{5}
	cfg.UpgradeGroupIDs = []int64{6}
	_, err := s.SaveOAuthAutoConfig(context.Background(), cfg)
	require.NoError(t, err, "disabling must remain possible after a target group is deleted")
}

func TestAutoConfigImportedMarkerCannotCreateHistory(t *testing.T) {
	original := map[string]any{"auto_config_initial_revision": "imported", "keep": true}
	input := &CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: original}
	svc := &adminServiceImpl{}
	require.NoError(t, svc.ApplyOAuthAutoConfig(context.Background(), input))
	require.NotContains(t, input.Extra, "auto_config_initial_revision")
	require.Equal(t, true, input.Extra["keep"])
	require.Equal(t, "imported", original["auto_config_initial_revision"], "do not mutate the caller's map")
}

type autoConfigHistoryAccountRepo struct {
	*autoConfigAccountRepo
	recorded int
}

func (r *autoConfigHistoryAccountRepo) RecordAutoConfigInitial(_ context.Context, _ *Account, _ []int64) error {
	r.recorded++
	return nil
}
func TestAutoConfigInitialHistoryOnlyAfterNewAccount(t *testing.T) {
	repo := &autoConfigHistoryAccountRepo{autoConfigAccountRepo: &autoConfigAccountRepo{}}
	a := &Account{ID: 7, Extra: map[string]any{"keep": true}}
	recordAutoConfigInitial(context.Background(), repo, a, []int64{3})
	require.Zero(t, repo.recorded)
	a.Extra["auto_config_initial_revision"] = "r1"
	recordAutoConfigInitial(context.Background(), repo, a, []int64{3})
	require.Equal(t, 1, repo.recorded)
}
