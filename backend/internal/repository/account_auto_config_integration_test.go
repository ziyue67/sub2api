//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAutoConfigConcurrentResultsAndPreservedFields(t *testing.T) {
	ctx := context.Background()
	var accountID int64
	// Registered first so this check runs after all fixture cleanup, including
	// the committed outbox events needed by the concurrent transactions.
	t.Cleanup(func() {
		if accountID == 0 {
			return
		}
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", accountID).Scan(&count))
		require.Zero(t, count, "automatic configuration fixtures must not leak scheduler events into later tests")
	})
	group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: fmt.Sprintf("auto-config-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI})
	t.Cleanup(func() {
		require.NoError(t, integrationEntClient.Group.DeleteOneID(group.ID).Exec(ctx))
	})
	a := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "auto-config-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 3, Schedulable: true, Extra: map[string]any{"keep": "value"}})
	accountID = a.ID
	t.Cleanup(func() {
		require.NoError(t, integrationEntClient.Account.DeleteOneID(a.ID).Exec(ctx))
	})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		require.NoError(t, err)
	})
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	require.NoError(t, repo.BindGroups(ctx, a.ID, []int64{group.ID}))
	cfg := service.DefaultOAuthAutoConfig()
	cfg.UpgradeEnabled = true
	cfg.UpgradeGroupIDs = []int64{group.ID}
	cfg.SuccessesPerStep = 20
	cfg.Revision = "integration"
	cfg.MaxConcurrency = 100
	var prior string
	priorErr := integrationDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=$1", service.SettingKeyOAuthAutoConfig).Scan(&prior)
	if priorErr != nil {
		require.ErrorIs(t, priorErr, sql.ErrNoRows)
	}
	t.Cleanup(func() {
		var err error
		if priorErr == nil {
			_, err = integrationDB.ExecContext(ctx, "UPDATE settings SET value=$2 WHERE key=$1", service.SettingKeyOAuthAutoConfig, prior)
		} else {
			_, err = integrationDB.ExecContext(ctx, "DELETE FROM settings WHERE key=$1", service.SettingKeyOAuthAutoConfig)
		}
		require.NoError(t, err)
	})
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value", service.SettingKeyOAuthAutoConfig, string(raw))
	require.NoError(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	started := time.Now()
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.RecordConcurrencyResult(ctx, service.AccountConcurrencyResult{AccountID: a.ID, StartedAt: started, Success: true}, cfg)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 4, got.Concurrency)
	require.Equal(t, "value", got.Extra["keep"])
	// Manual settings cannot be decreased or replaced by a stale request snapshot.
	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET concurrency=9 WHERE id=$1", a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.RecordConcurrencyResult(ctx, service.AccountConcurrencyResult{AccountID: a.ID, StartedAt: time.Now(), Success: true}, cfg))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 9, got.Concurrency)
	// A 401/error status still resets progress, despite making the account inactive.
	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET status='error' WHERE id=$1", a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.RecordConcurrencyResult(ctx, service.AccountConcurrencyResult{AccountID: a.ID, StartedAt: time.Now(), Success: false}, cfg))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	state, ok := got.Extra[service.AutoConfigConcurrencyExtraKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(0), state["successes"])
}
