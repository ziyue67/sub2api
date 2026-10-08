package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type opsOnceRepoStub struct {
	accountOpsRepoStub
	observations       []string
	events             []AccountOpsEvent
	alerts, recoveries []bool
	eligible           bool
}

func (r *opsOnceRepoStub) ObserveThreshold(_ context.Context, e AccountOpsEvent, state string, _ time.Time, a, b bool) error {
	r.events = append(r.events, e)
	r.observations = append(r.observations, state)
	r.alerts = append(r.alerts, a)
	r.recoveries = append(r.recoveries, b)
	return nil
}
func (r *opsOnceRepoStub) ThresholdEventEligible(context.Context, *AccountOpsEvent) (bool, error) {
	return r.eligible, nil
}
func TestAccountOpsOnceObservesHealthyAndUnknownWithoutSending(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	balance := &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &now, FreshUntil: &until, Data: map[string]any{"remaining": 6.0, "unit": "USD"}}
	a := &Account{ID: 41, Name: "fixture", Type: AccountTypeAPIKey, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", Balance: balance}}}
	repo := &opsOnceRepoStub{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
	cfg := defaultAccountOpsConfig()
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 41, Enabled: true, Threshold: 5, Unit: "USD", NotifyAlert: opsBool(false), NotifyRecovery: opsBool(true)}}
	for _, value := range []float64{6, 5, 4, 6} {
		balance.Data["remaining"] = value
		require.NoError(t, svc.observeThreshold(context.Background(), a, 41, "balance_threshold", cfg, now, false))
	}
	balance.Status = "failed"
	require.NoError(t, svc.observeThreshold(context.Background(), a, 41, "balance_threshold", cfg, now, false))
	require.Equal(t, []string{"resolved", "active", "active", "resolved", "unknown"}, repo.observations)
	require.Equal(t, 6.0, *repo.events[3].Details.Balance)
	require.False(t, repo.alerts[0])
	require.True(t, repo.recoveries[0])
	require.NotEmpty(t, repo.events[0].Criteria)
}
func TestAccountOpsScopedSaveRetainsPrivateChannelsOtherRulesAndOmittedFlags(t *testing.T) {
	ctx := context.Background()
	settings := &accountOpsSettingsStub{}
	accounts := &opsAccountsStub{account: &Account{ID: 41, Type: AccountTypeAPIKey}}
	svc := NewAccountOpsService(settings, &accountOpsRepoStub{}, nil)
	svc.SetNotificationDependencies(accounts, opsTestEncryptor{}, true, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 41, Enabled: true, Threshold: 5, Unit: "USD", NotifyAlert: opsBool(false), NotifyRecovery: opsBool(true)}}
	cfg.Webhooks = []AccountOpsWebhook{{ID: "bot", Provider: "dingtalk", Enabled: true, URL: "https://oapi.dingtalk.com/robot/send?access_token=fake-only", Secret: "fake-secret"}}
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	var before storedOpsConfig
	require.NoError(t, json.Unmarshal([]byte(settings.raw), &before))
	threshold := 9.0
	out, err := svc.SaveRule(ctx, 41, AccountOpsRuleUpdate{Metric: "balance", Enabled: true, Threshold: &threshold})
	require.NoError(t, err)
	require.False(t, *out.BalanceThresholds[0].NotifyAlert)
	var after storedOpsConfig
	require.NoError(t, json.Unmarshal([]byte(settings.raw), &after))
	require.Equal(t, before.StoredWebhooks, after.StoredWebhooks)
	// An account becoming orphaned must not prevent an unrelated settings save.
	accounts.err = ErrAccountNotFound
	recipient, cooldown := "new@example.test", 90
	out, err = svc.SaveNotificationSettings(ctx, AccountOpsNotificationSettings{Recipient: &recipient, BalanceLow: opsBool(true), WeeklyQuota: opsBool(true), CooldownMinutes: &cooldown})
	require.NoError(t, err)
	require.Equal(t, 9.0, out.BalanceThresholds[0].Threshold)
	require.Len(t, out.Webhooks, 1)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fake-secret")
	require.NotContains(t, string(raw), "fake-only")
	require.NotContains(t, string(raw), "cipher")
	accounts.err = nil
	var legacy AccountOpsConfig
	require.NoError(t, json.Unmarshal([]byte(`{"cooldown_minutes":90,"balance_thresholds":[{"account_id":41,"enabled":true,"threshold":10,"unit":"USD"}]}`), &legacy))
	require.NoError(t, svc.SaveConfig(ctx, legacy))
	out, err = svc.GetConfig(ctx)
	require.NoError(t, err)
	require.False(t, *out.BalanceThresholds[0].NotifyAlert)
	require.True(t, *out.BalanceThresholds[0].NotifyRecovery)
}
func TestAccountOpsNotificationPartialSavePreservesUnrelatedFields(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		change     func(*AccountOpsConfig)
	}{
		{"master", `{"enabled":false}`, func(c *AccountOpsConfig) { c.Enabled = false }},
		{"email", `{"recipient":"new@example.test"}`, func(c *AccountOpsConfig) { c.Recipient = "new@example.test" }},
		{"balance", `{"balance_low":false}`, func(c *AccountOpsConfig) { c.BalanceLow = false }},
		{"quota", `{"weekly_quota":false}`, func(c *AccountOpsConfig) { c.WeeklyQuota = false }},
		{"interval", `{"cooldown_minutes":120}`, func(c *AccountOpsConfig) { c.CooldownMinutes = 120 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc := NewAccountOpsService(&accountOpsSettingsStub{}, &accountOpsRepoStub{}, nil)
			cfg := defaultAccountOpsConfig()
			cfg.Enabled, cfg.Recipient, cfg.CooldownMinutes = true, "old@example.test", 90
			require.NoError(t, svc.SaveConfig(ctx, cfg))
			cfg = cfg.public()
			var input AccountOpsNotificationSettings
			require.NoError(t, json.Unmarshal([]byte(tc.body), &input))
			out, err := svc.SaveNotificationSettings(ctx, input)
			require.NoError(t, err)
			tc.change(&cfg)
			require.Equal(t, cfg, out)
			var invalid AccountOpsNotificationSettings
			require.NoError(t, json.Unmarshal([]byte(`{"cooldown_minutes":0}`), &invalid))
			_, err = svc.SaveNotificationSettings(ctx, invalid)
			require.ErrorIs(t, err, ErrAccountOpsConfigValidation)
			persisted, err := svc.GetConfig(ctx)
			require.NoError(t, err)
			require.Equal(t, out, persisted)
		})
	}
}

func TestAccountOpsRecoveryUsesHistoricalSnapshotAfterNewLowEpisode(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	value, threshold := 8.0, 5.0
	a := &Account{ID: 41, Type: AccountTypeAPIKey, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", Balance: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &now, FreshUntil: &until, Data: map[string]any{"remaining": 2.0, "unit": "USD"}}}}}
	settings := &accountOpsSettingsStub{}
	repo := &opsOnceRepoStub{eligible: true}
	sender := &accountOpsSenderStub{}
	svc := NewAccountOpsService(settings, repo, sender)
	svc.SetNotificationDependencies(&opsAccountsStub{account: a}, nil, false, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	cfg.Recipient = "fake@example.test"
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 41, Enabled: true, Threshold: threshold, Unit: "USD"}}
	require.NoError(t, svc.SaveConfig(context.Background(), cfg))
	criteria, _, _, _ := opsCriteria(cfg, 41, "balance_threshold")
	e := &AccountOpsEvent{ID: "historical", EpisodeID: "old-cycle", Phase: "recovery", AccountID: 41, Kind: "balance_threshold", Criteria: criteria, Identity: opsAccountIdentity(a), Details: &AccountOpsDetails{Balance: &value, Threshold: &threshold, Unit: "USD"}, LastSeen: now}
	svc.deliverEvent(context.Background(), e)
	require.Equal(t, 1, sender.calls)
	require.Contains(t, sender.body, "恢复时余额")
	require.Contains(t, sender.body, "8 USD")
	require.NotContains(t, sender.body, "2 USD")
	require.Equal(t, "sent", repo.state)
	cfg.BalanceThresholds[0].Threshold = 6
	require.NoError(t, svc.SaveConfig(context.Background(), cfg))
	e.Deliveries = nil
	svc.deliverEvent(context.Background(), e)
	require.Equal(t, 1, sender.calls)
	require.Equal(t, "suppressed", repo.state)
}

func TestAccountOpsOnceWarningDefersUnknownAndHealthyThenSendsFreshAdverse(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	until := now.Add(time.Hour)
	value, threshold := 4.0, 5.0
	balance := &UpstreamBalanceSnapshot{Status: "failed", ReceivedAt: &now, FreshUntil: &until, Data: map[string]any{"remaining": 2.0, "unit": "USD"}}
	a := &Account{ID: 41, Type: AccountTypeAPIKey, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", Balance: balance}}}
	settings := &accountOpsSettingsStub{}
	repo := &opsOnceRepoStub{eligible: true}
	sender := &accountOpsSenderStub{}
	svc := NewAccountOpsService(settings, repo, sender)
	svc.SetNotificationDependencies(&opsAccountsStub{account: a}, nil, false, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	cfg.Recipient = "fake@example.test"
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 41, Enabled: true, Threshold: threshold, Unit: "USD"}}
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	criteria, _, _, _ := opsCriteria(cfg, 41, "balance_threshold")
	e := &AccountOpsEvent{ID: "historical", EpisodeID: "cycle", Phase: "alert", AccountID: 41, Kind: "balance_threshold", Criteria: criteria, Identity: opsAccountIdentity(a), Details: &AccountOpsDetails{Balance: &value, Threshold: &threshold, Unit: "USD"}, LastSeen: now}
	svc.deliverEvent(ctx, e)
	require.Zero(t, sender.calls)
	require.Equal(t, "deferred", repo.state)
	balance.Status = "ok"
	balance.Data["remaining"] = 8.0
	svc.deliverEvent(ctx, e)
	require.Zero(t, sender.calls)
	require.Equal(t, "deferred", repo.state)
	balance.Data["remaining"] = 2.0
	svc.deliverEvent(ctx, e)
	require.Equal(t, 1, sender.calls)
	require.Equal(t, "sent", repo.state)
	require.Contains(t, sender.body, "4 USD")
	require.NotContains(t, sender.body, "2 USD")
}

func TestAccountOpsOnceFailedLookupResetsConfirmationEvenWithReturnedAccount(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	a := &Account{ID: 41, Type: AccountTypeAPIKey, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", Balance: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &now, FreshUntil: &until, Data: map[string]any{"remaining": 8.0, "unit": "USD"}}}}}
	repo := &opsOnceRepoStub{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
	svc.SetNotificationDependencies(&opsAccountsStub{account: a, err: errors.New("synthetic lookup failure")}, nil, false, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 41, Enabled: true, Threshold: 5, Unit: "USD"}}
	svc.config.Store(cfg)
	svc.scanBalances(context.Background())
	require.Equal(t, []string{"unknown"}, repo.observations)
}

type opsTimeoutAccounts struct{ AccountRepository }

func (opsTimeoutAccounts) GetByID(ctx context.Context, _ int64) (*Account, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type opsLiveObservationRepo struct {
	opsOnceRepoStub
	writeErr error
}

func (r *opsLiveObservationRepo) ObserveThreshold(ctx context.Context, e AccountOpsEvent, state string, now time.Time, a, b bool) error {
	r.writeErr = ctx.Err()
	if r.writeErr != nil {
		return r.writeErr
	}
	return r.opsOnceRepoStub.ObserveThreshold(ctx, e, state, now, a, b)
}
func TestAccountOpsOnceLookupTimeoutStillPersistsUnknown(t *testing.T) {
	repo := &opsLiveObservationRepo{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, nil)
	svc.thresholdLookupTimeout = time.Millisecond
	svc.SetNotificationDependencies(opsTimeoutAccounts{}, nil, false, "UTC")
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 41, Enabled: true, Threshold: 5, Unit: "USD"}}
	svc.config.Store(cfg)
	svc.scanBalances(context.Background())
	require.NoError(t, repo.writeErr)
	require.Equal(t, []string{"unknown"}, repo.observations, "expired lookup context must not swallow the reset observation")
}
