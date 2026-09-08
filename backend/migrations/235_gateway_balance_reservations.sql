-- Durable gateway balance holds.
--
-- A request reserves the user's available wallet before it is sent upstream.
-- The hold lives in users.frozen_balance and is settled/released exactly once
-- by request_id. Expiry is retained for a future cleanup worker;
-- application cancellation also releases the row immediately.

CREATE TABLE IF NOT EXISTS gateway_balance_reservations (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    -- Do not cascade-delete an active hold: the user row must be settled or
    -- released before its API key can be physically removed.
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reserved_amount DECIMAL(20,8) NOT NULL CHECK (reserved_amount >= 0),
    actual_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (actual_amount >= 0),
    status VARCHAR(16) NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved', 'settled', 'released')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT gateway_balance_reservations_request_key UNIQUE (request_id)
);

CREATE INDEX IF NOT EXISTS idx_gateway_balance_reservations_expiry
    ON gateway_balance_reservations (expires_at, id)
    WHERE status = 'reserved';

CREATE INDEX IF NOT EXISTS idx_gateway_balance_reservations_user_time
    ON gateway_balance_reservations (user_id, created_at);
