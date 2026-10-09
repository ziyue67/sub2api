package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestOpenAIOAuthReauthEnginePersistsInCorrectSQLColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewOpenAIOAuthReauthRepository(db)
	mock.ExpectExec("INSERT INTO openai_oauth_reauth_configs").WithArgs(int64(42), "user@example.com", "password_totp", "session_studio", "account", nil, "enc-password", "enc-totp", "", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpsertConfig(context.Background(), &service.OpenAIOAuthReauthStoredConfig{AccountID: 42, LoginEmail: "user@example.com", CredentialMode: "password_totp", Engine: "session_studio", ProxySource: "account", PasswordCiphertext: "enc-password", TOTPSecretCiphertext: "enc-totp"}))
	mock.ExpectQuery("SELECT account_id, login_email, credential_mode, engine, proxy_source, proxy_id").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"account_id", "email", "mode", "engine", "proxy_source", "proxy_id", "password", "totp", "otp", "updated_at"}).AddRow(42, "user@example.com", "password_totp", "session_studio", "account", nil, "enc-password", "enc-totp", nil, time.Now()))
	cfg, err := repo.GetConfig(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "session_studio", cfg.Engine)
	require.Equal(t, "enc-totp", cfg.TOTPSecretCiphertext)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIOAuthReauthEngineLegacyClaimFiltersBeforeLocking(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewOpenAIOAuthReauthRepository(db)
	mock.ExpectQuery("(?s)WITH expired_callbacks.*engine.*ANY.*FOR UPDATE SKIP LOCKED").WithArgs("failed", "failed", "callback_processing", int64(1800), "queued", "running", "starting", "old-worker", "", `{"local_worker"}`, "").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	claim, err := repo.ClaimNextTask(context.Background(), "old-worker", 30*time.Minute)
	require.NoError(t, err)
	require.Nil(t, claim)
	require.NoError(t, mock.ExpectationsWereMet())
}
