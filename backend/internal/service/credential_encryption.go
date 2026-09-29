package service

import (
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// CredentialEncryptionStatus never contains a key or a ciphertext. The local
// key belongs only to account re-login credentials, not user TOTP or payments.
type CredentialEncryptionStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
}

type OpenAICredentialEncryptor interface {
	SecretEncryptor
	EncryptionStatus() (CredentialEncryptionStatus, error)
	InitializeEncryption() (CredentialEncryptionStatus, error)
}

func (s *OpenAIOAuthReauthService) CredentialEncryptionStatus() (CredentialEncryptionStatus, error) {
	if s == nil {
		return CredentialEncryptionStatus{}, infraerrors.New(http.StatusServiceUnavailable, "OPENAI_REAUTH_UNAVAILABLE", "Credential encryption is unavailable")
	}
	if manager, ok := s.encryptor.(OpenAICredentialEncryptor); ok {
		status, err := manager.EncryptionStatus()
		if err != nil {
			return CredentialEncryptionStatus{}, credentialEncryptionStorageError()
		}
		return status, nil
	}
	status := CredentialEncryptionStatus{Configured: s.encryptionKeyConfigured, Source: "unconfigured"}
	if status.Configured {
		status.Source = "server_config"
	}
	return status, nil
}

func (s *OpenAIOAuthReauthService) InitializeCredentialEncryption() (CredentialEncryptionStatus, error) {
	if s != nil {
		if manager, ok := s.encryptor.(OpenAICredentialEncryptor); ok {
			status, err := manager.InitializeEncryption()
			if err != nil {
				return CredentialEncryptionStatus{}, credentialEncryptionStorageError()
			}
			return status, nil
		}
	}
	return CredentialEncryptionStatus{}, infraerrors.New(http.StatusServiceUnavailable, "CREDENTIAL_ENCRYPTION_UNAVAILABLE", "Credential encryption setup is unavailable")
}

func credentialEncryptionStorageError() error {
	return infraerrors.New(http.StatusServiceUnavailable, "CREDENTIAL_ENCRYPTION_STORAGE_FAILED", "Cannot access the credential encryption key. Check the persistent data directory permissions or restore its original key; existing keys will not be replaced.")
}
