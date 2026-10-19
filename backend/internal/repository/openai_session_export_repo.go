package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type openAISessionExportRepository struct{ db *sql.DB }

func NewOpenAISessionExportRepository(db *sql.DB) service.OpenAISessionExportRepository {
	return &openAISessionExportRepository{db: db}
}

func (r *openAISessionExportRepository) CreateSessionExportTask(ctx context.Context, accountID int64, hash string) (*service.OpenAISessionExportTaskRecord, error) {
	var task service.OpenAISessionExportTaskRecord
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO openai_session_export_tasks (account_id, expected_credentials_hash)
		SELECT id, $2 FROM accounts
		WHERE id=$1 AND deleted_at IS NULL AND platform='openai' AND type='oauth' AND parent_account_id IS NULL
		RETURNING id, account_id, status, stage, worker_id, expected_credentials_hash, error_code,
			attempt, created_at, updated_at, claimed_at, finished_at
	`, accountID, hash).Scan(
		&task.ID, &task.AccountID, &task.Status, &task.Stage, &task.WorkerID, &task.ExpectedCredentialsHash,
		&task.ErrorCode, &task.Attempt, &task.CreatedAt, &task.UpdatedAt, &task.ClaimedAt, &task.FinishedAt,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return nil, infraerrors.Conflict("OPENAI_SESSION_EXPORT_TASK_ACTIVE", "A Session export task is already active for this account")
		}
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.BadRequest("OPENAI_SESSION_EXPORT_ACCOUNT_INVALID", "Only an OpenAI OAuth parent account can export a ChatGPT Session")
		}
		return nil, err
	}
	return &task, nil
}

func (r *openAISessionExportRepository) GetSessionExportTask(ctx context.Context, taskID int64) (*service.OpenAISessionExportTaskRecord, error) {
	var task service.OpenAISessionExportTaskRecord
	err := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, status, stage, worker_id, expected_credentials_hash, error_code,
			attempt, created_at, updated_at, claimed_at, finished_at
		FROM openai_session_export_tasks WHERE id=$1
	`, taskID).Scan(
		&task.ID, &task.AccountID, &task.Status, &task.Stage, &task.WorkerID, &task.ExpectedCredentialsHash,
		&task.ErrorCode, &task.Attempt, &task.CreatedAt, &task.UpdatedAt, &task.ClaimedAt, &task.FinishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &task, err
}

func (r *openAISessionExportRepository) ExpireActiveSessionExportTasks(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE openai_session_export_tasks
		SET status='expired', stage='failed', error_code='api_restarted', finished_at=NOW(), updated_at=NOW()
		WHERE status IN ('queued', 'running')
	`)
	return err
}

func (r *openAISessionExportRepository) ClaimSessionExportTask(ctx context.Context, workerID string, staleAfter time.Duration) (*service.OpenAISessionExportTaskRecord, error) {
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	seconds := int64(staleAfter.Seconds())
	var task service.OpenAISessionExportTaskRecord
	err := r.db.QueryRowContext(ctx, `
		WITH next AS (
			SELECT id FROM openai_session_export_tasks
			WHERE status='queued' OR (status='running' AND claimed_at < NOW() - ($2 * INTERVAL '1 second'))
			ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE openai_session_export_tasks t
		SET status='running', stage='starting', worker_id=$1, attempt=t.attempt+1,
			claimed_at=NOW(), updated_at=NOW(), error_code=''
		FROM next WHERE t.id=next.id
		RETURNING t.id, t.account_id, t.status, t.stage, t.worker_id, t.expected_credentials_hash,
			t.error_code, t.attempt, t.created_at, t.updated_at, t.claimed_at, t.finished_at
	`, workerID, seconds).Scan(
		&task.ID, &task.AccountID, &task.Status, &task.Stage, &task.WorkerID, &task.ExpectedCredentialsHash,
		&task.ErrorCode, &task.Attempt, &task.CreatedAt, &task.UpdatedAt, &task.ClaimedAt, &task.FinishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &task, err
}

func (r *openAISessionExportRepository) UpdateSessionExportStage(ctx context.Context, taskID int64, workerID, stage string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_session_export_tasks SET stage=$1, updated_at=NOW()
		WHERE id=$2 AND worker_id=$3 AND status='running'
	`, stage, taskID, workerID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return infraerrors.Conflict("OPENAI_SESSION_EXPORT_TASK_NOT_ACTIVE", "Session export task is no longer active")
	}
	return nil
}

func (r *openAISessionExportRepository) FinishSessionExportTask(ctx context.Context, taskID int64, workerID, status, errorCode string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_session_export_tasks SET status=$1,
			stage=CASE WHEN $1='succeeded' THEN 'succeeded' ELSE 'failed' END,
			error_code=$2, finished_at=NOW(), updated_at=NOW()
		WHERE id=$3 AND worker_id=$4 AND status IN ('queued', 'running')
	`, status, errorCode, taskID, workerID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return infraerrors.Conflict("OPENAI_SESSION_EXPORT_TASK_NOT_ACTIVE", "Session export task is no longer active")
	}
	return nil
}
