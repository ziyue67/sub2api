-- Disposable staging state only. This is not an acknowledgement by a real user.
-- Match the synthetic administrator, never copy this fixture into production.
INSERT INTO settings(key,value,updated_at)
SELECT 'admin_compliance_acknowledgement:' || id,
       jsonb_build_object('version','v2026.06.10','admin_user_id',id,
                          'user_agent','synthetic-integration-fixture',
                          'accepted_at','2000-01-01T00:00:00Z')::text,
       NOW()
FROM users WHERE email='experiment-admin@example.test'
ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at;
