-- Forward only. Do not infer ownership of legacy disabled accounts from audit logs.
ALTER TABLE users ADD COLUMN IF NOT EXISTS moderation_status_revision BIGINT NOT NULL DEFAULT 0;
CREATE OR REPLACE FUNCTION content_moderation_status_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.moderation_status_revision := OLD.moderation_status_revision + 1;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS content_moderation_status_revision ON users;
-- UPDATE OF fires even for a repeated explicit administrator disable.
CREATE TRIGGER content_moderation_status_revision BEFORE UPDATE OF status ON users
FOR EACH ROW EXECUTE FUNCTION content_moderation_status_revision();

CREATE TABLE IF NOT EXISTS content_moderation_user_state (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 0,
    generation BIGINT NOT NULL DEFAULT 0,
    auto_ban_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    owned_status_revision BIGINT,
    reason VARCHAR(500) NOT NULL DEFAULT '',
    actor_id BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS content_moderation_user_hits (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generation BIGINT NOT NULL,
    request_id VARCHAR(256) NOT NULL,
    action VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, generation, request_id)
);
CREATE INDEX IF NOT EXISTS idx_content_moderation_user_hits_window
ON content_moderation_user_hits(user_id, generation, created_at);
CREATE TABLE IF NOT EXISTS content_moderation_user_actions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    actor_id BIGINT NOT NULL,
    action VARCHAR(32) NOT NULL,
    reason VARCHAR(500) NOT NULL,
    version BIGINT NOT NULL,
    auto_ban_exempt BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_content_moderation_user_actions_user ON content_moderation_user_actions(user_id, id DESC);

-- Preserve the existing current-count boundary without claiming legacy status ownership.
-- Snapshot old hits once; future current counts are independent of audit retention/deletion.
INSERT INTO content_moderation_user_hits(user_id,generation,request_id,action,created_at)
SELECT l.user_id,0,'legacy:' || l.id::text,l.action,l.created_at
FROM content_moderation_logs l
JOIN users u ON u.id=l.user_id
WHERE l.flagged AND l.action<>'hash_block'
 AND l.mode NOT IN ('cyber_log_only','risk_control_log_only')
 AND l.created_at > COALESCE((SELECT MAX(b.created_at) FROM content_moderation_logs b
 WHERE b.user_id=l.user_id AND b.auto_banned),'-infinity'::timestamptz)
ON CONFLICT(user_id,generation,request_id) DO NOTHING;
