-- Attribution only: existing usage billing and all monetary calculations remain
-- untouched. Legacy rows have no connection attribution.
CREATE TABLE IF NOT EXISTS openai_auth_connection_usage (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL,
    request_id TEXT NOT NULL,
    connection_id UUID NOT NULL REFERENCES openai_auth_connections(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(account_id,api_key_id,request_id)
);
CREATE INDEX IF NOT EXISTS openai_auth_connection_usage_connection ON openai_auth_connection_usage(connection_id,created_at);
