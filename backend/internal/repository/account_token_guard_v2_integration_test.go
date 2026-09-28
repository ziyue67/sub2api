//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountTokenGuardV2RepositoryLeasesAndReauth(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name: "credential-guard-v2-test", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-old-token"},
	})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, account.ID)
		require.NoError(t, err)
	})
	guard := NewAccountTokenGuardV2Repository(integrationDB)
	require.NoError(t, guard.UpsertAccount(ctx, account.ID, false, false))
	claimed, err := guard.ClaimDue(ctx, "instance-a", time.Minute, 10)
	require.NoError(t, err)
	require.Empty(t, claimed, "paused accounts must not be scheduled")
	require.NoError(t, guard.UpsertAccount(ctx, account.ID, true, true))
	claimed, err = guard.ClaimDue(ctx, "instance-a", time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	other, err := guard.ClaimAccount(ctx, account.ID, "instance-b", time.Minute)
	require.NoError(t, err)
	require.Nil(t, other, "a second instance cannot claim a live lease")
	now := time.Now().Truncate(time.Microsecond)
	completion := service.AccountTokenGuardV2ProbeCompletion{
		ProbeState: service.AccountTokenGuardV2ProbeOK, LastProbeAt: now, NextProbeAt: now.Add(time.Hour),
	}
	require.ErrorIs(t, guard.CompleteProbe(ctx, account.ID, "instance-b", completion), sql.ErrNoRows)
	require.NoError(t, guard.CompleteProbe(ctx, account.ID, "instance-a", completion))
	stored, err := guard.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.Empty(t, stored.LeaseOwner)
	require.Nil(t, stored.LeaseUntil)
	require.Equal(t, service.AccountTokenGuardV2ProbeOK, stored.ProbeState)
	claimed, err = guard.ClaimDue(ctx, "instance-b", time.Minute, 10)
	require.NoError(t, err)
	require.Empty(t, claimed)
	require.NoError(t, guard.RescheduleEnabled(ctx))
	claimed, err = guard.ClaimDue(ctx, "instance-b", time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = integrationDB.ExecContext(ctx, `UPDATE account_token_guard_v2_accounts SET lease_until = NOW() - INTERVAL '1 second' WHERE account_id = $1`, account.ID)
	require.NoError(t, err)
	other, err = guard.ClaimAccount(ctx, account.ID, "instance-c", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, other, "expired leases must be recoverable")
	require.ErrorIs(t, guard.CompleteProbe(ctx, account.ID, "instance-b", completion), sql.ErrNoRows)

	reauth := NewOpenAIOAuthReauthRepository(integrationDB)
	require.NoError(t, reauth.UpsertConfig(ctx, &service.OpenAIOAuthReauthStoredConfig{
		AccountID: account.ID, LoginEmail: "guard@example.com", CredentialMode: service.OpenAIOAuthReauthModePasswordTOTP,
		ProxySource: service.OpenAIOAuthReauthProxySourceAccount, PasswordCiphertext: "synthetic-ciphertext",
	}))
	config, err := reauth.GetConfig(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-ciphertext", config.PasswordCiphertext)
	require.Empty(t, config.OTPURLCiphertext)
	task, err := reauth.CreateTask(ctx, account.ID, "synthetic-hash")
	require.NoError(t, err)
	_, err = reauth.CreateTask(ctx, account.ID, "another-hash")
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	running, err := reauth.ClaimNextTask(ctx, "worker-a", time.Minute)
	require.NoError(t, err)
	require.Equal(t, task.ID, running.ID)
	duplicate, err := reauth.ClaimNextTask(ctx, "worker-b", time.Minute)
	require.NoError(t, err)
	require.Nil(t, duplicate)
	require.Error(t, reauth.UpdateStage(ctx, task.ID, "worker-b", service.OpenAIOAuthReauthStageStarting))
	require.NoError(t, reauth.UpdateStage(ctx, task.ID, "worker-a", service.OpenAIOAuthReauthStagePasswordSubmitted))
	_, started, err := reauth.BeginDirectCallback(ctx, task.ID, "worker-a")
	require.NoError(t, err)
	require.True(t, started)
	accountRepo := NewAccountRepository(testEntClient(t), integrationDB, nil).(service.OpenAIOAuthReauthCredentialUpdater)
	updated := map[string]any{"access_token": "test-new-token"}
	applied, err := accountRepo.ApplyOpenAIOAuthReauth(ctx, task.ID, "worker-a", account.ID, map[string]any{"access_token": "stale-token"}, updated, nil)
	require.NoError(t, err)
	require.False(t, applied)
	applied, err = accountRepo.ApplyOpenAIOAuthReauth(ctx, task.ID, "worker-a", account.ID, account.Credentials, updated, nil)
	require.NoError(t, err)
	require.True(t, applied)
	done, err := reauth.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, service.OpenAIOAuthReauthStatusSucceeded, done.Status)
}
