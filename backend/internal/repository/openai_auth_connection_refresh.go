package repository

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// All lifecycle commits acquire the account lock before the registration lock,
// just like account selection and the snapshot trigger.
func (r *openAIOAuthReauthRepository) refreshMutation(ctx context.Context, c *service.OpenAIAuthConnection, query string, args ...any) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, c.AccountID).Scan(&id); err != nil {
		return false, err
	}
	params := []any{c.AccountID, c.ID, c.Revision, c.LeaseOwner, c.LeaseEpoch}
	params = append(params, args...)
	result, err := tx.ExecContext(ctx, query, params...)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	return true, tx.Commit()
}

const refreshFence = ` account_id=$1 AND id=$2 AND revision=$3 AND lease_owner=$4 AND lease_epoch=$5 AND lease_owner<>'' AND lease_until>clock_timestamp()`

func (r *openAIOAuthReauthRepository) BeginAuthConnectionRefresh(ctx context.Context, c *service.OpenAIAuthConnection, attempt string) (bool, error) {
	return r.refreshMutation(ctx, c, `UPDATE openai_auth_connections SET refresh_state='in_progress',refresh_attempt_id=$6,refresh_candidate_ciphertext='',updated_at=NOW() WHERE`+refreshFence+` AND status='active' AND refresh_state='idle'`, attempt)
}
func (r *openAIOAuthReauthRepository) MarkAuthConnectionRefresh(ctx context.Context, c *service.OpenAIAuthConnection, state string) (bool, error) {
	if state != "idle" && state != "uncertain" && state != "rejected" {
		return false, sql.ErrNoRows
	}
	return r.refreshMutation(ctx, c, `UPDATE openai_auth_connections SET refresh_state=$6,updated_at=NOW() WHERE`+refreshFence+` AND refresh_attempt_id=$7 AND refresh_state='in_progress' AND refresh_candidate_ciphertext=''`, state, c.RefreshAttemptID)
}
func (r *openAIOAuthReauthRepository) StageAuthConnectionRefresh(ctx context.Context, c *service.OpenAIAuthConnection, candidate string) (bool, error) {
	return r.refreshMutation(ctx, c, `UPDATE openai_auth_connections SET refresh_candidate_ciphertext=$6,updated_at=NOW() WHERE`+refreshFence+` AND status='active' AND refresh_state IN ('in_progress','uncertain') AND refresh_attempt_id=$7`, candidate, c.RefreshAttemptID)
}
func (r *openAIOAuthReauthRepository) FinishAuthConnectionRefresh(ctx context.Context, c *service.OpenAIAuthConnection, ciphertext string, scopes, capabilities string) (bool, error) {
	return r.refreshMutation(ctx, c, `UPDATE openai_auth_connections SET credentials_ciphertext=$6,scopes=$7::jsonb,capabilities=$8::jsonb,refresh_state='idle',refresh_attempt_id='',refresh_candidate_ciphertext='',revision=revision+1,updated_at=NOW() WHERE`+refreshFence+` AND status='active' AND refresh_state IN ('in_progress','uncertain') AND refresh_attempt_id=$9 AND refresh_candidate_ciphertext<>''`, ciphertext, scopes, capabilities, c.RefreshAttemptID)
}
