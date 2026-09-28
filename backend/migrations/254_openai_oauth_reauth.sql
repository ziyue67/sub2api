-- OpenAI OAuth re-authentication configuration and durable task state.
-- The OTP endpoint is encrypted by the application before it reaches this table.
CREATE TABLE IF NOT EXISTS openai_oauth_reauth_configs (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    login_email TEXT NOT NULL,
    otp_url_ciphertext TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS openai_oauth_reauth_tasks (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued',
    stage TEXT NOT NULL DEFAULT 'queued',
    worker_id TEXT,
    auth_session_id TEXT,
    expected_credentials_hash TEXT NOT NULL,
    error_message TEXT,
    attempt INTEGER NOT NULL DEFAULT 0,
    claimed_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT openai_oauth_reauth_tasks_status_check CHECK (
        status IN ('queued', 'running', 'callback_processing', 'succeeded', 'failed')
    ),
    CONSTRAINT openai_oauth_reauth_tasks_stage_check CHECK (
        stage IN (
            'queued', 'starting', 'protocol_connecting', 'email_submitted',
            'waiting_otp', 'otp_submitted', 'waiting_callback',
            'exchanging_token', 'applying_credentials', 'succeeded', 'failed'
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_openai_oauth_reauth_tasks_account_created
    ON openai_oauth_reauth_tasks (account_id, created_at DESC, id DESC);

CREATE UNIQUE INDEX IF NOT EXISTS uq_openai_oauth_reauth_tasks_active_account
    ON openai_oauth_reauth_tasks (account_id)
    WHERE status IN ('queued', 'running', 'callback_processing');

CREATE INDEX IF NOT EXISTS idx_openai_oauth_reauth_tasks_claimable
    ON openai_oauth_reauth_tasks (status, created_at, id);
