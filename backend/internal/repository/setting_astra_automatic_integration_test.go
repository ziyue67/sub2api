//go:build integration

package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAstraAutomaticAtomicSettings(t *testing.T) {
	ctx := t.Context()
	client := testEntClient(t)
	a := mustCreateAccount(t, client, &service.Account{Name: "astra-auto", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Concurrency: 4, Schedulable: true, Credentials: map[string]any{"access_token": "keep-test-token", "refresh_token": "keep-refresh", "model_mapping": map[string]any{"gpt-5.6-sol": "gpt-5.6-sol"}}, Extra: map[string]any{"privacy_mode": "training_off", "unrelated": true}})
	t.Cleanup(func() { _ = client.Account.DeleteOneID(a.ID).Exec(ctx) })
	source := mustCreateAccount(t, client, &service.Account{Name: "astra-donor", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Concurrency: 2, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{}}, Extra: map[string]any{"unrelated": "kept"}})
	t.Cleanup(func() { _ = client.Account.DeleteOneID(source.ID).Exec(ctx) })
	key := fmt.Sprintf("astra-auto-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = client.Setting.Delete().Where(setting.KeyEQ(key)).Exec(ctx) })
	repo := &settingRepository{client: client}
	value := config.AstraRoutingSettings{CookiePool: config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{source.ID}, TargetAccountIDs: []int64{a.ID}}, WSSession: config.CodexWSAnchorConfig{Enabled: true, AccountIDs: []int64{a.ID}}, Revision: "one"}
	require.NoError(t, repo.SetAstraRoutingWithAccounts(ctx, key, value))
	got, err := client.Account.Get(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "keep-test-token", got.Credentials["access_token"])
	require.Equal(t, "keep-refresh", got.Credentials["refresh_token"])
	mapping := got.Credentials["model_mapping"].(map[string]any)
	require.Equal(t, "gpt-6-astra", mapping["gpt-6-astra"])
	require.Equal(t, "gpt-5.6-sol", mapping["gpt-5.6-sol"])
	require.Equal(t, true, got.Extra["openai_oauth_responses_websockets_v2_enabled"])
	require.Equal(t, "ctx_pool", got.Extra["openai_oauth_responses_websockets_v2_mode"])
	require.Equal(t, true, got.Extra["unrelated"])
	donor, err := client.Account.Get(ctx, source.ID)
	require.NoError(t, err)
	require.Empty(t, donor.Credentials["model_mapping"])
	require.NotContains(t, donor.Extra, "openai_oauth_responses_websockets_v2_enabled")
	before, err := repo.GetValue(ctx, key)
	require.NoError(t, err)
	// A later missing row rolls back earlier account updates and the setting.
	require.NoError(t, client.Account.UpdateOneID(a.ID).SetExtra(map[string]any{"marker": "must-survive"}).Exec(ctx))
	beforeAccount, err := client.Account.Get(ctx, a.ID)
	require.NoError(t, err)
	value.WSSession.AccountIDs = append(value.WSSession.AccountIDs, 9223372036854775806)
	value.Revision = "two"
	require.Error(t, repo.SetAstraRoutingWithAccounts(ctx, key, value))
	after, err := repo.GetValue(ctx, key)
	require.NoError(t, err)
	require.Equal(t, before, after)
	got, err = client.Account.Get(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, beforeAccount.Extra, got.Extra)
}
