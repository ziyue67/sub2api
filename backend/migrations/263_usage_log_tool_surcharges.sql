ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS tool_surcharges JSONB;

COMMENT ON COLUMN usage_logs.tool_surcharges IS
    'Completed hosted-tool surcharge snapshots: name, count, USD/1K price, multiplier, actual cost';
