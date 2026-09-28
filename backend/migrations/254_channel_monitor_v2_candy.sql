ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS candy_probes JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Synthetic group probes are separate from billing/traffic aggregates. Only a
-- bounded answer preview and a normalized reason are retained, never credentials.
CREATE TABLE IF NOT EXISTS channel_monitor_v2_candy_results (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    config_key VARCHAR(64) NOT NULL,
    model VARCHAR(100) NOT NULL,
    reasoning_effort VARCHAR(16) NOT NULL,
    slot TIMESTAMPTZ NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    verdict VARCHAR(16) NOT NULL DEFAULT 'running' CHECK (verdict IN ('running','correct','incorrect','error')),
    latency_ms BIGINT NOT NULL DEFAULT 0,
    answer_preview VARCHAR(512) NOT NULL DEFAULT '',
    reason VARCHAR(32) NOT NULL DEFAULT '',
    UNIQUE (group_id, config_key, slot)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_monitor_v2_candy_running
    ON channel_monitor_v2_candy_results (group_id) WHERE verdict = 'running';
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_candy_history
    ON channel_monitor_v2_candy_results (group_id, checked_at DESC);
