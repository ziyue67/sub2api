-- Redacted benchmark observations only. Routing Cookies and credentials stay in memory.
CREATE TABLE IF NOT EXISTS astra_gateway_history (
 gateway TEXT NOT NULL,
 source_account_id BIGINT NOT NULL,
 target_account_id BIGINT NOT NULL DEFAULT 0,
 passes BIGINT NOT NULL DEFAULT 0,
 failures BIGINT NOT NULL DEFAULT 0,
 first_seen TIMESTAMPTZ NOT NULL,
 last_seen TIMESTAMPTZ NOT NULL,
 last_pass TIMESTAMPTZ,
 last_failure TIMESTAMPTZ,
 last_answer TEXT NOT NULL DEFAULT '',
 last_reason TEXT NOT NULL,
 PRIMARY KEY (gateway, source_account_id, target_account_id)
);
CREATE INDEX IF NOT EXISTS idx_astra_gateway_history_seen ON astra_gateway_history(last_seen DESC);
