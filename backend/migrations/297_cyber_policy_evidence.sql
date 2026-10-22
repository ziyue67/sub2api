-- Confirmed upstream cyber events only. No plaintext input is stored here.
ALTER TABLE content_moderation_logs ADD COLUMN IF NOT EXISTS evidence_status TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS cyber_policy_evidence (
 log_id BIGINT PRIMARY KEY REFERENCES content_moderation_logs(id) ON DELETE CASCADE,
 user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
 ciphertext TEXT NOT NULL CHECK (octet_length(ciphertext) BETWEEN 1 AND 524288),
 redaction_version TEXT NOT NULL,
 truncated BOOLEAN NOT NULL,
 original_bytes BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_evidence_expiry ON cyber_policy_evidence(expires_at);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_evidence_user ON cyber_policy_evidence(user_id);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_evidence_pending ON content_moderation_logs(created_at) WHERE evidence_status='pending';
