-- A rotation reserves the existing re-login task's unique active-account slot.
-- Secrets are encrypted by the application; ordinary task/status queries never read them.
CREATE TABLE openai_totp_rotations (
    task_id BIGINT PRIMARY KEY REFERENCES openai_oauth_reauth_tasks(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN
        ('queued','running','enrolling','prepared','activating','verifying','uncertain','succeeded','failed')),
    action TEXT NOT NULL DEFAULT 'rotate' CHECK (action IN ('rotate','verify_new','verify_old')),
    worker_id TEXT NOT NULL DEFAULT '',
    candidate_ciphertext TEXT NOT NULL DEFAULT '',
    previous_ciphertext TEXT NOT NULL,
    password_ciphertext TEXT NOT NULL,
    config_updated_at TIMESTAMPTZ NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_openai_totp_rotations_account ON openai_totp_rotations(account_id, task_id DESC);

-- Serialize configuration edits with rotation creation using the configuration row lock.
CREATE FUNCTION protect_openai_totp_rotation_config() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM openai_totp_rotations
        WHERE account_id = OLD.account_id AND state NOT IN ('succeeded', 'failed')) THEN
        RAISE EXCEPTION 'Resolve the active 2FA rotation before editing login credentials'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_openai_totp_rotation_config
    BEFORE UPDATE ON openai_oauth_reauth_configs
    FOR EACH ROW EXECUTE FUNCTION protect_openai_totp_rotation_config();
