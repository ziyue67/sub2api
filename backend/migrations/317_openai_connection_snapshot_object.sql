-- Activating connections on a legacy scalar/null extra must publish an object
-- snapshot. Existing credential/account fields remain untouched.
CREATE OR REPLACE FUNCTION openai_connection_snapshot() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE aid BIGINT; metadata JSONB;
BEGIN
    aid := COALESCE(NEW.account_id, OLD.account_id);
    PERFORM id FROM accounts WHERE id=aid FOR UPDATE;
    SELECT COALESCE(jsonb_agg(jsonb_build_object(
        'id',id,'kind',kind,'status',status,'capabilities',capabilities,
        'preferred',preferred,'cooldown_until',cooldown_until) ORDER BY created_at,id),'[]'::jsonb)
      INTO metadata FROM openai_auth_connections WHERE account_id=aid;
    UPDATE accounts SET extra=(CASE WHEN jsonb_typeof(extra)='object' THEN extra ELSE '{}'::jsonb END) || jsonb_build_object(
        'openai_connections_managed',EXISTS(SELECT 1 FROM openai_auth_connection_policies WHERE account_id=aid AND enabled),
        'openai_auth_connections',metadata), updated_at=NOW() WHERE id=aid;
    INSERT INTO scheduler_outbox(event_type,account_id) VALUES('account_changed',aid);
    RETURN NULL;
END $$;
