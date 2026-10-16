-- Durable publisher revision floor and encrypted packet. Do not delete/reset a
-- stream while receivers retain its revision; recovery must preserve this row.
CREATE TABLE IF NOT EXISTS regional_scheduler_streams (
    region_id VARCHAR(63) PRIMARY KEY,
    revision BIGINT NOT NULL CHECK (revision >= 0),
    packet BYTEA,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((revision = 0 AND packet IS NULL) OR (revision > 0 AND packet IS NOT NULL))
);
