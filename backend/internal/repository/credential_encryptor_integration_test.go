//go:build integration

package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCredentialEncryptionDatabaseRestartAndKeyLoss(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	ctx := context.Background()
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ab", 32)}}
	fallback, err := NewAESEncryptor(cfg)
	require.NoError(t, err)
	e := NewOpenAICredentialEncryptor(cfg, fallback, integrationDB)
	status, err := e.EncryptionStatus()
	require.NoError(t, err)
	require.False(t, status.Configured)
	status, err = e.InitializeEncryption()
	require.NoError(t, err)
	require.True(t, status.Configured)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name: "credential-encryption-integration", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "fixture-access-token"},
	})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, account.ID)
		require.NoError(t, err)
	})
	ciphertext, err := e.Encrypt("fixture-login-password")
	require.NoError(t, err)
	r := NewOpenAIOAuthReauthRepository(integrationDB)
	require.NoError(t, r.UpsertConfig(ctx, &service.OpenAIOAuthReauthStoredConfig{
		AccountID: account.ID, LoginEmail: "fixture@example.com", CredentialMode: service.OpenAIOAuthReauthModePasswordTOTP,
		ProxySource: service.OpenAIOAuthReauthProxySourceAccount, PasswordCiphertext: ciphertext,
	}))
	stored, err := r.GetConfig(ctx, account.ID)
	require.NoError(t, err)
	require.NotContains(t, stored.PasswordCiphertext, "fixture-login-password")
	// On restart the ephemeral shared key can change; the dedicated key persists.
	cfg.Totp.EncryptionKey = strings.Repeat("cd", 32)
	fallback, err = NewAESEncryptor(cfg)
	require.NoError(t, err)
	restarted := NewOpenAICredentialEncryptor(cfg, fallback, integrationDB)
	actual, err := restarted.Decrypt(stored.PasswordCiphertext)
	require.NoError(t, err)
	require.Equal(t, "fixture-login-password", actual)
	keyPath := filepath.Join(dir, "secrets", "credential-operations.key")
	key, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(keyPath))
	_, err = restarted.InitializeEncryption()
	require.Error(t, err, "existing encrypted rows require the original key, not a replacement")
	_, err = os.Stat(keyPath)
	require.True(t, os.IsNotExist(err))
	require.NoError(t, os.WriteFile(keyPath, key, 0o600))
	actual, err = restarted.Decrypt(stored.PasswordCiphertext)
	require.NoError(t, err)
	require.Equal(t, "fixture-login-password", actual)
}
