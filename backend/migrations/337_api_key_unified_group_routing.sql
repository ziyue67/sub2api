-- Existing keys retain legacy behavior until explicitly saved with a policy.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS group_routing JSONB;
