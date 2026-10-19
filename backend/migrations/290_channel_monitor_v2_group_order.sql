-- Display order for groups inside one platform. Empty means name, then id.
-- This is not the monitored-group allow-list; group_ids stays a set.
ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS group_order BIGINT[] NOT NULL DEFAULT '{}';
