package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const credentialCipherPrefix = "credential-v1:"

// A dedicated key lets an admin enable account credential storage immediately,
// without rotating the shared TOTP/payment key or changing process config.
// The prefix preserves decryption of existing server-key ciphertexts.
type credentialEncryptor struct {
	fallback             service.SecretEncryptor
	fixed                bool
	keyPath              string
	hasStoredCredentials func() (bool, error)
}

func NewOpenAICredentialEncryptor(cfg *config.Config, fallback service.SecretEncryptor, db *sql.DB) service.OpenAICredentialEncryptor {
	dir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dir == "" {
		if info, err := os.Stat("/app/data"); err == nil && info.IsDir() {
			dir = "/app/data"
		} else {
			dir = "./data"
		}
	}
	return &credentialEncryptor{fallback: fallback, fixed: cfg != nil && cfg.Totp.EncryptionKeyConfigured, keyPath: filepath.Join(dir, "secrets", "credential-operations.key"), hasStoredCredentials: func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var exists bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM openai_oauth_reauth_configs
			WHERE COALESCE(length(password_ciphertext), 0) > 0
			OR COALESCE(length(totp_secret_ciphertext), 0) > 0
			OR COALESCE(length(otp_url_ciphertext), 0) > 0)`).Scan(&exists)
		return exists, err
	}}
}

func (e *credentialEncryptor) localEncryptor() (*AESEncryptor, error) {
	info, err := os.Lstat(e.keyPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 65 {
		return nil, errors.New("credential key must be a private regular file")
	}
	f, err := os.Open(e.keyPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid credential encryption key")
	}
	return &AESEncryptor{key: key}, nil
}

func (e *credentialEncryptor) EncryptionStatus() (service.CredentialEncryptionStatus, error) {
	if e.fixed {
		return service.CredentialEncryptionStatus{Configured: true, Source: "server_config"}, nil
	}
	_, err := e.localEncryptor()
	if errors.Is(err, os.ErrNotExist) {
		if e.hasStoredCredentials != nil {
			exists, readErr := e.hasStoredCredentials()
			if readErr != nil {
				return service.CredentialEncryptionStatus{}, readErr
			}
			if exists {
				return service.CredentialEncryptionStatus{}, errors.New("restore the original credential encryption key")
			}
		}
		return service.CredentialEncryptionStatus{Source: "unconfigured"}, nil
	}
	if err != nil {
		return service.CredentialEncryptionStatus{}, err
	}
	return service.CredentialEncryptionStatus{Configured: true, Source: "local_file"}, nil
}

func (e *credentialEncryptor) InitializeEncryption() (service.CredentialEncryptionStatus, error) {
	status, err := e.EncryptionStatus()
	if err != nil || status.Configured {
		return status, err
	}
	dir := filepath.Dir(e.keyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return status, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return status, err
	}
	f, err := os.CreateTemp(dir, ".credential-key-*")
	if err != nil {
		return status, err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err = f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		return status, err
	}
	if err = f.Sync(); err != nil {
		return status, err
	}
	if err = f.Close(); err != nil {
		return status, err
	}
	// Link publishes a complete file atomically without replacing an existing
	// key, including when another process wins a concurrent initialization.
	if err = os.Link(f.Name(), e.keyPath); err != nil && !errors.Is(err, os.ErrExist) {
		return status, err
	}
	d, err := os.Open(dir)
	if err != nil {
		return status, err
	}
	defer func() { _ = d.Close() }()
	if err = d.Sync(); err != nil {
		return status, err
	}
	return e.EncryptionStatus()
}

func (e *credentialEncryptor) Encrypt(plaintext string) (string, error) {
	if e.fixed {
		return e.fallback.Encrypt(plaintext)
	}
	local, err := e.localEncryptor()
	if err != nil {
		return "", errors.New("credential encryption is unavailable")
	}
	ciphertext, err := local.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return credentialCipherPrefix + ciphertext, nil
}

func (e *credentialEncryptor) Decrypt(ciphertext string) (string, error) {
	if strings.HasPrefix(ciphertext, credentialCipherPrefix) {
		local, err := e.localEncryptor()
		if err != nil {
			return "", errors.New("credential encryption is unavailable")
		}
		return local.Decrypt(strings.TrimPrefix(ciphertext, credentialCipherPrefix))
	}
	if !e.fixed {
		return "", errors.New("original server encryption key is required")
	}
	return e.fallback.Decrypt(ciphertext)
}
