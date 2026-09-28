-- Optional managed proxy used only by OpenAI OAuth re-login.
ALTER TABLE openai_oauth_reauth_configs
    ADD COLUMN IF NOT EXISTS proxy_id BIGINT REFERENCES proxies(id) ON DELETE SET NULL;
