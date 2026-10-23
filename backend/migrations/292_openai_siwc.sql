-- SIWC grants remain independent of Codex and Excel credentials.
ALTER TABLE openai_oauth_reauth_tasks DROP CONSTRAINT IF EXISTS openai_oauth_reauth_tasks_oauth_profile_check;
ALTER TABLE openai_oauth_reauth_tasks ADD CONSTRAINT openai_oauth_reauth_tasks_oauth_profile_check CHECK (oauth_profile IN ('codex','excel','siwc'));
CREATE TABLE openai_siwc_credentials (
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 host_id TEXT NOT NULL,
 credentials_ciphertext TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
