CREATE TABLE IF NOT EXISTS openai_auth_connection_affinity (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL,
    session_hash TEXT NOT NULL,
    connection_id UUID NOT NULL REFERENCES openai_auth_connections(id),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(account_id,api_key_id,session_hash)
);
CREATE INDEX IF NOT EXISTS openai_connection_affinity_expiry ON openai_auth_connection_affinity(expires_at);
