-- Observation records no account actions and can coexist with both independent
-- BPS lifecycle and group/scheduling policies. Reserve one rule per scope.
DROP INDEX IF EXISTS scheduled_test_quality_account_scope_unique;
CREATE UNIQUE INDEX scheduled_test_quality_account_scope_unique
 ON scheduled_test_plans (account_id,
   (CASE pelican_config->'quality'->>'action'
      WHEN 'enable_bps' THEN 'bps'
      WHEN 'observe_only' THEN 'observation'
      ELSE 'quarantine' END))
 WHERE pelican_config->'quality' IS NOT NULL;
