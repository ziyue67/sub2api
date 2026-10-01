-- Cost snapshots must survive the seven-day / 100-result history retention.
-- Legacy results have no usage data: NULL means unknown, never free.
ALTER TABLE pelican_group_test_results
    ADD COLUMN IF NOT EXISTS cost_usd NUMERIC(20, 10),
    ADD COLUMN IF NOT EXISTS cost_incomplete BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS pelican_group_test_daily_costs (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    cost_date DATE NOT NULL,
    cost_usd NUMERIC(20, 10) NOT NULL DEFAULT 0,
    unpriced_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, cost_date)
);

COMMENT ON COLUMN pelican_group_test_results.cost_usd IS
    'USD upstream cost snapshot from reported token usage and account cost multiplier; NULL if unavailable';
COMMENT ON TABLE pelican_group_test_daily_costs IS
    'Group-level lifetime costs by server-local completion date, independent of result and plan retention';
