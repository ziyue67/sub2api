//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAutoConfigHistoryPersistenceAndCursor(t *testing.T) {
	ctx := context.Background()
	marker := fmt.Sprintf("history-test-%d", time.Now().UnixNano())
	var prior string
	priorErr := integrationDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=$1", service.SettingKeyOAuthAutoConfig).Scan(&prior)
	if priorErr != nil {
		require.ErrorIs(t, priorErr, sql.ErrNoRows)
	}
	t.Cleanup(func() {
		if priorErr == nil {
			_, err := integrationDB.ExecContext(ctx, "UPDATE settings SET value=$2 WHERE key=$1", service.SettingKeyOAuthAutoConfig, prior)
			require.NoError(t, err)
		} else {
			_, err := integrationDB.ExecContext(ctx, "DELETE FROM settings WHERE key=$1", service.SettingKeyOAuthAutoConfig)
			require.NoError(t, err)
		}
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM account_auto_config_events WHERE details->'config'->>'revision'=$1", marker)
		require.NoError(t, err)
	})
	settings := NewSettingRepository(integrationEntClient)
	config := service.DefaultOAuthAutoConfig()
	config.Revision = marker
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, service.SettingKeyOAuthAutoConfig, string(raw)))
	require.NoError(t, settings.Set(ctx, service.SettingKeyOAuthAutoConfig, string(raw)))
	// A fresh repository instance reads persistent history, including the saved snapshot.
	logs := &accountOpsRepository{db: integrationDB}
	first, err := logs.ListAutoConfigEvents(ctx, 0, service.AutoConfigEventSaved, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, marker, first[0].Details.Config.Revision)
	require.Equal(t, config.Concurrency, first[0].Details.Config.Concurrency)
	require.NoError(t, settings.Set(ctx, service.SettingKeyOAuthAutoConfig, string(raw)))
	older, err := logs.ListAutoConfigEvents(ctx, first[0].ID, service.AutoConfigEventSaved, 1)
	require.NoError(t, err)
	require.Len(t, older, 1)
	require.Less(t, older[0].ID, first[0].ID, "new inserts must not duplicate the previous page")
	saved, err := settings.GetValue(ctx, service.SettingKeyOAuthAutoConfig)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), saved)

	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "history-example", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 5, Priority: 0, Credentials: map[string]any{"access_token": "private-test-token"}})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM account_auto_config_events WHERE account_id=$1", account.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", account.ID)
		require.NoError(t, err)
	})
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	require.NoError(t, repo.RecordAutoConfigInitial(ctx, account, []int64{3, 5}))
	require.NoError(t, integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx))
	initial, err := logs.ListAutoConfigEvents(ctx, 0, service.AutoConfigEventInitial, 1)
	require.NoError(t, err)
	require.Len(t, initial, 1)
	require.Equal(t, account.ID, initial[0].AccountID)
	require.Equal(t, "history-example", initial[0].AccountName, "account deletion does not erase the history snapshot")
	require.Equal(t, []int64{3, 5}, initial[0].Details.GroupIDs)
	encoded, err := json.Marshal(initial)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-test-token")
}
