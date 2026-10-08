//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountOpsDurableCoalescingAndClaims(t *testing.T) {
	ctx := context.Background()
	var account int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('ops-fixture','openai','apikey','active',true) RETURNING id`).Scan(&account))
	defer func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account) }()
	repo := NewAccountOpsRepository(integrationDB)
	event := service.AccountOpsEvent{AccountID: account, AccountName: "ops-fixture", Kind: "balance_low", Signal: "balance_error_code", HTTPStatus: 402}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.Record(ctx, event) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	items, err := repo.List(ctx, 0, 100)
	require.NoError(t, err)
	var found *service.AccountOpsEvent
	for i := range items {
		if items[i].AccountID == account {
			found = &items[i]
		}
	}
	require.NotNil(t, found)
	require.EqualValues(t, 12, found.Occurrences)
	claims := make(chan *service.AccountOpsEvent, 2)
	claimErrors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e, err := repo.Claim(ctx); claims <- e; claimErrors <- err }()
	}
	wg.Wait()
	close(claims)
	close(claimErrors)
	for err := range claimErrors {
		require.NoError(t, err)
	}
	var claimed *service.AccountOpsEvent
	count := 0
	for e := range claims {
		if e != nil {
			count++
			claimed = e
		}
	}
	require.Equal(t, 1, count)
	require.NoError(t, repo.Complete(ctx, claimed, "sent", time.Hour))
	require.NoError(t, repo.Record(ctx, event))
	none, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, none, "cooldown survives a new repository instance")
	_, err = integrationDB.ExecContext(ctx, `UPDATE account_ops_alerts SET next_send_at=NOW()-INTERVAL '1 second' WHERE account_id=$1`, account)
	require.NoError(t, err)
	require.NoError(t, repo.Record(ctx, event))
	again, err := NewAccountOpsRepository(integrationDB).Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, again)
	require.NoError(t, repo.Complete(ctx, claimed, "failed", time.Minute)) // stale lease must not overwrite the new delivery
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_alerts WHERE account_id=$1`, account).Scan(&state))
	require.Equal(t, "sending", state)
	require.NoError(t, repo.Complete(ctx, again, "failed", time.Minute))
	require.NoError(t, repo.SuppressDisabled(ctx, service.AccountOpsConfig{}))
	none, err = repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, none)
}

func TestAccountOpsRobotReservationsAreDurableAndRejectRecoveredLease(t *testing.T) {
	ctx := context.Background()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('robot-slot','openai','apikey','active',true) RETURNING id`).Scan(&id))
	defer func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id) }()
	repo := NewAccountOpsRepository(integrationDB)
	require.NoError(t, repo.Record(ctx, service.AccountOpsEvent{AccountID: id, Kind: "balance_low", AccountName: "robot-slot", Signal: "balance_error_code"}))
	event, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, event)
	reserver := repo.(interface {
		ReserveRobotDelivery(context.Context, *service.AccountOpsEvent, string, string, string) (bool, error)
	})
	ok, err := reserver.ReserveRobotDelivery(ctx, event, "robot:one:revision", "feishu", "synthetic-hash")
	require.NoError(t, err)
	require.True(t, ok)
	start := time.Now()
	ok, err = reserver.ReserveRobotDelivery(ctx, event, "robot:two:revision", "feishu", "synthetic-hash")
	require.NoError(t, err)
	require.True(t, ok)
	require.GreaterOrEqual(t, time.Since(start), 3*time.Second)
	require.NoError(t, repo.SuppressDisabled(ctx, service.AccountOpsConfig{}))
	ok, err = reserver.ReserveRobotDelivery(ctx, event, "robot:three:revision", "feishu", "synthetic-hash")
	require.NoError(t, err)
	require.False(t, ok)
	items, err := repo.List(ctx, 0, 100)
	require.NoError(t, err)
	for _, item := range items {
		if item.AccountID == id {
			raw, err := json.Marshal(item)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "synthetic-hash")
			require.NotContains(t, string(raw), "_attempted_at")
		}
	}
}

func TestAccountOpsRobotTestSharesDurableRateGuardWithoutAlertRecord(t *testing.T) {
	repo := NewAccountOpsRepository(integrationDB)
	rate := repo.(interface {
		ReserveRobotTest(context.Context, string) error
	})
	ctx := context.Background()
	var before int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_ops_alerts`).Scan(&before))
	require.NoError(t, rate.ReserveRobotTest(ctx, "synthetic-test-hash"))
	start := time.Now()
	require.NoError(t, rate.ReserveRobotTest(ctx, "synthetic-test-hash"))
	require.GreaterOrEqual(t, time.Since(start), 3*time.Second)
	var after int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_ops_alerts`).Scan(&after))
	require.Equal(t, before, after)
}

func TestAccountOpsConfigUsesRealAESAndRedactsStoredSecrets(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(integrationEntClient)
	key := "account_ops_notifications_v1"
	previous, previousErr := settings.GetValue(ctx, key)
	defer func() {
		if previousErr == nil {
			_ = settings.Set(ctx, key, previous)
		} else {
			_ = settings.Delete(ctx, key)
		}
	}()
	enc, encErr := NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)}})
	require.NoError(t, encErr)
	svc := service.NewAccountOpsService(settings, NewAccountOpsRepository(integrationDB), nil)
	svc.SetNotificationDependencies(nil, enc, true, "Asia/Shanghai")
	cfg := service.AccountOpsConfig{Enabled: true, BalanceLow: true, CooldownMinutes: 60, Webhooks: []service.AccountOpsWebhook{{ID: "robot", Provider: "dingtalk", Enabled: true, URL: "https://oapi.dingtalk.com/robot/send?access_token=synthetic-only-token", Secret: "synthetic-only-secret"}}}
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	raw, err := settings.GetValue(ctx, key)
	require.NoError(t, err)
	require.NotContains(t, raw, "synthetic-only-token")
	require.NotContains(t, raw, "synthetic-only-secret")
	var stored struct {
		Webhooks []struct {
			URLCipher    string `json:"url_cipher"`
			SecretCipher string `json:"secret_cipher"`
			Revision     string `json:"revision"`
		} `json:"encrypted_webhooks"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Len(t, stored.Webhooks, 1)
	url, err := enc.Decrypt(stored.Webhooks[0].URLCipher)
	require.NoError(t, err)
	require.Equal(t, cfg.Webhooks[0].URL, url)
	secret, err := enc.Decrypt(stored.Webhooks[0].SecretCipher)
	require.NoError(t, err)
	require.Equal(t, cfg.Webhooks[0].Secret, secret)
	revision := stored.Webhooks[0].Revision
	public, err := svc.GetConfig(ctx)
	require.NoError(t, err)
	out, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(out), "cipher")
	require.NotContains(t, string(out), "synthetic-only")
	require.NoError(t, svc.SaveConfig(ctx, public))
	raw, err = settings.GetValue(ctx, key)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, revision, stored.Webhooks[0].Revision)
	public.Webhooks[0].Secret = "new-synthetic-secret"
	require.NoError(t, svc.SaveConfig(ctx, public))
	raw, err = settings.GetValue(ctx, key)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.NotEqual(t, revision, stored.Webhooks[0].Revision)
}

// Threshold periodic rearming coverage replaced by account_ops_once_integration_test.go.
