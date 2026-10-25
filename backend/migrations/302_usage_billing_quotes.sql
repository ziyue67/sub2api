-- New charges only: no historical quote backfill or monetary adjustment.
CREATE TABLE usage_billing_quotes (
    request_id VARCHAR(255) NOT NULL,
    api_key_id BIGINT NOT NULL,
    request_fingerprint VARCHAR(64) NOT NULL,
    quoted_actual_cost NUMERIC(20,10) NOT NULL,
    canonical_actual_cost NUMERIC(20,8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (request_id, api_key_id)
);
