-- Durable metadata for one-shot ChatGPT Web Session exports.
-- Session JSON is intentionally kept in process memory only.
CREATE TABLE IF NOT EXISTS openai_session_export_tasks (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued',
    stage TEXT NOT NULL DEFAULT 'queued',
    worker_id TEXT NOT NULL DEFAULT '',
    expected_credentials_hash TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    claimed_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT openai_session_export_tasks_status_check CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed', 'expired')
    ),
    CONSTRAINT openai_session_export_tasks_stage_check CHECK (
        stage IN (
            'queued', 'starting', 'protocol_connecting', 'password_submitted',
            'mfa_submitted', 'session_request', 'identity_verification',
            'succeeded', 'failed'
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_openai_session_export_tasks_account_created
    ON openai_session_export_tasks (account_id, created_at DESC, id DESC);

CREATE UNIQUE INDEX IF NOT EXISTS uq_openai_session_export_tasks_active_account
    ON openai_session_export_tasks (account_id)
    WHERE status IN ('queued', 'running');

CREATE INDEX IF NOT EXISTS idx_openai_session_export_tasks_claimable
    ON openai_session_export_tasks (status, created_at, id);
