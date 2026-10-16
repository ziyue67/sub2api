ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS cache_creation_billing JSONB;

COMMENT ON COLUMN usage_logs.cache_creation_billing IS
    'Admin-only cache billing audit: original upstream counters, billing policy, conversion and cost delta; not verified upstream charges';
