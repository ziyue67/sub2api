-- Additive, durable budget. Do not count already-terminal pre-upgrade history.
ALTER TABLE account_token_guard_v2_accounts
    ADD COLUMN relogin_fail_streak INTEGER NOT NULL DEFAULT 0 CHECK (relogin_fail_streak >= 0),
    ADD COLUMN last_counted_task_id BIGINT NOT NULL DEFAULT 0 CHECK (last_counted_task_id >= 0),
    ADD COLUMN auto_relogin_exhausted BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX idx_openai_oauth_reauth_codex_budget
    ON openai_oauth_reauth_tasks (account_id, id) WHERE oauth_profile = 'codex';

UPDATE account_token_guard_v2_accounts AS guard
SET last_counted_task_id = COALESCE((
    SELECT MAX(task.id) FROM openai_oauth_reauth_tasks AS task
    WHERE task.account_id = guard.account_id AND task.oauth_profile = 'codex'
      AND task.status IN ('failed', 'succeeded')
      AND NOT EXISTS (SELECT 1 FROM openai_totp_rotations AS rotation WHERE rotation.task_id = task.id)
), 0);
