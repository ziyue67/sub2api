-- A true physical account deletion cascades only its analytics binding. Advance
-- the former entity revision so stale multi-account impact confirmations fail.
-- Ordinary account deletion is soft and leaves the binding intact.
CREATE OR REPLACE FUNCTION upstream_channel_binding_delete_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.channel_id IS NOT NULL THEN
        UPDATE upstream_channels SET revision = revision + 1, updated_at = CURRENT_TIMESTAMP
        WHERE id = OLD.channel_id;
    END IF;
    RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS upstream_channel_binding_delete_revision ON upstream_channel_bindings;
CREATE TRIGGER upstream_channel_binding_delete_revision AFTER DELETE ON upstream_channel_bindings
FOR EACH ROW EXECUTE FUNCTION upstream_channel_binding_delete_revision();
