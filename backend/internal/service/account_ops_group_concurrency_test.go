package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type accountOpsReconcileConfigRepo struct {
	accountOpsRepoStub
	raw        string
	pending    string
	beforeLock func()
	commitErr  error
	writes     int
}

func (r *accountOpsReconcileConfigRepo) ReadAccountOpsConfig(context.Context) (string, error) {
	return r.raw, nil
}

func (r *accountOpsReconcileConfigRepo) PersistAccountOpsConfig(_ context.Context, raw string, _ AccountOpsConfig) error {
	r.writes++
	r.pending = raw
	return nil
}

func (r *accountOpsReconcileConfigRepo) WithAccountOpsConfigLock(ctx context.Context, fn func(context.Context) error) error {
	if r.beforeLock != nil {
		r.beforeLock()
	}
	defer func() { r.pending = "" }()
	if err := fn(ctx); err != nil {
		return err
	}
	if r.commitErr != nil {
		return r.commitErr
	}
	if r.pending != "" {
		r.raw = r.pending
	}
	return nil
}

func accountOpsReconcileFixture(t *testing.T) (*AccountOpsService, *accountOpsReconcileConfigRepo, *accountOpsGroupPersistenceAccounts) {
	t.Helper()
	accounts := &accountOpsGroupPersistenceAccounts{accountOpsGroupAccounts: accountOpsGroupAccounts{items: map[int64]*Account{
		1: {ID: 1, Name: "existing", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://example.com/v1"}},
		2: {ID: 2, Name: "new", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://example.com/v1"}},
	}}}
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	cfg.Recipient = "before@example.com"
	cfg.GroupsInitialized = true
	cfg.Groups = []AccountOpsGroup{{ID: "manual-example1", Name: "shared", Provider: "sub2api", Site: "https://example.com", AccountIDs: []int64{1}}}
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 1, Enabled: true, Threshold: 5, Unit: "USD"}}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &accountOpsReconcileConfigRepo{raw: string(raw)}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
	svc.SetNotificationDependencies(accounts, nil, false, "UTC")
	svc.config.Store(cfg)
	return svc, repo, accounts
}

func TestAccountOpsGroupReconcilePreservesConcurrentConfigChanges(t *testing.T) {
	svc, repo, accounts := accountOpsReconcileFixture(t)
	repo.beforeLock = func() {
		// Another instance commits while this service is waiting for the config lock.
		var current AccountOpsConfig
		require.NoError(t, json.Unmarshal([]byte(repo.raw), &current))
		current.Recipient = "after@example.com"
		current.BalanceThresholds[0].Threshold = 17
		accounts.items[3] = &Account{ID: 3, Name: "concurrent", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://example.com/v1"}}
		current.Groups[0].AccountIDs = append(current.Groups[0].AccountIDs, 3)
		current.BalanceThresholds = append(current.BalanceThresholds, AccountOpsBalanceRule{AccountID: 3, Enabled: true, Threshold: 17, Unit: "USD"})
		raw, err := json.Marshal(current)
		require.NoError(t, err)
		repo.raw = string(raw)
	}

	got, err := svc.ensureManualAccountOpsGroups(context.Background())

	require.NoError(t, err)
	require.Equal(t, "after@example.com", got.Recipient)
	require.Equal(t, []int64{1, 3, 2}, got.Groups[0].AccountIDs)
	require.Len(t, got.BalanceThresholds, 3, "the stale account list must not delete a concurrently added account's rule")
	for _, rule := range got.BalanceThresholds {
		require.Equal(t, 17.0, rule.Threshold)
	}
	require.Equal(t, got, svc.currentConfig(), "publish the configuration produced under the lock")
	var persisted AccountOpsConfig
	require.NoError(t, json.Unmarshal([]byte(repo.raw), &persisted))
	require.Equal(t, "after@example.com", persisted.Recipient)
	require.Len(t, persisted.BalanceThresholds, 3)
	require.Equal(t, 2, accounts.lists, "concurrently changed configuration requires a fresh account snapshot")
}

func TestAccountOpsGroupReconcilePreservesRulesAddedBeforeConfigRead(t *testing.T) {
	svc, repo, accounts := accountOpsReconcileFixture(t)
	ctx := context.Background()
	staleAccounts, err := svc.listAccountOpsAccounts(ctx)
	require.NoError(t, err)

	// Another instance commits after the account read but before the first config read.
	accounts.items[3] = &Account{ID: 3, Name: "concurrent", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://example.com/v1"}}
	var current AccountOpsConfig
	require.NoError(t, json.Unmarshal([]byte(repo.raw), &current))
	current.Groups[0].AccountIDs = append(current.Groups[0].AccountIDs, 2, 3)
	for _, id := range []int64{2, 3} {
		current.BalanceThresholds = append(current.BalanceThresholds, copyBalanceRuleForAccount(current.BalanceThresholds[0], id))
	}
	raw, err := json.Marshal(current)
	require.NoError(t, err)
	repo.raw = string(raw)

	got, err := svc.ensureManualAccountOpsGroupsFromAccounts(ctx, staleAccounts)

	require.NoError(t, err)
	require.Len(t, got.BalanceThresholds, 3, "a stale account snapshot must not delete the new account's rule")
	require.Equal(t, []int64{1, 2, 3}, got.Groups[0].AccountIDs)
	require.Zero(t, repo.writes, "the fresh snapshot already matches the persisted configuration")
	require.Equal(t, string(raw), repo.raw)
	require.Equal(t, got, svc.currentConfig())
	require.Equal(t, 2, accounts.lists)
}

func TestAccountOpsGroupReconcileDoesNotPublishFailedCommit(t *testing.T) {
	svc, repo, accounts := accountOpsReconcileFixture(t)
	before := svc.currentConfig()
	raw := repo.raw
	repo.commitErr = errors.New("synthetic commit failure")

	_, err := svc.ensureManualAccountOpsGroups(context.Background())

	require.ErrorIs(t, err, repo.commitErr)
	require.Equal(t, raw, repo.raw)
	require.Equal(t, before, svc.currentConfig(), "uncommitted groups must not replace the active configuration")
	require.Equal(t, 2, accounts.lists, "persisting group changes requires a fresh account snapshot")
}

func TestAccountOpsGroupReconcileSkipsWriteIfAnotherInstanceAlreadyReconciled(t *testing.T) {
	svc, repo, accounts := accountOpsReconcileFixture(t)
	repo.beforeLock = func() {
		var current AccountOpsConfig
		require.NoError(t, json.Unmarshal([]byte(repo.raw), &current))
		current.Groups[0].AccountIDs = append(current.Groups[0].AccountIDs, 2)
		current.BalanceThresholds = append(current.BalanceThresholds, copyBalanceRuleForAccount(current.BalanceThresholds[0], 2))
		raw, err := json.Marshal(current)
		require.NoError(t, err)
		repo.raw = string(raw)
	}

	got, err := svc.ensureManualAccountOpsGroups(context.Background())

	require.NoError(t, err)
	require.Zero(t, repo.writes)
	require.Equal(t, []int64{1, 2}, got.Groups[0].AccountIDs)
	require.Equal(t, got, svc.currentConfig())
	require.Equal(t, 2, accounts.lists)
}
