package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type managedReauthTestEncryptor struct {
	reauthTestEncryptor
	ready bool
	err   error
}

func (e *managedReauthTestEncryptor) EncryptionStatus() (CredentialEncryptionStatus, error) {
	return CredentialEncryptionStatus{Configured: e.ready, Source: "local_file"}, e.err
}
func (e *managedReauthTestEncryptor) InitializeEncryption() (CredentialEncryptionStatus, error) {
	if e.err == nil {
		e.ready = true
	}
	return e.EncryptionStatus()
}

func TestCredentialEncryptionInitializationEnablesSavingWithoutRestart(t *testing.T) {
	svc, _, repo, _, _, _ := newReauthTestService("acct-1")
	svc.encryptionKeyConfigured = false
	svc.encryptor = &managedReauthTestEncryptor{}
	input := OpenAIOAuthReauthConfigInput{LoginEmail: "user@example.com", CredentialMode: OpenAIOAuthReauthModePasswordTOTP, Password: "fixture-password"}
	_, err := svc.SaveCredentialConfig(context.Background(), 42, input)
	require.Equal(t, "OPENAI_REAUTH_ENCRYPTION_KEY_REQUIRED", infraerrors.Reason(err))
	require.Nil(t, repo.config)
	status, err := svc.InitializeCredentialEncryption()
	require.NoError(t, err)
	require.True(t, status.Configured)
	_, err = svc.SaveCredentialConfig(context.Background(), 42, input)
	require.NoError(t, err)
	require.NotEmpty(t, repo.config.PasswordCiphertext)
	require.Empty(t, repo.config.TOTPSecretCiphertext) // An account without 2FA remains supported.
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	require.JSONEq(t, `{"configured":true,"source":"local_file"}`, string(raw))
}

func TestCredentialEncryptionStorageFailureIsRedacted(t *testing.T) {
	svc, _, repo, _, _, _ := newReauthTestService("acct-1")
	svc.encryptor = &managedReauthTestEncryptor{err: errors.New("sensitive-file-content")}
	_, err := svc.InitializeCredentialEncryption()
	require.Equal(t, "CREDENTIAL_ENCRYPTION_STORAGE_FAILED", infraerrors.Reason(err))
	require.NotContains(t, err.Error(), "sensitive-file-content")
	_, err = svc.SaveConfig(context.Background(), 42, "user@example.com", "https://mail.example.com/code")
	require.Equal(t, "CREDENTIAL_ENCRYPTION_STORAGE_FAILED", infraerrors.Reason(err))
	require.Nil(t, repo.config)
}
