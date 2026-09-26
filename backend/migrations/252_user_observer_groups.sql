ALTER TABLE users ADD COLUMN IF NOT EXISTS observer_group_ids JSONB NOT NULL DEFAULT '[]'::jsonb;
