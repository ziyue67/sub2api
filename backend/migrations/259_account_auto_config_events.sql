-- Structured automatic-configuration history. Never store credentials or raw requests.
CREATE TABLE IF NOT EXISTS account_auto_config_events (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL DEFAULT 0,
    account_name VARCHAR(255) NOT NULL DEFAULT '',
    platform VARCHAR(32) NOT NULL DEFAULT '',
    kind VARCHAR(32) NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_account_auto_config_events_recent
    ON account_auto_config_events (id DESC);
CREATE INDEX IF NOT EXISTS idx_account_auto_config_events_kind_recent
    ON account_auto_config_events (kind, id DESC);
