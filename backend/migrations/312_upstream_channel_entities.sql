-- Analytics-only explicit ownership. Never infer bindings from URL or historical usage.
CREATE TABLE IF NOT EXISTS upstream_channels (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL UNIQUE CHECK (code ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    remark TEXT NOT NULL DEFAULT '' CHECK (char_length(remark) <= 500),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS upstream_channels_name_unique ON upstream_channels (lower(name));
CREATE TABLE IF NOT EXISTS upstream_channel_bindings (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    channel_id BIGINT REFERENCES upstream_channels(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS upstream_channel_bindings_channel ON upstream_channel_bindings(channel_id,account_id);
COMMENT ON TABLE upstream_channels IS 'Explicit current analytics ownership, unrelated to model-pricing channels and gateway routing';
COMMENT ON TABLE upstream_channel_bindings IS 'Current ownership only; NULL tombstones retain CAS version. Hard account deletion removes only this binding and preserves usage. Bound channels cannot be deleted.';
-- Do not infer ownership from URL values, display names, or historical usage.
