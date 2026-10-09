package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *openAIOAuthReauthRepository) CreateExcelTask(ctx context.Context, accountID int64, hash string) (*service.OpenAIOAuthReauthTaskRecord, error) {
	row := r.db.QueryRowContext(ctx, `
 INSERT INTO openai_oauth_reauth_tasks(account_id,status,stage,expected_credentials_hash,oauth_profile)
 VALUES($1,'queued','queued',$2,'excel')
 RETURNING id,account_id,status,stage,COALESCE(worker_id,''),COALESCE(auth_session_id,''),
 expected_credentials_hash,COALESCE(error_message,''),attempt,created_at,updated_at,finished_at,oauth_profile`, accountID, hash)
	record, err := scanOpenAIOAuthReauthTask(row)
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code == "23505" {
		return nil, infraerrors.Conflict("OPENAI_REAUTH_TASK_ALREADY_ACTIVE", "an authorization task is already active for this account")
	}
	return record, err
}

func (r *openAIOAuthReauthRepository) GetExcelCredentials(ctx context.Context, id int64) (string, error) {
	var ciphertext string
	err := r.db.QueryRowContext(ctx, `SELECT credentials_ciphertext FROM openai_excel_oauth_credentials WHERE account_id=$1`, id).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ciphertext, err
}

func (r *openAIOAuthReauthRepository) ApplyExcelCredentials(ctx context.Context, record *service.OpenAIOAuthReauthTaskRecord, expected map[string]any, ciphertext string) (bool, error) {
	raw, err := json.Marshal(expected)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT a.id FROM accounts a JOIN openai_oauth_reauth_tasks t ON t.account_id=a.id
 WHERE t.id=$1 AND t.worker_id=$2 AND t.status='callback_processing' AND t.oauth_profile='excel'
 AND a.id=$3 AND a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth' AND a.credentials=$4::jsonb
 FOR UPDATE OF a,t`, record.ID, record.WorkerID, record.AccountID, string(raw)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO openai_excel_oauth_credentials(account_id,credentials_ciphertext)
 VALUES($1,$2) ON CONFLICT(account_id) DO UPDATE SET credentials_ciphertext=EXCLUDED.credentials_ciphertext,updated_at=NOW()`, id, ciphertext); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET status='succeeded',stage='succeeded',error_message=NULL,finished_at=NOW(),updated_at=NOW() WHERE id=$1`, record.ID); err != nil {
		return false, err
	}
	// No primary credentials, account health, cooldown or admin routing edits.
	return true, tx.Commit()
}

func (r *openAIOAuthReauthRepository) ReplaceExcelCredentials(ctx context.Context, id int64, expected, replacement string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE openai_excel_oauth_credentials SET credentials_ciphertext=$3,updated_at=NOW() WHERE account_id=$1 AND credentials_ciphertext=$2`, id, expected, replacement)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *openAIOAuthReauthRepository) ListMissingExcelAuthorizations(ctx context.Context, limit int) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT a.id FROM accounts a
 JOIN openai_oauth_reauth_configs c ON c.account_id=a.id
 LEFT JOIN openai_excel_oauth_credentials e ON e.account_id=a.id
 WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth'
 AND a.parent_account_id IS NULL AND a.extra->'openai_excel_bps'='true'::jsonb
 AND COALESCE(lower(trim(a.credentials->>'plan_type')),'') <> 'free'
 AND COALESCE(a.credentials->>'client_id','') <> 'app_fnr0pYvVwwFDocDumLG3H2Bp'
 AND COALESCE(a.credentials->>'auth_mode','') NOT IN ('personalAccessToken','agent_identity')
 AND c.credential_mode='password_totp' AND COALESCE(c.password_ciphertext,'') <> '' AND e.account_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM openai_oauth_reauth_tasks t WHERE t.account_id=a.id
 AND (t.status IN ('queued','running','callback_processing') OR (t.oauth_profile='excel' AND t.updated_at > NOW()-INTERVAL '30 minutes')))
 ORDER BY a.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *openAIOAuthReauthRepository) DeleteExcelCredentials(ctx context.Context, id int64, expected string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM openai_excel_oauth_credentials WHERE account_id=$1 AND credentials_ciphertext=$2`, id, expected)
	return err
}
