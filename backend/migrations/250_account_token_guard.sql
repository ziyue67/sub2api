-- 账号凭证守护（智能运维 → 凭证守护）
-- 事件表保存每次探活/重登/状态自愈的审计记录；状态表保存每个账号最近一次巡检结果，供页面直接展示。

CREATE TABLE IF NOT EXISTS account_token_guard_events (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    account_name VARCHAR(255) NOT NULL DEFAULT '',
    kind VARCHAR(32) NOT NULL,
    detail VARCHAR(1024) NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_account_token_guard_events_created
    ON account_token_guard_events (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_account_token_guard_events_account
    ON account_token_guard_events (account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS account_token_guard_states (
    account_id BIGINT PRIMARY KEY,
    account_name VARCHAR(255) NOT NULL DEFAULT '',
    account_status VARCHAR(32) NOT NULL DEFAULT '',
    schedulable BOOLEAN NOT NULL DEFAULT FALSE,
    probe_state VARCHAR(32) NOT NULL DEFAULT '',
    probe_detail VARCHAR(1024) NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    fail_streak INTEGER NOT NULL DEFAULT 0,
    last_probe_at TIMESTAMPTZ,
    last_fix_at TIMESTAMPTZ,
    last_fix_action VARCHAR(255) NOT NULL DEFAULT '',
    last_fix_result VARCHAR(1024) NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_account_token_guard_states_updated
    ON account_token_guard_states (updated_at DESC);
