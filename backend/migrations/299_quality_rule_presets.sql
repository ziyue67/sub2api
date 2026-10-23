-- Reusable configuration only. Kept separate from quality_rule_templates,
-- whose enabled rows continuously distribute plans to matching accounts.
CREATE TABLE IF NOT EXISTS quality_rule_presets (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    model_id VARCHAR(100) NOT NULL,
    cron_expression TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    max_results INTEGER NOT NULL,
    pelican_config JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
