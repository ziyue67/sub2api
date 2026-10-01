package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type openAIOAuthReauthRepository struct {
	db *sql.DB
}

func NewOpenAIOAuthReauthRepository(db *sql.DB) service.OpenAIOAuthReauthRepository {
	return &openAIOAuthReauthRepository{db: db}
}

func (r *openAIOAuthReauthRepository) UpsertConfig(ctx context.Context, config *service.OpenAIOAuthReauthStoredConfig) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO openai_oauth_reauth_configs (
			account_id, login_email, credential_mode, engine, proxy_source, proxy_id,
			password_ciphertext, totp_secret_ciphertext, otp_url_ciphertext
		)
		VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'local_worker'), $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''))
		ON CONFLICT (account_id) DO UPDATE
		SET login_email = EXCLUDED.login_email,
			credential_mode = EXCLUDED.credential_mode,
			engine = EXCLUDED.engine,
			proxy_source = EXCLUDED.proxy_source,
			proxy_id = EXCLUDED.proxy_id,
			password_ciphertext = EXCLUDED.password_ciphertext,
			totp_secret_ciphertext = EXCLUDED.totp_secret_ciphertext,
			otp_url_ciphertext = EXCLUDED.otp_url_ciphertext,
			updated_at = NOW()
	`, config.AccountID, config.LoginEmail, config.CredentialMode, config.Engine, config.ProxySource, config.ProxyID,
		config.PasswordCiphertext, config.TOTPSecretCiphertext, config.OTPURLCiphertext)
	return err
}

func (r *openAIOAuthReauthRepository) GetConfig(ctx context.Context, accountID int64) (*service.OpenAIOAuthReauthStoredConfig, error) {
	var cfg service.OpenAIOAuthReauthStoredConfig
	var proxyID sql.NullInt64
	var passwordCiphertext, totpSecretCiphertext, otpURLCiphertext sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT account_id, login_email, credential_mode, engine, proxy_source, proxy_id,
			password_ciphertext, totp_secret_ciphertext, otp_url_ciphertext, updated_at
		FROM openai_oauth_reauth_configs
		WHERE account_id = $1
	`, accountID).Scan(
		&cfg.AccountID, &cfg.LoginEmail, &cfg.CredentialMode, &cfg.Engine, &cfg.ProxySource, &proxyID,
		&passwordCiphertext, &totpSecretCiphertext, &otpURLCiphertext, &cfg.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cfg.PasswordCiphertext = passwordCiphertext.String
	cfg.TOTPSecretCiphertext = totpSecretCiphertext.String
	cfg.OTPURLCiphertext = otpURLCiphertext.String
	if proxyID.Valid {
		cfg.ProxyID = &proxyID.Int64
	}
	return &cfg, nil
}

func (r *openAIOAuthReauthRepository) CreateTask(ctx context.Context, accountID int64, expectedCredentialsHash string) (*service.OpenAIOAuthReauthTaskRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO openai_oauth_reauth_tasks (account_id, status, stage, expected_credentials_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING id, account_id, status, stage, COALESCE(worker_id, ''),
			COALESCE(auth_session_id, ''), expected_credentials_hash,
			COALESCE(error_message, ''), attempt,
			created_at, updated_at, finished_at
	`, accountID, service.OpenAIOAuthReauthStatusQueued, service.OpenAIOAuthReauthStageQueued, expectedCredentialsHash)
	record, err := scanOpenAIOAuthReauthTask(row)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return nil, infraerrors.Conflict("OPENAI_REAUTH_TASK_ALREADY_ACTIVE", "a re-login task is already active for this account")
		}
		return nil, err
	}
	return record, nil
}

func (r *openAIOAuthReauthRepository) GetLatestTask(ctx context.Context, accountID int64) (*service.OpenAIOAuthReauthTaskRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, status, stage, COALESCE(worker_id, ''),
			COALESCE(auth_session_id, ''), expected_credentials_hash,
			COALESCE(error_message, ''), attempt,
			created_at, updated_at, finished_at
		FROM openai_oauth_reauth_tasks
		WHERE account_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, accountID)
	record, err := scanOpenAIOAuthReauthTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return record, err
}

func (r *openAIOAuthReauthRepository) GetTask(ctx context.Context, taskID int64) (*service.OpenAIOAuthReauthTaskRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, status, stage, COALESCE(worker_id, ''),
			COALESCE(auth_session_id, ''), expected_credentials_hash,
			COALESCE(error_message, ''), attempt,
			created_at, updated_at, finished_at
		FROM openai_oauth_reauth_tasks
		WHERE id = $1
	`, taskID)
	record, err := scanOpenAIOAuthReauthTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return record, err
}

func (r *openAIOAuthReauthRepository) ClaimNextTask(ctx context.Context, workerID string, staleAfter time.Duration) (*service.OpenAIOAuthReauthTaskRecord, error) {
	return r.claimNextTask(ctx, workerID, staleAfter, "")
}

func (r *openAIOAuthReauthRepository) ClaimNextPasswordTask(ctx context.Context, workerID string, staleAfter time.Duration) (*service.OpenAIOAuthReauthTaskRecord, error) {
	return r.claimNextTask(ctx, workerID, staleAfter, service.OpenAIOAuthReauthModePasswordTOTP)
}

func (r *openAIOAuthReauthRepository) claimNextTask(ctx context.Context, workerID string, staleAfter time.Duration, mode string) (*service.OpenAIOAuthReauthTaskRecord, error) {
	return r.ClaimNextTaskForEngines(ctx, workerID, staleAfter, mode, []string{service.OpenAIOAuthReauthEngineLocal})
}

func (r *openAIOAuthReauthRepository) ClaimNextTaskForEngines(ctx context.Context, workerID string, staleAfter time.Duration, mode string, engines []string) (*service.OpenAIOAuthReauthTaskRecord, error) {
	return r.ClaimNextTaskForRuntime(ctx, workerID, staleAfter, mode, engines, "")
}

func (r *openAIOAuthReauthRepository) ClaimNextTaskForRuntime(ctx context.Context, workerID string, staleAfter time.Duration, mode string, engines []string, globalEngine string) (*service.OpenAIOAuthReauthTaskRecord, error) {
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	staleSeconds := int64(staleAfter.Seconds())
	row := r.db.QueryRowContext(ctx, `
		WITH expired_callbacks AS (
			UPDATE openai_oauth_reauth_tasks
			SET status = $1,
				stage = $2,
				error_message = 'callback processing expired before completion',
				finished_at = NOW(),
				updated_at = NOW()
			WHERE status = $3
				AND claimed_at < NOW() - ($4 * INTERVAL '1 second')
		), next_task AS (
			SELECT id
			FROM openai_oauth_reauth_tasks
			WHERE (status = $5
				OR (
					status = $6
					AND claimed_at < NOW() - ($4 * INTERVAL '1 second')
				))
				AND ($9 = '' OR EXISTS (
					SELECT 1 FROM openai_oauth_reauth_configs AS config
					WHERE config.account_id = openai_oauth_reauth_tasks.account_id AND config.credential_mode = $9
				))
				AND COALESCE((SELECT CASE WHEN $11 <> '' THEN
					CASE WHEN config.credential_mode = 'password_totp' THEN $11 ELSE 'local_worker' END
					ELSE engine END FROM openai_oauth_reauth_configs AS config
					WHERE config.account_id = openai_oauth_reauth_tasks.account_id), 'local_worker') = ANY($10::text[])
			ORDER BY created_at ASC, id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE openai_oauth_reauth_tasks AS tasks
		SET status = $6,
			stage = $7,
			worker_id = $8,
			auth_session_id = NULL,
			error_message = NULL,
			attempt = tasks.attempt + 1,
			claimed_at = NOW(),
			finished_at = NULL,
			updated_at = NOW()
		FROM next_task
		WHERE tasks.id = next_task.id
		RETURNING tasks.id, tasks.account_id, tasks.status, tasks.stage,
			COALESCE(tasks.worker_id, ''), COALESCE(tasks.auth_session_id, ''),
			tasks.expected_credentials_hash, COALESCE(tasks.error_message, ''), tasks.attempt,
			tasks.created_at, tasks.updated_at, tasks.finished_at
	`,
		service.OpenAIOAuthReauthStatusFailed,
		service.OpenAIOAuthReauthStageFailed,
		service.OpenAIOAuthReauthStatusCallbackProcessing,
		staleSeconds,
		service.OpenAIOAuthReauthStatusQueued,
		service.OpenAIOAuthReauthStatusRunning,
		service.OpenAIOAuthReauthStageStarting,
		workerID, mode, pq.Array(engines), globalEngine,
	)
	record, err := scanOpenAIOAuthReauthTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return record, err
}

func (r *openAIOAuthReauthRepository) SetSession(ctx context.Context, taskID int64, workerID, sessionID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_oauth_reauth_tasks
		SET auth_session_id = $3,
			updated_at = NOW()
		WHERE id = $1 AND worker_id = $2 AND status = $4
	`, taskID, workerID, sessionID, service.OpenAIOAuthReauthStatusRunning)
	return requireOpenAIOAuthReauthTaskUpdate(result, err)
}

func (r *openAIOAuthReauthRepository) UpdateStage(ctx context.Context, taskID int64, workerID, stage string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_oauth_reauth_tasks
		SET stage = $3,
			updated_at = NOW()
		WHERE id = $1 AND worker_id = $2
			AND status IN ($4, $5)
	`, taskID, workerID, stage, service.OpenAIOAuthReauthStatusRunning, service.OpenAIOAuthReauthStatusCallbackProcessing)
	return requireOpenAIOAuthReauthTaskUpdate(result, err)
}

func (r *openAIOAuthReauthRepository) BeginCallback(ctx context.Context, taskID int64, workerID string) (*service.OpenAIOAuthReauthTaskRecord, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE openai_oauth_reauth_tasks
		SET status = $3,
			stage = $4,
			updated_at = NOW()
		WHERE id = $1 AND worker_id = $2 AND status = $5
			AND auth_session_id IS NOT NULL AND BTRIM(auth_session_id) <> ''
		RETURNING id, account_id, status, stage, COALESCE(worker_id, ''),
			COALESCE(auth_session_id, ''), expected_credentials_hash,
			COALESCE(error_message, ''), attempt,
			created_at, updated_at, finished_at
	`, taskID, workerID, service.OpenAIOAuthReauthStatusCallbackProcessing,
		service.OpenAIOAuthReauthStageExchangingToken, service.OpenAIOAuthReauthStatusRunning)
	record, err := scanOpenAIOAuthReauthTask(row)
	if err == nil {
		return record, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	existing, getErr := r.GetTask(ctx, taskID)
	if getErr != nil {
		return nil, false, getErr
	}
	if existing != nil && (existing.Status == service.OpenAIOAuthReauthStatusSucceeded || existing.Status == service.OpenAIOAuthReauthStatusFailed) {
		return existing, false, nil
	}
	return nil, false, fmt.Errorf("task is not owned by worker or has no auth session")
}

func (r *openAIOAuthReauthRepository) BeginDirectCallback(ctx context.Context, taskID int64, workerID string) (*service.OpenAIOAuthReauthTaskRecord, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE openai_oauth_reauth_tasks
		SET status = $3,
			stage = $4,
			updated_at = NOW()
		WHERE id = $1 AND worker_id = $2 AND status = $5
		RETURNING id, account_id, status, stage, COALESCE(worker_id, ''),
			COALESCE(auth_session_id, ''), expected_credentials_hash,
			COALESCE(error_message, ''), attempt,
			created_at, updated_at, finished_at
	`, taskID, workerID, service.OpenAIOAuthReauthStatusCallbackProcessing,
		service.OpenAIOAuthReauthStageExchangingToken, service.OpenAIOAuthReauthStatusRunning)
	record, err := scanOpenAIOAuthReauthTask(row)
	if err == nil {
		return record, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	existing, getErr := r.GetTask(ctx, taskID)
	if getErr != nil {
		return nil, false, getErr
	}
	if existing != nil && (existing.Status == service.OpenAIOAuthReauthStatusSucceeded || existing.Status == service.OpenAIOAuthReauthStatusFailed) {
		return existing, false, nil
	}
	return nil, false, fmt.Errorf("task is not owned by worker")
}

func (r *openAIOAuthReauthRepository) MarkSucceeded(ctx context.Context, taskID int64, workerID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_oauth_reauth_tasks
		SET status = $2,
			stage = $3,
			error_message = NULL,
			finished_at = NOW(),
			updated_at = NOW()
		WHERE id = $1 AND worker_id = $4 AND status = $5
	`, taskID, service.OpenAIOAuthReauthStatusSucceeded, service.OpenAIOAuthReauthStageSucceeded,
		workerID, service.OpenAIOAuthReauthStatusCallbackProcessing)
	return requireOpenAIOAuthReauthTaskUpdate(result, err)
}

func (r *openAIOAuthReauthRepository) MarkFailed(ctx context.Context, taskID int64, workerID, reason string) error {
	reason = strings.TrimSpace(reason)
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_oauth_reauth_tasks
		SET status = $2,
			stage = $3,
			error_message = $4,
			finished_at = NOW(),
			updated_at = NOW()
		WHERE id = $1 AND worker_id = $5 AND status IN ($6, $7)
	`, taskID, service.OpenAIOAuthReauthStatusFailed, service.OpenAIOAuthReauthStageFailed, reason,
		workerID, service.OpenAIOAuthReauthStatusRunning, service.OpenAIOAuthReauthStatusCallbackProcessing)
	return requireOpenAIOAuthReauthTaskUpdate(result, err)
}

type openAIOAuthReauthRow interface {
	Scan(dest ...any) error
}

func scanOpenAIOAuthReauthTask(row openAIOAuthReauthRow) (*service.OpenAIOAuthReauthTaskRecord, error) {
	var record service.OpenAIOAuthReauthTaskRecord
	var finishedAt sql.NullTime
	if err := row.Scan(
		&record.ID, &record.AccountID, &record.Status, &record.Stage,
		&record.WorkerID, &record.AuthSessionID, &record.ExpectedCredentialsHash,
		&record.Error, &record.Attempt,
		&record.CreatedAt, &record.UpdatedAt, &finishedAt,
	); err != nil {
		return nil, err
	}
	if finishedAt.Valid {
		record.FinishedAt = &finishedAt.Time
	}
	return &record, nil
}

func requireOpenAIOAuthReauthTaskUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
