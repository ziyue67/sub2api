//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSIWCGrantCASAndWorkerOwnershipPreservePrimaryCredentials(t *testing.T) {
	ctx, repo, cfg := totpRotationFixture(t)
	accounts := NewAccountRepository(integrationEntClient, integrationDB, nil)
	before, err := accounts.GetByID(ctx, cfg.AccountID)
	require.NoError(t, err)
	host, previous, err := repo.SIWCRegistration(ctx, cfg.AccountID, "urn:uuid:first")
	require.NoError(t, err)
	require.Empty(t, previous)
	repeated, _, err := repo.SIWCRegistration(ctx, cfg.AccountID, "urn:uuid:ignored")
	require.NoError(t, err)
	require.Equal(t, host, repeated)
	task, err := repo.CreateSIWCTask(ctx, cfg.AccountID, "snapshot")
	require.NoError(t, err)
	_, err = repo.CreateTask(ctx, cfg.AccountID, "snapshot")
	require.Error(t, err)
	skipped, err := repo.ClaimNextTaskForRuntime(ctx, "remote", time.Minute, "", []string{"session_studio"}, "session_studio")
	require.NoError(t, err)
	require.Nil(t, skipped)
	claimed, err := repo.ClaimNextTaskForRuntime(ctx, "local", time.Minute, "password_totp", []string{"local_worker"}, "session_studio")
	require.NoError(t, err)
	require.Equal(t, task.ID, claimed.ID)
	require.NoError(t, repo.SetSession(ctx, task.ID, "local", "server-session"))
	claimed, started, err := repo.BeginCallback(ctx, task.ID, "local")
	require.NoError(t, err)
	require.True(t, started)
	changed, err := repo.SaveSIWCCredentials(ctx, cfg.AccountID, map[string]any{"email": "stale@example.test"}, "", "encrypted:first", true, claimed)
	require.NoError(t, err)
	require.False(t, changed)
	changed, err = repo.SaveSIWCCredentials(ctx, cfg.AccountID, before.Credentials, "wrong-snapshot", "encrypted:first", true, claimed)
	require.NoError(t, err)
	require.False(t, changed)
	changed, err = repo.SaveSIWCCredentials(ctx, cfg.AccountID, before.Credentials, "", "encrypted:first", true, claimed)
	require.NoError(t, err)
	require.True(t, changed)
	completed, err := repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", completed.Status)
	changed, err = repo.SaveSIWCCredentials(ctx, cfg.AccountID, before.Credentials, "", "encrypted:stale", false, nil)
	require.NoError(t, err)
	require.False(t, changed)
	saved, err := repo.GetSIWCCredentials(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Equal(t, "encrypted:first", saved)
	after, err := accounts.GetByID(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Equal(t, before.Credentials, after.Credentials)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Schedulable, after.Schedulable)
	// Independent refresh must tolerate rotation of the primary Codex bearer.
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{access_token}','"rotated-codex"'::jsonb) WHERE id=$1`, cfg.AccountID)
	require.NoError(t, err)
	changed, err = repo.SaveSIWCCredentials(ctx, cfg.AccountID, before.Credentials, "encrypted:first", "encrypted:refreshed", false, nil)
	require.NoError(t, err)
	require.True(t, changed)
	after, err = accounts.GetByID(ctx, cfg.AccountID)
	require.NoError(t, err)
	require.Equal(t, "rotated-codex", after.GetCredential("access_token"))
}
