-- Channel monitor V3 idle probes: for groups an admin picks, a slot with too
-- little real traffic gets one short active request near its end, so quiet
-- groups do not turn grey. Off by default; slots with enough real requests are
-- still judged from the passive V2 facts alone.

ALTER TABLE channel_monitor_v3_config
    ADD COLUMN IF NOT EXISTS idle_probe JSONB NOT NULL DEFAULT '{"enabled": false, "groups": []}'::jsonb;

CREATE TABLE IF NOT EXISTS channel_monitor_v3_probe_results (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model VARCHAR(100) NOT NULL,
    -- The slot the probe was taken for. The unique key is the claim that keeps
    -- several instances from probing the same slot twice.
    slot_start TIMESTAMPTZ NOT NULL,
    slot_minutes INTEGER NOT NULL CHECK (slot_minutes BETWEEN 1 AND 60),
    outcome VARCHAR(16) NOT NULL DEFAULT 'running' CHECK (outcome IN ('running', 'success', 'failure')),
    latency_ms INTEGER,
    -- V2 error category of a failure; empty on success.
    error_category VARCHAR(32) NOT NULL DEFAULT '',
    error_message VARCHAR(500) NOT NULL DEFAULT '',
    -- The account that answered, kept without a foreign key so history survives deletion.
    account_id BIGINT,
    -- Upstream cost from reported usage; NULL when it could not be priced.
    cost_usd NUMERIC(20, 10),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    CONSTRAINT uq_channel_monitor_v3_probe_slot UNIQUE (group_id, model, slot_start)
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_probe_results_slot
    ON channel_monitor_v3_probe_results (slot_start);

COMMENT ON TABLE channel_monitor_v3_probe_results IS
    'V3 idle probes; kept for the availability range plus one day, never billed to users.';
