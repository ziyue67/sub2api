ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS request_kind TEXT NOT NULL DEFAULT 'normal';

ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_request_kind_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_request_kind_check
    CHECK (request_kind IN ('normal', 'compact')) NOT VALID;
