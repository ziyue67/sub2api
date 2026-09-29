//go:build unit

package repository

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func testCredentialEncryptor(t *testing.T) *credentialEncryptor {
	t.Helper()
	return &credentialEncryptor{fallback: aesEncryptor(t), keyPath: filepath.Join(t.TempDir(), "secrets", "credential-operations.key")}
}

func TestCredentialEncryptionPersistsWithoutChangingSharedKey(t *testing.T) {
	e := testCredentialEncryptor(t)
	status, err := e.EncryptionStatus()
	require.NoError(t, err)
	require.False(t, status.Configured)
	_, err = e.Encrypt("fixture-password")
	require.Error(t, err)
	legacy, err := e.fallback.Encrypt("unrelated-existing-secret")
	require.NoError(t, err)
	status, err = e.InitializeEncryption()
	require.NoError(t, err)
	require.True(t, status.Configured)
	require.Equal(t, "local_file", status.Source)
	before, err := os.ReadFile(e.keyPath)
	require.NoError(t, err)
	info, err := os.Stat(e.keyPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	ciphertext, err := e.Encrypt("fixture-password")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ciphertext, credentialCipherPrefix))
	require.NotContains(t, ciphertext, "fixture-password")
	// A new instance simulates restart; it has no cached key or initialized flag.
	restarted := &credentialEncryptor{fallback: aesEncryptor(t), keyPath: e.keyPath}
	plaintext, err := restarted.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, "fixture-password", plaintext)
	_, err = restarted.InitializeEncryption()
	require.NoError(t, err)
	after, err := os.ReadFile(e.keyPath)
	require.NoError(t, err)
	require.Equal(t, before, after)
	plaintext, err = e.fallback.Decrypt(legacy)
	require.NoError(t, err)
	require.Equal(t, "unrelated-existing-secret", plaintext)
}

func TestCredentialEncryptionConfiguredServerKeyAndMixedCiphertexts(t *testing.T) {
	e := testCredentialEncryptor(t)
	e.fixed = true
	status, err := e.InitializeEncryption()
	require.NoError(t, err)
	require.Equal(t, "server_config", status.Source)
	_, err = os.Stat(e.keyPath)
	require.True(t, os.IsNotExist(err))
	legacy, err := e.Encrypt("existing")
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(legacy, credentialCipherPrefix))
	e.fixed = false
	_, err = e.Decrypt(legacy)
	require.Error(t, err)
	_, err = e.InitializeEncryption()
	require.NoError(t, err)
	local, err := e.Encrypt("new")
	require.NoError(t, err)
	e.fixed = true
	for ciphertext, expected := range map[string]string{legacy: "existing", local: "new"} {
		actual, err := e.Decrypt(ciphertext)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
}

func TestCredentialEncryptionConcurrentInitialization(t *testing.T) {
	e := testCredentialEncryptor(t)
	const n = 16
	results := make(chan string, n)
	errorsCh := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			instance := &credentialEncryptor{fallback: e.fallback, keyPath: e.keyPath}
			if _, err := instance.InitializeEncryption(); err != nil {
				errorsCh <- err
				return
			}
			ciphertext, err := instance.Encrypt("concurrent")
			if err != nil {
				errorsCh <- err
				return
			}
			results <- ciphertext
		}()
	}
	wg.Wait()
	close(errorsCh)
	close(results)
	for err := range errorsCh {
		require.NoError(t, err)
	}
	require.Len(t, results, n)
	for ciphertext := range results {
		plaintext, err := e.Decrypt(ciphertext)
		require.NoError(t, err)
		require.Equal(t, "concurrent", plaintext)
	}
}

func TestCredentialEncryptionNeverReplacesInvalidOrExposedKey(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o644} {
		t.Run(mode.String(), func(t *testing.T) {
			e := testCredentialEncryptor(t)
			require.NoError(t, os.MkdirAll(filepath.Dir(e.keyPath), 0o700))
			const invalid = "fixture-do-not-overwrite"
			require.NoError(t, os.WriteFile(e.keyPath, []byte(invalid), mode))
			_, err := e.InitializeEncryption()
			require.Error(t, err)
			actual, err := os.ReadFile(e.keyPath)
			require.NoError(t, err)
			require.Equal(t, invalid, string(actual))
		})
	}
}

func TestCredentialEncryptionRefusesMissingKeyWithExistingCredentials(t *testing.T) {
	for _, readErr := range []error{nil, errors.New("database unavailable")} {
		e := testCredentialEncryptor(t)
		e.hasStoredCredentials = func() (bool, error) { return true, readErr }
		_, err := e.InitializeEncryption()
		require.Error(t, err)
		_, err = os.Stat(e.keyPath)
		require.True(t, os.IsNotExist(err))
	}
}

func TestCredentialEncryptionRejectsSymlinkAndUnwritableDirectory(t *testing.T) {
	e := testCredentialEncryptor(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(e.keyPath), 0o700))
	target := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(target, []byte(strings.Repeat("ab", 32)), 0o600))
	require.NoError(t, os.Symlink(target, e.keyPath))
	_, err := e.InitializeEncryption()
	require.Error(t, err)
	e = testCredentialEncryptor(t)
	require.NoError(t, os.WriteFile(filepath.Dir(e.keyPath), []byte("block directory creation"), 0o600))
	_, err = e.InitializeEncryption()
	require.Error(t, err)
}

func TestCredentialEncryptionProviderChecksExistingDatabaseSecrets(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "first setup", true: "restore required"}[exists], func(t *testing.T) {
			t.Setenv("DATA_DIR", t.TempDir())
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectQuery("SELECT EXISTS ").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(exists))
			e := NewOpenAICredentialEncryptor(&config.Config{}, aesEncryptor(t), db)
			status, err := e.InitializeEncryption()
			if exists {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.True(t, status.Configured)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
