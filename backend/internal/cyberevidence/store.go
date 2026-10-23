package cyberevidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const ConfigKey = "cyber_policy_evidence_config"

type SQLStore struct{ db *sql.DB }

func NewSQLStore(db *sql.DB) *SQLStore { return &SQLStore{db: db} }
func (s *SQLStore) LoadConfig(ctx context.Context) (Config, error) {
	c := DefaultConfig()
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, ConfigKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}
func (s *SQLStore) SaveConfig(ctx context.Context, c Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, ConfigKey, string(raw))
	return err
}
func (s *SQLStore) Status(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE content_moderation_logs SET evidence_status=$2 WHERE id=$1 AND action='cyber_policy' AND evidence_status='pending'`, id, status)
	return err
}
func (s *SQLStore) Save(ctx context.Context, id int64, p Prepared, days int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT evidence_status FROM content_moderation_logs WHERE id=$1 AND action='cyber_policy' FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "deleted" || status == "expired" {
		return ErrNotFound
	}
	// ON CONFLICT makes retry after an ambiguous commit idempotent. Only encrypted
	// data and non-content metadata enter this table.
	_, err = tx.ExecContext(ctx, `INSERT INTO cyber_policy_evidence(log_id,user_id,ciphertext,redaction_version,truncated,original_bytes,created_at,expires_at)
 SELECT l.id,l.user_id,$2,$3,$4,$5,l.created_at,l.created_at+($6 * INTERVAL '1 day') FROM content_moderation_logs l
 LEFT JOIN users u ON u.id=l.user_id
 WHERE l.id=$1 AND l.action='cyber_policy' AND (l.user_id IS NULL OR u.deleted_at IS NULL)
 ON CONFLICT(log_id) DO NOTHING`, id, p.Ciphertext, p.RedactionVersion, p.Truncated, p.OriginalBytes, days)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE content_moderation_logs SET evidence_status='saved' WHERE id=$1 AND EXISTS(SELECT 1 FROM cyber_policy_evidence WHERE log_id=$1)`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
func (s *SQLStore) Get(ctx context.Context, id int64) (Record, error) {
	var r Record
	var ciphertext sql.NullString
	var expires sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT l.id,l.user_id,l.request_id,l.endpoint,l.model,l.created_at,
 CASE WHEN e.expires_at<=NOW() OR (e.created_at IS NOT NULL AND e.created_at<=NOW()-($2 * INTERVAL '1 day')) THEN 'expired'
 WHEN u.deleted_at IS NOT NULL OR (l.evidence_status='saved' AND e.log_id IS NULL) THEN 'unavailable'
 WHEN l.evidence_status='' THEN 'historical_unavailable' ELSE l.evidence_status END,
 CASE WHEN e.expires_at>NOW() AND e.created_at>NOW()-($2 * INTERVAL '1 day') AND u.deleted_at IS NULL THEN e.ciphertext END,e.expires_at
 FROM content_moderation_logs l LEFT JOIN cyber_policy_evidence e ON e.log_id=l.id LEFT JOIN users u ON u.id=l.user_id
 WHERE l.id=$1 AND l.action='cyber_policy'`, id, s.retention(ctx)).Scan(&r.LogID, &r.UserID, &r.RequestID, &r.Endpoint, &r.Model, &r.OccurredAt, &r.Status, &ciphertext, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, ErrUnavailable
	}
	if expires.Valid {
		r.ExpiresAt = &expires.Time
	}
	r.Ciphertext = ciphertext.String
	return r, nil
}
func (s *SQLStore) retention(ctx context.Context) int {
	c, err := s.LoadConfig(ctx)
	if err != nil || !c.Valid() {
		return 1
	}
	return c.RetentionDays
}
func (s *SQLStore) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE content_moderation_logs SET evidence_status='deleted' WHERE id=$1 AND action='cyber_policy'`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM cyber_policy_evidence WHERE log_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLStore) Cleanup(ctx context.Context, days int) (int64, error) {
	result, err := s.db.ExecContext(ctx, `WITH removed AS (
 DELETE FROM cyber_policy_evidence e WHERE e.expires_at<=NOW() OR e.created_at<=NOW()-($1 * INTERVAL '1 day')
 OR EXISTS(SELECT 1 FROM users u WHERE u.id=e.user_id AND u.deleted_at IS NOT NULL) RETURNING log_id)
 UPDATE content_moderation_logs SET evidence_status='expired' WHERE id IN (SELECT log_id FROM removed)`, days)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	_, err = s.db.ExecContext(ctx, `UPDATE content_moderation_logs SET evidence_status='interrupted' WHERE evidence_status='pending' AND created_at<NOW()-INTERVAL '10 minutes'`)
	return n, err
}

var _ Store = (*SQLStore)(nil)
