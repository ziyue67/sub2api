-- Independent of image_creator's customer quota and seven-day retention.
CREATE TABLE IF NOT EXISTS image_request_archives (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    group_id BIGINT,
    account_id BIGINT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL,
    upstream_model TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL,
    prompt TEXT NOT NULL,
    parameters JSONB NOT NULL DEFAULT '{}',
    files JSONB NOT NULL DEFAULT '[]',
    status TEXT NOT NULL CHECK (status IN ('saved', 'partial', 'failed')),
    error_codes TEXT NOT NULL DEFAULT '',
    byte_size BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS image_request_archives_created_idx ON image_request_archives (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS image_request_archives_user_idx ON image_request_archives (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS image_request_archives_group_idx ON image_request_archives (group_id, created_at DESC);
CREATE INDEX IF NOT EXISTS image_request_archives_account_idx ON image_request_archives (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS image_request_archives_expiry_idx ON image_request_archives (expires_at);
