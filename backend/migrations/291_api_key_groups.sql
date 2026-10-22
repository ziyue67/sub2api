-- Additional groups only: api_keys.group_id remains the legacy/default binding.
CREATE TABLE IF NOT EXISTS api_key_groups (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (api_key_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_api_key_groups_group_id ON api_key_groups (group_id, api_key_id);
