-- Persist intent before spending a rotating refresh token. Unknown outcomes are
-- never retried with the old token. Candidate credentials remain encrypted and
-- can be recovered after a failed/uncertain final commit.
ALTER TABLE openai_auth_connections
    ADD COLUMN IF NOT EXISTS lease_epoch BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refresh_state TEXT NOT NULL DEFAULT 'idle'
        CHECK(refresh_state IN ('idle','in_progress','uncertain','rejected')),
    ADD COLUMN IF NOT EXISTS refresh_attempt_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refresh_candidate_ciphertext TEXT NOT NULL DEFAULT '';

CREATE OR REPLACE FUNCTION openai_connection_snapshot() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE aid BIGINT; metadata JSONB;
BEGIN
    aid := COALESCE(NEW.account_id, OLD.account_id);
    PERFORM id FROM accounts WHERE id=aid FOR UPDATE;
    SELECT COALESCE(jsonb_agg(jsonb_build_object(
        'id',id,'kind',kind,'status',status,'capabilities',capabilities,
        'preferred',preferred,'cooldown_until',cooldown_until,'refresh_state',refresh_state)
        ORDER BY created_at,id),'[]'::jsonb)
      INTO metadata FROM openai_auth_connections WHERE account_id=aid;
    UPDATE accounts SET extra=(CASE WHEN jsonb_typeof(extra)='object' THEN extra ELSE '{}'::jsonb END) || jsonb_build_object(
        'openai_connections_managed',EXISTS(SELECT 1 FROM openai_auth_connection_policies WHERE account_id=aid AND enabled),
        'openai_auth_connections',metadata), updated_at=NOW() WHERE id=aid;
    INSERT INTO scheduler_outbox(event_type,account_id) VALUES('account_changed',aid);
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS openai_connection_snapshot_trigger ON openai_auth_connections;
CREATE TRIGGER openai_connection_snapshot_trigger AFTER INSERT OR UPDATE OF status,capabilities,preferred,cooldown_until,credentials_ciphertext,refresh_state OR DELETE ON openai_auth_connections
FOR EACH ROW EXECUTE FUNCTION openai_connection_snapshot();
