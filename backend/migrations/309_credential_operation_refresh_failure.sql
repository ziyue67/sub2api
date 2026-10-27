-- Last observed refresh failure is historical evidence, separate from probes,
-- credentials and the current account status. Old rows have no observation.
ALTER TABLE account_token_guard_v2_accounts
    ADD COLUMN IF NOT EXISTS refresh_failure_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_refresh_failure_at TIMESTAMPTZ;
