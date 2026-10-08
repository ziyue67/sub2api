-- Manual, immutable experiments. No quality-rule/account actions are attached.
CREATE TABLE IF NOT EXISTS controlled_experiments (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft','running','stop_requested','completed','budget_exhausted','cancelled','interrupted')),
    max_calls INTEGER NOT NULL CHECK (max_calls BETWEEN 1 AND 300),
    reserved_calls INTEGER NOT NULL DEFAULT 0 CHECK (reserved_calls >= 0 AND reserved_calls <= max_calls),
    spec JSONB NOT NULL,
    preflight JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    lease_until TIMESTAMPTZ,
    stop_reason VARCHAR(100) NOT NULL DEFAULT ''
);
-- Serialize experiments across replicas to limit interference and upstream load.
CREATE UNIQUE INDEX IF NOT EXISTS controlled_experiments_one_active
    ON controlled_experiments ((true)) WHERE status IN ('running','stop_requested');

CREATE TABLE IF NOT EXISTS controlled_experiment_attempts (
    run_id BIGINT NOT NULL REFERENCES controlled_experiments(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    status VARCHAR(24) NOT NULL CHECK (status IN ('reserved','completed','protocol_failed','unknown','rejected','not_sent')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    data JSONB NOT NULL,
    PRIMARY KEY (run_id, sequence)
);
-- A committed reservation consumes budget even if the process dies before send.
-- Expired runs are interrupted, never queued or replayed automatically.
