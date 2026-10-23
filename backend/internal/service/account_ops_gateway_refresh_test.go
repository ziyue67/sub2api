package service

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type accountOpsRoleSettings struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
}

func (r *accountOpsRoleSettings) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (r *accountOpsRoleSettings) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}
func (r *accountOpsRoleSettings) setConfig(t *testing.T, c AccountOpsConfig) {
	t.Helper()
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	require.NoError(t, r.Set(context.Background(), accountOpsSettingsKey, string(raw)))
}

type accountOpsRoleAccounts struct {
	AccountRepository
	scans atomic.Int64
	items []Account
}

func (r *accountOpsRoleAccounts) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	r.scans.Add(1)
	return r.items, nil
}

type accountOpsRoleEvents struct {
	AccountOpsRepository
	refreshed chan AccountOpsConfig
	recorded  chan AccountOpsEvent
}

func (r *accountOpsRoleEvents) SuppressDisabled(_ context.Context, c AccountOpsConfig) error {
	select {
	case r.refreshed <- c:
	default:
	}
	return nil
}
func (r *accountOpsRoleEvents) Record(_ context.Context, event AccountOpsEvent) error {
	r.recorded <- event
	return nil
}

func TestGatewayAccountOpsRefreshSkipsGlobalGroupsButKeepsConfigAndEvents(t *testing.T) {
	settings := &accountOpsRoleSettings{values: make(map[string]string)}
	c := defaultAccountOpsConfig()
	c.Enabled = true
	c.WeeklyQuota = true
	c.Recipient = "first@example.com"
	settings.setConfig(t, c)
	accounts := &accountOpsRoleAccounts{}
	events := &accountOpsRoleEvents{refreshed: make(chan AccountOpsConfig, 8), recorded: make(chan AccountOpsEvent, 8)}
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway}}
	svc := ProvideAccountOpsService(settings, events, nil, accounts, nil, cfg, nil, nil, nil, nil, nil)
	defer svc.Stop()
	select {
	case <-events.refreshed:
	case <-time.After(time.Second):
		t.Fatal("gateway did not load its initial event configuration")
	}
	require.Zero(t, accounts.scans.Load(), "gateway startup must not hydrate all accounts")
	assertRecorded := func(kind string) {
		t.Helper()
		svc.queue <- AccountOpsEvent{AccountID: 7, Kind: kind}
		select {
		case event := <-events.recorded:
			require.Equal(t, kind, event.Kind)
		case <-time.After(time.Second):
			t.Fatal("gateway did not persist its local event")
		}
	}
	assertRecorded("weekly_quota")

	// Exercise the same refresh path used by the periodic loop after a remote
	// full node changes configuration. Local delivery policy must converge
	// without repeating the full node's group reconciliation/account scan.
	c.Recipient = "updated@example.com"
	c.WeeklyQuota = false
	c.BalanceLow = true
	settings.setConfig(t, c)
	for range 3 {
		require.True(t, svc.refreshConfig(context.Background(), false))
	}
	require.Equal(t, c.Recipient, svc.currentConfig().Recipient)
	require.False(t, svc.currentConfig().Allows("weekly_quota"))
	require.True(t, svc.currentConfig().Allows("balance_low"))
	assertRecorded("balance_low")
	require.Zero(t, accounts.scans.Load(), "gateway refreshes must not hydrate all accounts")
}

func TestFullAccountOpsStartupKeepsSharedGroupReconciliation(t *testing.T) {
	for _, role := range []string{"", config.RuntimeRoleFull} {
		t.Run(role, func(t *testing.T) {
			settings := &accountOpsRoleSettings{values: make(map[string]string)}
			accounts := &accountOpsRoleAccounts{items: []Account{{
				ID: 42, Name: "test account", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://example.com/v1"},
			}}}
			events := &accountOpsRoleEvents{refreshed: make(chan AccountOpsConfig, 8), recorded: make(chan AccountOpsEvent, 8)}
			cfg := &config.Config{Runtime: config.RuntimeConfig{Role: role}}
			svc := ProvideAccountOpsService(settings, events, nil, accounts, nil, cfg, nil, nil, nil, nil, nil)
			defer svc.Stop()
			require.Eventually(t, func() bool { return svc.currentConfig().GroupsInitialized }, time.Second, time.Millisecond)
			require.EqualValues(t, 1, accounts.scans.Load())
			stored, err := svc.GetConfig(context.Background())
			require.NoError(t, err)
			require.True(t, stored.GroupsInitialized)
			require.Len(t, stored.Groups, 1)
			require.Equal(t, []int64{42}, stored.Groups[0].AccountIDs)
			svc.refreshConfig(context.Background(), true)
			require.EqualValues(t, 2, accounts.scans.Load(), "full periodic refresh must retain reconciliation")
		})
	}
}
