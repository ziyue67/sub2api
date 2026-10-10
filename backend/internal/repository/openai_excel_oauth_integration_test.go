//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestExcelOAuthIntegrationDualGrantAtomicityAndEngineRouting(t *testing.T) {
	ctx, repo, cfg := totpRotationFixture(t)
	accountRepo := NewAccountRepository(integrationEntClient, integrationDB, nil)
	before, err := accountRepo.GetByID(ctx, cfg.AccountID)
	require.NoError(t, err)
	encryptor, err := NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)}})
	require.NoError(t, err)
	ciphertext, err := encryptor.Encrypt(`{"access_token":"fixture-excel-token"}`)
	require.NoError(t, err)
	require.NotContains(t, ciphertext, "fixture-excel-token")

	// Local Excel tasks override a Session Studio global preference. Codex keeps it.
	task, err := repo.CreateExcelTask(ctx, cfg.AccountID, "snapshot")
	require.NoError(t, err)
	require.Equal(t, "excel", task.OAuthProfile)
	_, err = repo.CreateTask(ctx, cfg.AccountID, "snapshot")
	require.Error(t, err, "same-account login remains exclusive")
	skipped, err := repo.ClaimNextTaskForRuntime(ctx, "remote", time.Minute, "", []string{"session_studio"}, "session_studio")
	require.NoError(t, err)
	require.Nil(t, skipped)
	claimed, err := repo.ClaimNextTaskForRuntime(ctx, "local", time.Minute, "password_totp", []string{"local_worker"}, "session_studio")
	require.NoError(t, err)
	require.Equal(t, task.ID, claimed.ID)
	claimed, started, err := repo.BeginDirectCallback(ctx, task.ID, "local")
	require.NoError(t, err)
	require.True(t, started)
	require.Equal(t, "excel", claimed.OAuthProfile)
	changed, err := repo.ApplyExcelCredentials(ctx, claimed, map[string]any{"email": "stale@example.test"}, ciphertext)
	require.NoError(t, err)
	require.False(t, changed)
	empty, err := repo.GetExcelCredentials(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Empty(t, empty)
	// A failed task-finalization write rolls back the separately encrypted grant.
	_, err = integrationDB.ExecContext(ctx, `CREATE FUNCTION excel_task_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic finalization failure'; END$$;
 CREATE TRIGGER excel_task_fail BEFORE UPDATE ON openai_oauth_reauth_tasks FOR EACH ROW WHEN(NEW.status='succeeded') EXECUTE FUNCTION excel_task_fail()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS excel_task_fail ON openai_oauth_reauth_tasks; DROP FUNCTION IF EXISTS excel_task_fail()`)
	})
	_, err = repo.ApplyExcelCredentials(ctx, claimed, before.Credentials, ciphertext)
	require.Error(t, err)
	empty, err = repo.GetExcelCredentials(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Empty(t, empty)
	_, err = integrationDB.ExecContext(ctx, `DROP TRIGGER excel_task_fail ON openai_oauth_reauth_tasks; DROP FUNCTION excel_task_fail()`)
	require.NoError(t, err)
	changed, err = repo.ApplyExcelCredentials(ctx, claimed, before.Credentials, ciphertext)
	require.NoError(t, err)
	require.True(t, changed)
	after, err := accountRepo.GetByID(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Equal(t, before.Credentials, after.Credentials)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Schedulable, after.Schedulable)
	completed, err := repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, service.OpenAIOAuthReauthStatusSucceeded, completed.Status)
	require.Equal(t, "excel", completed.OAuthProfile)
	saved, err := repo.GetExcelCredentials(ctx, cfg.AccountID)
	require.NoError(t, err)
	plain, err := encryptor.Decrypt(saved)
	require.NoError(t, err)
	require.Contains(t, plain, "fixture-excel-token")

	changed, err = repo.ReplaceExcelCredentials(ctx, cfg.AccountID, "wrong", "encrypted:second")
	require.NoError(t, err)
	require.False(t, changed)
	changed, err = repo.ReplaceExcelCredentials(ctx, cfg.AccountID, ciphertext, "encrypted:second")
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repo.DeleteExcelCredentials(ctx, cfg.AccountID, ciphertext))
	current, err := repo.GetExcelCredentials(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Equal(t, "encrypted:second", current)
	_, err = repo.CreateTask(ctx, cfg.AccountID, "snapshot")
	require.NoError(t, err)
	codex, err := repo.ClaimNextTaskForRuntime(ctx, "remote", time.Minute, "", []string{"session_studio"}, "session_studio")
	require.NoError(t, err)
	require.NotNil(t, codex)
	require.Equal(t, "codex", codex.OAuthProfile)
}

func TestExcelOAuthIntegrationQueueCoversExistingSwitchAndCooldown(t *testing.T) {
	ctx, repo, cfg := totpRotationFixture(t)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_build_object('openai_excel_bps',true) WHERE id=$1`, cfg.AccountID)
	require.NoError(t, err)
	ids, err := repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.Contains(t, ids, cfg.AccountID)
	task, err := repo.CreateExcelTask(ctx, cfg.AccountID, "snapshot")
	require.NoError(t, err)
	ids, err = repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.NotContains(t, ids, cfg.AccountID)
	_, err = repo.ClaimNextTask(ctx, "local", time.Minute)
	require.NoError(t, err)
	require.NoError(t, repo.MarkFailed(ctx, task.ID, "local", "bounded login failure"))
	ids, err = repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.NotContains(t, ids, cfg.AccountID)
	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET updated_at=NOW()-INTERVAL '31 minutes' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	ids, err = repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.Contains(t, ids, cfg.AccountID)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=credentials||jsonb_build_object('plan_type','free') WHERE id=$1`, cfg.AccountID)
	require.NoError(t, err)
	ids, err = repo.ListMissingExcelAuthorizations(ctx, 50)
	require.NoError(t, err)
	require.NotContains(t, ids, cfg.AccountID)
}

func TestLatestExcelAuthorizationIgnoresNewerCodexTask(t *testing.T) {
	ctx, repo, cfg := totpRotationFixture(t)
	task, err := repo.CreateExcelTask(ctx, cfg.AccountID, "test-snapshot")
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET status='failed',stage='failed',error_message='reason=security_check' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	_, err = repo.CreateTask(ctx, cfg.AccountID, "new-codex-snapshot")
	require.NoError(t, err)
	latest, err := repo.GetLatestExcelTask(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Equal(t, task.ID, latest.ID)
	require.Equal(t, "failed", latest.Status)
}

func TestExcelAuthorizationAutomaticallyActivatesRequestedRoute(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "activate", true: "cancel during login"}[cancel], func(t *testing.T) {
			ctx, repo, cfg := totpRotationFixture(t)
			accountRepo := NewAccountRepository(integrationEntClient, integrationDB, nil)
			before, err := accountRepo.GetByID(ctx, cfg.AccountID)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=extra||'{"openai_excel_bps":true,"openai_excel_bps_authorization_pending":true}'::jsonb WHERE id=$1`, cfg.AccountID)
			require.NoError(t, err)
			task, err := repo.CreateExcelTask(ctx, cfg.AccountID, "snapshot")
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET status='callback_processing',worker_id='test-worker' WHERE id=$1`, task.ID)
			require.NoError(t, err)
			task.WorkerID = "test-worker"
			if cancel {
				_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=extra||'{"openai_excel_bps":false}'::jsonb WHERE id=$1`, cfg.AccountID)
				require.NoError(t, err)
			}
			changed, err := repo.ApplyExcelCredentials(ctx, task, before.Credentials, "test-encrypted-grant")
			require.NoError(t, err)
			require.True(t, changed)
			after, err := accountRepo.GetByID(ctx, cfg.AccountID)
			require.NoError(t, err)
			require.Equal(t, !cancel, after.IsExcelBPSEnabled())
			require.Equal(t, before.Credentials, after.Credentials)
			if !cancel {
				require.NotEqual(t, true, after.Extra[service.ExcelBPSAuthorizationPendingKey])
			}
		})
	}
}

func TestExistingMissingExcelRouteAutomaticallyPrepares(t *testing.T) {
	ctx, repo, cfg := totpRotationFixture(t)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=extra||'{"openai_excel_bps":true}'::jsonb WHERE id=$1`, cfg.AccountID)
	require.NoError(t, err)
	require.NoError(t, repo.PrepareMissingExcelRoutes(ctx))
	accountRepo := NewAccountRepository(integrationEntClient, integrationDB, nil)
	account, err := accountRepo.GetByID(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Equal(t, true, account.Extra["openai_excel_bps"])
	require.Equal(t, true, account.Extra[service.ExcelBPSAuthorizationPendingKey])
	require.False(t, account.IsExcelBPSEnabled())
	require.NoError(t, repo.PrepareMissingExcelRoutes(ctx))
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO openai_excel_oauth_credentials(account_id,credentials_ciphertext) VALUES($1,'saved-test-grant')`, cfg.AccountID)
	require.NoError(t, err)
	require.NoError(t, repo.PrepareMissingExcelRoutes(ctx))
	account, err = accountRepo.GetByID(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.True(t, account.IsExcelBPSEnabled())
}

func TestBulkExcelPreparationUsesPerAccountState(t *testing.T) {
	ctx, _, first := totpRotationFixture(t)
	_, _, second := totpRotationFixture(t)
	repo := NewAccountRepository(integrationEntClient, integrationDB, nil)
	_, err := repo.BulkUpdate(ctx, []int64{first.AccountID, second.AccountID}, service.AccountBulkUpdate{
		Extra:                        map[string]any{"openai_excel_bps": true},
		ExcelBPSAuthorizationPending: map[int64]bool{first.AccountID: false, second.AccountID: true},
	})
	require.NoError(t, err)
	a, err := repo.GetByID(ctx, first.AccountID)
	require.NoError(t, err)
	require.True(t, a.IsExcelBPSEnabled())
	b, err := repo.GetByID(ctx, second.AccountID)
	require.NoError(t, err)
	require.False(t, b.IsExcelBPSEnabled())
	require.Equal(t, true, b.Extra["openai_excel_bps"])
	_, err = repo.BulkUpdate(ctx, []int64{second.AccountID}, service.AccountBulkUpdate{Extra: map[string]any{"openai_excel_bps": false}, ExcelBPSAuthorizationPending: map[int64]bool{second.AccountID: false}})
	require.NoError(t, err)
	b, err = repo.GetByID(ctx, second.AccountID)
	require.NoError(t, err)
	require.NotEqual(t, true, b.Extra[service.ExcelBPSAuthorizationPendingKey])
}
