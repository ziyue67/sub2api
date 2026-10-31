package repository

import "context"

// Observational attribution is deliberately independent of money/ledger writes.
func (r *openAIOAuthReauthRepository) RecordAuthConnectionUsage(ctx context.Context, accountID, apiKeyID int64, id, requestID string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO openai_auth_connection_usage(account_id,api_key_id,connection_id,request_id)
    SELECT $1,$2,id,$4 FROM openai_auth_connections WHERE account_id=$1 AND id=$3
    ON CONFLICT(account_id,api_key_id,request_id) DO NOTHING`, accountID, apiKeyID, id, requestID)
	return err
}
