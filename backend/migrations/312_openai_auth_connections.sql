-- Connection credentials use the existing durable credential encryptor in the
-- service. No plaintext token is copied by SQL; legacy slots remain authoritative
-- until explicitly moved to a managed connection.
CREATE TABLE IF NOT EXISTS openai_auth_connections (
    id UUID PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('codex','bps','siwc','api_key')),
    label TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','disabled','unlinked')),
    issuer TEXT NOT NULL DEFAULT '',
    client_id TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    identity_verified BOOLEAN NOT NULL DEFAULT FALSE,
    workspace TEXT NOT NULL DEFAULT '',
    resource TEXT NOT NULL DEFAULT '',
    audience TEXT NOT NULL DEFAULT '',
    scopes JSONB NOT NULL DEFAULT '[]',
    capabilities JSONB NOT NULL DEFAULT '[]',
    host_id TEXT NOT NULL DEFAULT '',
    credentials_ciphertext TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    preferred BOOLEAN NOT NULL DEFAULT FALSE,
    cooldown_until TIMESTAMPTZ,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (jsonb_typeof(scopes) = 'array' AND jsonb_typeof(capabilities) = 'array')
);
CREATE INDEX IF NOT EXISTS openai_auth_connections_account ON openai_auth_connections(account_id);
CREATE UNIQUE INDEX IF NOT EXISTS openai_auth_connections_preferred ON openai_auth_connections(account_id) WHERE preferred;
