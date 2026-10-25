-- Only newly observed, explicitly staged events are recovered. This migration
-- does not infer or charge historical usage, or change any monetary row.
CREATE TABLE usage_billing_events (
    request_id VARCHAR(255) NOT NULL,
    api_key_id BIGINT NOT NULL,
    request_fingerprint VARCHAR(64) NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending','applied','complete','legacy','blocked')),
    result JSONB,
    attempts INTEGER NOT NULL DEFAULT 0,
    failure_code TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    observed_at TIMESTAMPTZ NOT NULL,
    staged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    applied_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (request_id, api_key_id)
);
CREATE INDEX usage_billing_events_pending ON usage_billing_events(next_attempt_at,staged_at)
    WHERE state IN ('pending','applied');

CREATE FUNCTION reject_usage_billing_event_identity_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.request_id IS DISTINCT FROM NEW.request_id
       OR OLD.api_key_id IS DISTINCT FROM NEW.api_key_id
       OR OLD.request_fingerprint IS DISTINCT FROM NEW.request_fingerprint
       OR OLD.schema_version IS DISTINCT FROM NEW.schema_version
       OR OLD.payload IS DISTINCT FROM NEW.payload
       OR OLD.observed_at IS DISTINCT FROM NEW.observed_at THEN
        RAISE EXCEPTION 'usage billing event identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER usage_billing_event_identity_immutable BEFORE UPDATE ON usage_billing_events
    FOR EACH ROW EXECUTE FUNCTION reject_usage_billing_event_identity_mutation();
