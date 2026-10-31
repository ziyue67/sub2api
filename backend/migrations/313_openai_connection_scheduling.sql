-- Safe connection metadata participates in the existing scheduler snapshot and
-- outbox. These keys are server-owned and cannot be replaced by old API clients.
CREATE TABLE IF NOT EXISTS openai_auth_connection_policies (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE OR REPLACE FUNCTION openai_connection_snapshot() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE aid BIGINT; metadata JSONB;
BEGIN
    aid := COALESCE(NEW.account_id, OLD.account_id);
    PERFORM id FROM accounts WHERE id=aid FOR UPDATE;
    SELECT COALESCE(jsonb_agg(jsonb_build_object(
        'id',id,'kind',kind,'status',status,'capabilities',capabilities,
        'preferred',preferred,'cooldown_until',cooldown_until) ORDER BY created_at,id),'[]'::jsonb)
      INTO metadata FROM openai_auth_connections WHERE account_id=aid;
    UPDATE accounts SET extra=COALESCE(extra,'{}'::jsonb) || jsonb_build_object(
        'openai_connections_managed',EXISTS(SELECT 1 FROM openai_auth_connection_policies WHERE account_id=aid AND enabled),
        'openai_auth_connections',metadata), updated_at=NOW() WHERE id=aid;
    INSERT INTO scheduler_outbox(event_type,account_id) VALUES('account_changed',aid);
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS openai_connection_snapshot_trigger ON openai_auth_connections;
CREATE TRIGGER openai_connection_snapshot_trigger AFTER INSERT OR UPDATE OF status,capabilities,preferred,cooldown_until,credentials_ciphertext OR DELETE ON openai_auth_connections
FOR EACH ROW EXECUTE FUNCTION openai_connection_snapshot();
CREATE OR REPLACE FUNCTION preserve_openai_connection_snapshot() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF pg_trigger_depth()=1 THEN
        NEW.extra := (COALESCE(NEW.extra,'{}'::jsonb) - 'openai_connections_managed' - 'openai_auth_connections') ||
          jsonb_strip_nulls(jsonb_build_object('openai_connections_managed',OLD.extra->'openai_connections_managed',
          'openai_auth_connections',OLD.extra->'openai_auth_connections'));
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS preserve_openai_connection_snapshot_trigger ON accounts;
CREATE TRIGGER preserve_openai_connection_snapshot_trigger BEFORE UPDATE OF extra ON accounts
FOR EACH ROW EXECUTE FUNCTION preserve_openai_connection_snapshot();
