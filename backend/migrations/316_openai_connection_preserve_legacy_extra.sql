-- Legacy extra may be JSON null/scalar. Connection metadata protection must not
-- make otherwise valid legacy quota updates fail before their own normalization.
CREATE OR REPLACE FUNCTION preserve_openai_connection_snapshot() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE saved JSONB;
BEGIN
    IF pg_trigger_depth()=1 THEN
        saved := jsonb_strip_nulls(jsonb_build_object(
            'openai_connections_managed',OLD.extra->'openai_connections_managed',
            'openai_auth_connections',OLD.extra->'openai_auth_connections'));
        IF jsonb_typeof(NEW.extra)='object' THEN
            NEW.extra := (NEW.extra - 'openai_connections_managed' - 'openai_auth_connections') || saved;
        ELSIF saved <> '{}'::jsonb THEN
            NEW.extra := saved;
        END IF;
    END IF;
    RETURN NEW;
END $$;
