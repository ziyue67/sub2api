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

func (r *openAIOAuthReauthRepository) ApplyExcelCredentials(ctx context.Context, record *service.OpenAIOAuthReauthTaskRecord, expected map[string]any, ciphertext string, diagnosis ...service.ExcelBPSCredentialState) (bool, error) {
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
	// Clear the preparation marker and persist safe metadata under the account
	// row lock. Administrative status and scheduling flags are never changed.
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=COALESCE(extra,'{}'::jsonb)-'openai_excel_bps_authorization_pending' WHERE id=$1`, id); err != nil {
		return false, err
	}
	if err = writeExcelGrantDiagnosis(ctx, tx, id, diagnosis); err != nil {
		return false, err
	}

	return true, tx.Commit()
}

func (r *openAIOAuthReauthRepository) ReplaceExcelCredentials(ctx context.Context, id int64, expected, replacement string, diagnosis ...service.ExcelBPSCredentialState) (bool, error) {
	return r.mutateExcelGrant(ctx, id, expected, replacement, false, diagnosis)
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

func (r *openAIOAuthReauthRepository) DeleteExcelCredentials(ctx context.Context, id int64, expected string, diagnosis ...service.ExcelBPSCredentialState) error {
	if len(diagnosis) == 0 {
		diagnosis = []service.ExcelBPSCredentialState{service.ExcelBPSGrantFailure("")}
	}
	_, err := r.mutateExcelGrant(ctx, id, expected, "", true, diagnosis)
	return err
}

// Lock accounts before grants, matching ApplyExcelCredentials. A stale response
// cannot change diagnostics or remove a newer ciphertext. Outbox is transactional.
func (r *openAIOAuthReauthRepository) mutateExcelGrant(ctx context.Context, id int64, expected, replacement string, remove bool, diagnosis []service.ExcelBPSCredentialState) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var result sql.Result
	if remove {
		result, err = tx.ExecContext(ctx, `DELETE FROM openai_excel_oauth_credentials WHERE account_id=$1 AND credentials_ciphertext=$2`, id, expected)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE openai_excel_oauth_credentials SET credentials_ciphertext=$3,updated_at=NOW() WHERE account_id=$1 AND credentials_ciphertext=$2`, id, expected, replacement)
	}
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed == 0 {
		return false, nil
	}
	if err = writeExcelGrantDiagnosis(ctx, tx, id, diagnosis); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func writeExcelGrantDiagnosis(ctx context.Context, tx *sql.Tx, id int64, diagnosis []service.ExcelBPSCredentialState) error {
	state := service.ExcelBPSCredentialState{Status: "unknown"}
	if len(diagnosis) > 0 {
		state = diagnosis[0]
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),ARRAY['openai_excel_bps_credential_state'], $2::jsonb,true),updated_at=NOW() WHERE id=$1`, id, string(raw))
	if err != nil {
		return err
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil)
}

func (r *openAIOAuthReauthRepository) GetLatestExcelTask(ctx context.Context, accountID int64) (*service.OpenAIOAuthReauthTaskRecord, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,account_id,status,stage,COALESCE(worker_id,''),COALESCE(auth_session_id,''),
 expected_credentials_hash,COALESCE(error_message,''),attempt,created_at,updated_at,finished_at,oauth_profile
 FROM openai_oauth_reauth_tasks WHERE account_id=$1 AND oauth_profile='excel'
 ORDER BY created_at DESC,id DESC LIMIT 1`, accountID)
	record, err := scanOpenAIOAuthReauthTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return record, err
}

// Bring pre-existing switches into the same automatic preparation state. The
// bounded transaction also invalidates scheduler snapshots before login runs.
func (r *openAIOAuthReauthRepository) PrepareMissingExcelRoutes(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `WITH candidates AS (
 SELECT a.id FROM accounts a WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth'
 AND a.parent_account_id IS NULL AND a.extra->'openai_excel_bps'='true'::jsonb
 AND COALESCE(a.extra->'openai_excel_bps_authorization_pending','false'::jsonb)<>'true'::jsonb
 AND COALESCE(a.credentials->>'client_id','')<>'app_fnr0pYvVwwFDocDumLG3H2Bp'
 AND NOT EXISTS(SELECT 1 FROM openai_excel_oauth_credentials e WHERE e.account_id=a.id AND e.credentials_ciphertext<>'')
 ORDER BY a.id LIMIT 50 FOR UPDATE OF a SKIP LOCKED)
 UPDATE accounts SET extra=extra||'{"openai_excel_bps_authorization_pending":true}'::jsonb,updated_at=NOW()
 WHERE id IN (SELECT id FROM candidates) RETURNING id`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	// A save racing the successful callback may carry an older preparation
	// marker. Reconcile it from the persisted grant without another login.
	readyRows, err := tx.QueryContext(ctx, `UPDATE accounts a SET extra=extra-'openai_excel_bps_authorization_pending',updated_at=NOW()
 WHERE a.id IN (SELECT b.id FROM accounts b WHERE b.deleted_at IS NULL
 AND b.extra->'openai_excel_bps'='true'::jsonb AND b.extra->'openai_excel_bps_authorization_pending'='true'::jsonb
 AND EXISTS(SELECT 1 FROM openai_excel_oauth_credentials e WHERE e.account_id=b.id AND e.credentials_ciphertext<>'')
 ORDER BY b.id LIMIT 50 FOR UPDATE OF b SKIP LOCKED) RETURNING a.id`)
	if err != nil {
		return err
	}
	for readyRows.Next() {
		var id int64
		if err := readyRows.Scan(&id); err != nil {
			_ = readyRows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = readyRows.Err()
	_ = readyRows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}
