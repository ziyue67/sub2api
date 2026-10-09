-- Keep Codex and Excel grants independent; existing tasks remain Codex.
ALTER TABLE openai_oauth_reauth_tasks
    ADD COLUMN IF NOT EXISTS oauth_profile TEXT NOT NULL DEFAULT 'codex'
    CHECK (oauth_profile IN ('codex', 'excel'));

CREATE TABLE IF NOT EXISTS openai_excel_oauth_credentials (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    credentials_ciphertext TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
