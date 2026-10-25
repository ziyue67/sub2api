package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type accountOpsBatchAccounts struct {
	AccountRepository
	items         []Account
	err           error
	waitForCancel bool
	calls         int
	ids           []int64
}

func (r *accountOpsBatchAccounts) ListAccountOpsSnapshots(ctx context.Context, ids []int64) ([]Account, error) {
	r.calls++
	r.ids = append([]int64(nil), ids...)
	if r.waitForCancel {
		<-ctx.Done()
		return r.items, ctx.Err()
	}
	return r.items, r.err
}

func accountOpsBatchBalanceAccount(id int64, balance float64) Account {
	now := time.Now()
	until := now.Add(time.Hour)
	return Account{ID: id, Name: "key", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{
		UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", Balance: &UpstreamBalanceSnapshot{
			Status: "ok", ReceivedAt: &now, FreshUntil: &until, Data: map[string]any{"remaining": balance, "unit": "USD"},
		}},
	}}
}

func TestAccountOpsBatchScanKeepsRuleOrderAcrossMetrics(t *testing.T) {
	now := time.Now()
	accounts := &accountOpsBatchAccounts{items: []Account{
		accountOpsBatchBalanceAccount(42, 2),
		{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
			"codex_usage_updated_at": now, "codex_5h_used_percent": 95, "codex_5h_reset_at": now.Add(time.Hour),
		}},
		accountOpsBatchBalanceAccount(41, 8),
	}}
	repo := &opsOnceRepoStub{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
	svc.SetNotificationDependencies(accounts, nil, false, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	cfg.Groups = []AccountOpsGroup{{ID: "manual-example", Name: "shared", AccountIDs: []int64{41, 42}}}
	cfg.BalanceThresholds = []AccountOpsBalanceRule{
		{AccountID: 41, Enabled: true, Threshold: 5, Unit: "USD"},
		{AccountID: 42, Enabled: true, Threshold: 5, Unit: "USD"},
	}
	cfg.QuotaThresholds = []AccountOpsQuotaRule{{AccountID: 43, Enabled: true, ThresholdPercent: 80, Window: "5h"}}
	svc.config.Store(cfg)

	svc.scanBalances(context.Background())

	require.Equal(t, 1, accounts.calls, "balance and quota rules share one batch read")
	require.Equal(t, []int64{41, 42, 43}, accounts.ids)
	require.Len(t, repo.events, 2)
	for i, event := range repo.events {
		switch event.Kind {
		case "balance_threshold":
			require.Equal(t, int64(41), event.AccountID, "database result order must not select the shared group representative")
			require.Equal(t, "resolved", repo.observations[i])
			require.NotNil(t, event.Details)
			require.NotNil(t, event.Details.Balance)
			require.Equal(t, 8.0, *event.Details.Balance)
		case "quota_threshold":
			require.Equal(t, int64(43), event.AccountID)
			require.Equal(t, "active", repo.observations[i])
		default:
			t.Fatalf("unexpected metric: %s", event.Kind)
		}
	}
}

func TestAccountOpsBatchLookupFailureStillPersistsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name          string
		items         []Account
		err           error
		waitForCancel bool
	}{
		{name: "partial data with error", items: []Account{accountOpsBatchBalanceAccount(41, 8)}, err: errors.New("synthetic batch failure")},
		{name: "missing account"},
		{name: "lookup timeout", items: []Account{accountOpsBatchBalanceAccount(41, 8)}, waitForCancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &accountOpsBatchAccounts{items: tc.items, err: tc.err, waitForCancel: tc.waitForCancel}
			repo := &opsLiveObservationRepo{}
			svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
			svc.thresholdLookupTimeout = time.Millisecond
			svc.SetNotificationDependencies(accounts, nil, false, "UTC")
			cfg := defaultAccountOpsConfig()
			cfg.Enabled = true
			cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 41, Enabled: true, Threshold: 5, Unit: "USD"}}
			svc.config.Store(cfg)

			svc.scanBalances(context.Background())

			require.Equal(t, 1, accounts.calls)
			require.NoError(t, repo.writeErr, "the observation must use a live context after the lookup expires")
			require.Equal(t, []string{"unknown"}, repo.observations)
		})
	}
}

func TestAccountOpsBatchScanWithoutRulesDoesNotReadAccounts(t *testing.T) {
	accounts := &accountOpsBatchAccounts{}
	repo := &opsOnceRepoStub{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
	svc.SetNotificationDependencies(accounts, nil, false, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	svc.config.Store(cfg)

	svc.scanBalances(context.Background())

	require.Zero(t, accounts.calls, "an empty rule set must not become an all-account batch")
	require.Empty(t, repo.events)
}

func TestAccountOpsMissingMembersStayInTheSameGroupDuringScanAndRecheck(t *testing.T) {
	accounts := &accountOpsBatchAccounts{}
	repo := &opsOnceRepoStub{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
	svc.SetNotificationDependencies(accounts, nil, false, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	cfg.Groups = []AccountOpsGroup{{ID: "manual-missing", Name: " shared ", AccountIDs: []int64{41, 42}}}
	cfg.BalanceThresholds = []AccountOpsBalanceRule{
		{AccountID: 42, Enabled: true, Threshold: 5, Unit: "USD"},
		{AccountID: 41, Enabled: true, Threshold: 5, Unit: "USD"},
	}
	svc.config.Store(cfg)
	svc.scanBalances(context.Background())
	require.Equal(t, []string{"unknown"}, repo.observations)
	require.Len(t, repo.events, 1, "missing members must not become separate account episodes")
	require.Equal(t, "manual-missing", repo.events[0].GroupID)
	require.Equal(t, "shared", repo.events[0].GroupName)
	require.Equal(t, int64(42), repo.events[0].AccountID)

	group, members, err := svc.loadThresholdGroupMembers(context.Background(), "manual-missing", "balance_threshold", cfg)
	require.NoError(t, err)
	require.Equal(t, repo.events[0].GroupID, group.ID)
	require.Equal(t, repo.events[0].GroupName, group.Name)
	require.Len(t, members, 2)
	require.Equal(t, int64(42), members[0].id)
	for _, member := range members {
		require.Nil(t, member.account)
		require.ErrorIs(t, member.lookupErr, ErrAccountNotFound)
	}
}
