ALTER TABLE openai_oauth_reauth_configs
    ADD COLUMN IF NOT EXISTS engine TEXT NOT NULL DEFAULT 'local_worker';

UPDATE openai_oauth_reauth_configs
SET engine = 'local_worker'
WHERE engine IS NULL OR BTRIM(engine) = '';

ALTER TABLE openai_oauth_reauth_configs
    DROP CONSTRAINT IF EXISTS openai_oauth_reauth_configs_engine_check;
ALTER TABLE openai_oauth_reauth_configs
    ADD CONSTRAINT openai_oauth_reauth_configs_engine_check CHECK (engine IN ('local_worker', 'session_studio'));
