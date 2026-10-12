-- Channel monitor V2 candy checks: operators choose how many recent results
-- each card shows (30-100, default 100), and one group may run several
-- model/effort probes at once.
-- The old running index was one row per group, so a second model could not be
-- claimed while the first was in flight.

ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS candy_history_limit INTEGER NOT NULL DEFAULT 100
        CHECK (candy_history_limit BETWEEN 30 AND 100);

DROP INDEX IF EXISTS idx_channel_monitor_v2_candy_running;
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_monitor_v2_candy_running
    ON channel_monitor_v2_candy_results (group_id, config_key) WHERE verdict = 'running';

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v2_candy_history_key
    ON channel_monitor_v2_candy_results (group_id, config_key, checked_at DESC);
