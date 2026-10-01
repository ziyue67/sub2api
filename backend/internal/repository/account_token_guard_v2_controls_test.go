package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountTokenGuardV2SwitchUpdatesOnlySuppliedFlag(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &accountTokenGuardV2Repository{db: db}
	disabled := false
	mock.ExpectExec("(?s)UPDATE account_token_guard_v2_accounts.*COALESCE.*WHERE account_id").WithArgs(int64(42), false, nil).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateSwitches(context.Background(), 42, &disabled, nil))
	mock.ExpectExec("UPDATE account_token_guard_v2_accounts").WithArgs(int64(42), nil, false).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateSwitches(context.Background(), 42, nil, &disabled))
	mock.ExpectExec("UPDATE account_token_guard_v2_accounts").WithArgs(int64(404), nil, false).WillReturnResult(sqlmock.NewResult(0, 0))
	require.Error(t, repo.UpdateSwitches(context.Background(), 404, nil, &disabled))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIOAuthReauthRuntimeEngineFiltersBeforeClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &openAIOAuthReauthRepository{db: db}
	mock.ExpectQuery("(?s)WITH expired_callbacks.*CASE WHEN.*credential_mode.*ELSE engine END.*ANY.*FOR UPDATE SKIP LOCKED").WithArgs("failed", "failed", "callback_processing", int64(1800), "queued", "running", "starting", "worker", "", sqlmock.AnyArg(), "session_studio").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	claim, err := repo.ClaimNextTaskForRuntime(context.Background(), "worker", 30*time.Minute, "", []string{service.OpenAIOAuthReauthEngineLocal}, service.OpenAIOAuthReauthEngineSessionStudio)
	require.NoError(t, err)
	require.Nil(t, claim)
	require.NoError(t, mock.ExpectationsWereMet())
}
