-- Existing quota rows receive the migration timestamp, not their original
-- registration time. This timestamp is diagnostic; quotas must not expire.
ALTER TABLE registration_ip_quotas
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
