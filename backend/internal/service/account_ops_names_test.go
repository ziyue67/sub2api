package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountOpsChannelNamesPersistAndRenameWithoutChangingIdentity(t *testing.T) {
	ctx := context.Background()
	settings := &accountOpsSettingsStub{}
	svc := NewAccountOpsService(settings, &accountOpsRepoStub{}, nil)
	svc.SetNotificationDependencies(nil, opsTestEncryptor{}, true, "UTC")
	cfg := defaultAccountOpsConfig()
	require.NoError(t, json.Unmarshal([]byte(`{"recipient":"ops@example.test","email_name":"  值班邮箱  ","webhooks":[{"id":"robot","provider":"dingtalk","enabled":true,"name":"  告警群  ","url":"https://oapi.dingtalk.com/robot/send?access_token=synthetic-token","secret":"synthetic-secret"}]}`), &cfg))
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	out, err := svc.GetConfig(ctx)
	require.NoError(t, err)
	public, err := json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(public), `"email_name":"值班邮箱"`)
	require.Contains(t, string(public), `"name":"告警群"`)
	require.NotContains(t, string(public), "synthetic-")
	require.NotContains(t, string(public), "cipher")
	var before, after storedOpsConfig
	require.NoError(t, json.Unmarshal([]byte(settings.raw), &before))
	var update AccountOpsWebhook
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"name":" 新告警群 "}`), &update))
	// Metadata can be edited without access to the fixed credential key.
	svc.SetNotificationDependencies(nil, nil, false, "UTC")
	_, err = svc.SaveWebhook(ctx, "robot", update)
	require.NoError(t, err)
	var email AccountOpsNotificationSettings
	require.NoError(t, json.Unmarshal([]byte(`{"email_name":" 新值班邮箱 "}`), &email))
	out, err = svc.SaveNotificationSettings(ctx, email)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(settings.raw), &after))
	require.Equal(t, before.StoredWebhooks[0].URLCipher, after.StoredWebhooks[0].URLCipher)
	require.Equal(t, before.StoredWebhooks[0].SecretCipher, after.StoredWebhooks[0].SecretCipher)
	require.Equal(t, before.StoredWebhooks[0].Revision, after.StoredWebhooks[0].Revision)
	require.Equal(t, "ops@example.test", out.Recipient)
	public, err = json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(public), `"email_name":"新值班邮箱"`)
	require.Contains(t, string(public), `"name":"新告警群"`)
	beforeOmitted := settings.raw
	_, err = svc.SaveWebhook(ctx, "robot", AccountOpsWebhook{Enabled: true})
	require.NoError(t, err)
	_, err = svc.SaveNotificationSettings(ctx, AccountOpsNotificationSettings{})
	require.NoError(t, err)
	require.JSONEq(t, beforeOmitted, settings.raw)
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"name":""}`), &update))
	_, err = svc.SaveWebhook(ctx, "robot", update)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(`{"recipient":"","email_name":""}`), &email))
	out, err = svc.SaveNotificationSettings(ctx, email)
	require.NoError(t, err)
	public, err = json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(public), `"name":""`)
	require.NotContains(t, string(public), "新值班邮箱")
	require.Empty(t, out.Recipient)
}

func TestAccountOpsChannelNamesRejectControlsAndOverlongInput(t *testing.T) {
	for _, name := range []string{strings.Repeat("名", 81), "private-canary\nname", "\nprivate-canary", "private-canary\t", "private-canary\u0000", "private-canary\u0085"} {
		for _, field := range []string{"name", "email_name"} {
			t.Run(field+"/"+name, func(t *testing.T) {
				settings := &accountOpsSettingsStub{raw: `{"cooldown_minutes":60,"encrypted_webhooks":[{"id":"robot","provider":"dingtalk","enabled":true,"url_cipher":"opaque-url","secret_cipher":"opaque-secret","revision":"revision"}]}`}
				svc := NewAccountOpsService(settings, &accountOpsRepoStub{}, nil)
				before := settings.raw
				raw, err := json.Marshal(map[string]any{field: name, "enabled": true})
				require.NoError(t, err)
				if field == "name" {
					var input AccountOpsWebhook
					require.NoError(t, json.Unmarshal(raw, &input))
					_, err = svc.SaveWebhook(context.Background(), "robot", input)
				} else {
					var input AccountOpsNotificationSettings
					require.NoError(t, json.Unmarshal(raw, &input))
					_, err = svc.SaveNotificationSettings(context.Background(), input)
				}
				require.ErrorIs(t, err, ErrAccountOpsConfigValidation)
				require.NotContains(t, err.Error(), "private-canary")
				require.Equal(t, before, settings.raw)
			})
		}
	}
}

func TestAccountOpsChannelNameLimitCountsUnicodeCharacters(t *testing.T) {
	settings := &accountOpsSettingsStub{}
	svc := NewAccountOpsService(settings, &accountOpsRepoStub{}, nil)
	name := strings.Repeat("群", 80)
	raw, err := json.Marshal(map[string]string{"email_name": "  " + name + "  "})
	require.NoError(t, err)
	var input AccountOpsNotificationSettings
	require.NoError(t, json.Unmarshal(raw, &input))
	out, err := svc.SaveNotificationSettings(context.Background(), input)
	require.NoError(t, err)
	raw, err = json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"email_name":"`+name+`"`)
}

func TestAccountOpsDeliveryNamesSnapshotLatestConfigAndPreserveSentHistory(t *testing.T) {
	ctx := context.Background()
	settings := &accountOpsSettingsStub{}
	repo := &opsReservationStub{}
	sender := &accountOpsSenderStub{}
	svc := NewAccountOpsService(settings, repo, sender)
	svc.SetNotificationDependencies(nil, opsTestEncryptor{}, true, "UTC")
	cfg := defaultAccountOpsConfig()
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"recipient":"ops@example.test","email_name":"原邮箱","webhooks":[{"id":"robot","enabled":true,"provider":"custom","name":"原机器人","url":"https://hooks.example.test/send"}]}`), &cfg))
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	repo.duringWait = func() {
		var input AccountOpsWebhook
		require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"name":"发送前的新名称"}`), &input))
		_, err := svc.SaveWebhook(ctx, "robot", input)
		require.NoError(t, err)
	}
	requests := 0
	svc.robotClient = &http.Client{Transport: opsRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	event := &AccountOpsEvent{AccountID: 7, Kind: "balance_low", Attempts: 1}
	svc.deliverEvent(ctx, event)
	require.Equal(t, 1, sender.calls)
	require.Equal(t, 1, requests)
	before, err := json.Marshal(event.Deliveries)
	require.NoError(t, err)
	require.Contains(t, string(before), `"name":"原邮箱"`)
	require.Contains(t, string(before), `"name":"发送前的新名称"`)
	var webhook AccountOpsWebhook
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"name":"发送后的名称"}`), &webhook))
	_, err = svc.SaveWebhook(ctx, "robot", webhook)
	require.NoError(t, err)
	var email AccountOpsNotificationSettings
	require.NoError(t, json.Unmarshal([]byte(`{"email_name":"新邮箱"}`), &email))
	_, err = svc.SaveNotificationSettings(ctx, email)
	require.NoError(t, err)
	svc.deliverEvent(ctx, event)
	require.Equal(t, 1, sender.calls, "a display rename must not resend successful mail")
	require.Equal(t, 1, requests, "a display rename must not create a new webhook delivery identity")
	after, err := json.Marshal(event.Deliveries)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}
