-- Card candy history can be set from 15 checks, not only 30. Existing rows
-- stay valid: the previous check already required 30–100.

DO $$
DECLARE
    constraint_name text;
BEGIN
    FOR constraint_name IN
        SELECT con.conname
        FROM pg_constraint con
        JOIN pg_class rel ON rel.oid = con.conrelid
        WHERE rel.relname = 'channel_monitor_v2_config'
          AND con.contype = 'c'
          AND pg_get_constraintdef(con.oid) ILIKE '%candy_history_limit%'
    LOOP
        EXECUTE format('ALTER TABLE channel_monitor_v2_config DROP CONSTRAINT %I', constraint_name);
    END LOOP;
END $$;

ALTER TABLE channel_monitor_v2_config
    ADD CONSTRAINT channel_monitor_v2_config_candy_history_limit_check
    CHECK (candy_history_limit BETWEEN 15 AND 100);
