-- Card bars follow this operator choice. windows keeps 18 equal slices of the
-- selected range. samples shows the latest 18 buckets that meet the minimum
-- sample, ignoring that range. Existing installs stay on windows.
ALTER TABLE channel_monitor_v2_config
    ADD COLUMN IF NOT EXISTS card_timeline_mode TEXT NOT NULL DEFAULT 'windows';

ALTER TABLE channel_monitor_v2_config
    DROP CONSTRAINT IF EXISTS channel_monitor_v2_config_card_timeline_mode_check;

ALTER TABLE channel_monitor_v2_config
    ADD CONSTRAINT channel_monitor_v2_config_card_timeline_mode_check
    CHECK (card_timeline_mode IN ('windows', 'samples'));
