-- Employee ingress policy; existing groups remain unrestricted.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS block_subagents BOOLEAN NOT NULL DEFAULT FALSE;
