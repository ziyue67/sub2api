-- Credential Guard V2: per-account encrypted login methods and durable probe leases.
ALTER TABLE openai_oauth_reauth_configs
    ADD COLUMN IF NOT EXISTS credential_mode TEXT NOT NULL DEFAULT 'email_otp_url',
    ADD COLUMN IF NOT EXISTS password_ciphertext TEXT,
    ADD COLUMN IF NOT EXISTS totp_secret_ciphertext TEXT;

ALTER TABLE openai_oauth_reauth_configs
    ALTER COLUMN otp_url_ciphertext DROP NOT NULL;

UPDATE openai_oauth_reauth_configs
SET credential_mode = 'email_otp_url'
WHERE credential_mode IS NULL OR BTRIM(credential_mode) = '';

ALTER TABLE openai_oauth_reauth_configs
    DROP CONSTRAINT IF EXISTS openai_oauth_reauth_configs_credential_mode_check;
ALTER TABLE openai_oauth_reauth_configs
    ADD CONSTRAINT openai_oauth_reauth_configs_credential_mode_check CHECK (
        credential_mode IN ('email_otp_url', 'password_totp')
    );

ALTER TABLE openai_oauth_reauth_tasks
    DROP CONSTRAINT IF EXISTS openai_oauth_reauth_tasks_stage_check;
ALTER TABLE openai_oauth_reauth_tasks
    ADD CONSTRAINT openai_oauth_reauth_tasks_stage_check CHECK (
        stage IN (
            'queued', 'starting', 'protocol_connecting', 'email_submitted',
            'waiting_otp', 'otp_submitted', 'password_submitted', 'mfa_submitted',
            'waiting_callback', 'exchanging_token', 'applying_credentials',
            'succeeded', 'failed'
        )
    );

CREATE TABLE IF NOT EXISTS account_token_guard_v2_accounts (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    auto_relogin_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    probe_state TEXT NOT NULL DEFAULT 'pending',
    probe_detail TEXT NOT NULL DEFAULT '',
    fail_streak INTEGER NOT NULL DEFAULT 0,
    last_probe_at TIMESTAMPTZ,
    last_reauth_at TIMESTAMPTZ,
    next_probe_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cooldown_until TIMESTAMPTZ,
    blocked_reason TEXT NOT NULL DEFAULT '',
    lease_owner TEXT,
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_token_guard_v2_probe_state_check CHECK (
        probe_state IN ('pending', 'ok', 'auth', 'transient')
    ),
    CONSTRAINT account_token_guard_v2_fail_streak_check CHECK (fail_streak >= 0)
);

CREATE INDEX IF NOT EXISTS idx_account_token_guard_v2_due
    ON account_token_guard_v2_accounts (next_probe_at, account_id)
    WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_account_token_guard_v2_lease
    ON account_token_guard_v2_accounts (lease_until)
    WHERE lease_until IS NOT NULL;
