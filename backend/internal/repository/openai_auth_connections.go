package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const authConnectionColumns = `id::text,account_id,kind,label,status,issuer,client_id,subject,identity_verified,workspace,resource,audience,scopes,capabilities,host_id,credentials_ciphertext,revision,preferred,cooldown_until,refresh_state,refresh_attempt_id,refresh_candidate_ciphertext,lease_owner,lease_epoch`

func scanAuthConnection(row interface{ Scan(...any) error }) (*service.OpenAIAuthConnection, error) {
	c := new(service.OpenAIAuthConnection)
	var scopes, capabilities []byte
	err := row.Scan(&c.ID, &c.AccountID, &c.Kind, &c.Label, &c.Status, &c.Issuer, &c.ClientID, &c.Subject, &c.IdentityVerified, &c.Workspace, &c.Resource, &c.Audience, &scopes, &capabilities, &c.HostID, &c.CredentialsCiphertext, &c.Revision, &c.Preferred, &c.CooldownUntil, &c.RefreshState, &c.RefreshAttemptID, &c.RefreshCandidateCiphertext, &c.LeaseOwner, &c.LeaseEpoch)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(scopes, &c.Scopes); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(capabilities, &c.Capabilities); err != nil {
		return nil, err
	}
	return c, nil
}

func (r *openAIOAuthReauthRepository) ListAuthConnections(ctx context.Context, id int64) ([]*service.OpenAIAuthConnection, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+authConnectionColumns+` FROM openai_auth_connections WHERE account_id=$1 ORDER BY created_at,id`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []*service.OpenAIAuthConnection{}
	for rows.Next() {
		c, e := scanAuthConnection(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (r *openAIOAuthReauthRepository) CreateAuthConnection(ctx context.Context, c *service.OpenAIAuthConnection) error {
	scopes, err := json.Marshal(c.Scopes)
	if err != nil {
		return err
	}
	caps, err := json.Marshal(c.Capabilities)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, c.AccountID).Scan(&locked); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO openai_auth_connections(id,account_id,kind,label,status,issuer,client_id,subject,identity_verified,workspace,resource,audience,scopes,capabilities,host_id,credentials_ciphertext)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, c.ID, c.AccountID, c.Kind, c.Label, c.Status, c.Issuer, c.ClientID, c.Subject, c.IdentityVerified, c.Workspace, c.Resource, c.Audience, string(scopes), string(caps), c.HostID, c.CredentialsCiphertext)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *openAIOAuthReauthRepository) CASAuthConnection(ctx context.Context, c *service.OpenAIAuthConnection, revision int64) (bool, error) {
	scopes, err := json.Marshal(c.Scopes)
	if err != nil {
		return false, err
	}
	caps, err := json.Marshal(c.Capabilities)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, c.AccountID).Scan(&locked); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE openai_auth_connections SET status=$4,issuer=$5,client_id=$6,subject=$7,identity_verified=$8,workspace=$9,resource=$10,audience=$11,scopes=$12,capabilities=$13,host_id=$14,credentials_ciphertext=$15,cooldown_until=$16,refresh_state=CASE WHEN $17 THEN 'idle' ELSE refresh_state END,
    refresh_attempt_id=CASE WHEN $17 THEN '' ELSE refresh_attempt_id END,
    refresh_candidate_ciphertext=CASE WHEN $17 THEN '' ELSE refresh_candidate_ciphertext END,revision=revision+1,updated_at=NOW()
    WHERE account_id=$1 AND id=$2 AND revision=$3 AND lease_epoch=$19
    AND (($18='' AND (lease_owner='' OR lease_until<=clock_timestamp())) OR ($18<>'' AND lease_owner=$18 AND lease_until>clock_timestamp()))`, c.AccountID, c.ID, revision, c.Status, c.Issuer, c.ClientID, c.Subject, c.IdentityVerified, c.Workspace, c.Resource, c.Audience, string(scopes), string(caps), c.HostID, c.CredentialsCiphertext, c.CooldownUntil, c.ResetRefresh, c.LeaseOwner, c.LeaseEpoch)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}
	return true, tx.Commit()
}

func (r *openAIOAuthReauthRepository) SelectAuthConnection(ctx context.Context, accountID int64, id string, revision int64) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&locked); err != nil {
		return false, err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT status='active' AND jsonb_array_length(capabilities)>0 FROM openai_auth_connections WHERE account_id=$1 AND id=$2 AND revision=$3 FOR UPDATE`, accountID, id, revision).Scan(&valid)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !valid {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO openai_auth_connection_policies(account_id,enabled) VALUES($1,true) ON CONFLICT(account_id) DO UPDATE SET enabled=true`, accountID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE openai_auth_connections SET preferred=false WHERE account_id=$1 AND preferred AND id<>$2`, accountID, id); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE openai_auth_connections SET preferred=true,updated_at=NOW() WHERE account_id=$1 AND id=$2`, accountID, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *openAIOAuthReauthRepository) AcquireAuthConnectionLease(ctx context.Context, accountID int64, id, owner string) (bool, error) {
	proof, err := r.AcquireAuthConnectionLeaseProof(ctx, accountID, id, owner)
	return proof != nil, err
}
func (r *openAIOAuthReauthRepository) AcquireAuthConnectionLeaseProof(ctx context.Context, accountID int64, id, owner string) (*service.OpenAIAuthConnectionLease, error) {
	proof := &service.OpenAIAuthConnectionLease{Owner: owner}
	err := r.db.QueryRowContext(ctx, `UPDATE openai_auth_connections SET lease_owner=$3,lease_epoch=lease_epoch+1,lease_until=clock_timestamp()+INTERVAL '60 seconds' WHERE account_id=$1 AND id=$2 AND (lease_until IS NULL OR lease_until<clock_timestamp()) RETURNING lease_epoch`, accountID, id, owner).Scan(&proof.Epoch)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return proof, nil
}
func (r *openAIOAuthReauthRepository) ReleaseAuthConnectionLease(ctx context.Context, accountID int64, id, owner string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE openai_auth_connections SET lease_owner='',lease_until=NULL WHERE account_id=$1 AND id=$2 AND lease_owner=$3`, accountID, id, owner)
	return err
}

func (r *openAIOAuthReauthRepository) UseLegacyAuthConnections(ctx context.Context, accountID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM openai_auth_connection_policies WHERE account_id=$1`, accountID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE openai_auth_connections SET preferred=false WHERE account_id=$1`, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *openAIOAuthReauthRepository) AuthConnectionsManaged(ctx context.Context, accountID int64) (bool, error) {
	var managed bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM openai_auth_connection_policies WHERE account_id=$1 AND enabled)`, accountID).Scan(&managed)
	return managed, err
}
