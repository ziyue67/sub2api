-- Explicit boundary observations. Do not backfill historical queue_wait_ms:
-- legacy passthrough/bridge timer scopes differ from HTTP/ctx_pool.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS queue_timing JSONB;
COMMENT ON COLUMN usage_logs.queue_timing IS 'Measured server/forward timing with source and scope; NULL means unknown; never client E2E';
