-- Extend the existing durable queue; secrets remain in encrypted settings only.
ALTER TABLE account_ops_alerts DROP CONSTRAINT account_ops_alerts_kind_check;
ALTER TABLE account_ops_alerts ADD CONSTRAINT account_ops_alerts_kind_check CHECK(kind IN ('balance_low','weekly_quota','balance_threshold','quota_threshold'));
ALTER TABLE account_ops_alerts DROP CONSTRAINT account_ops_alerts_state_check;
ALTER TABLE account_ops_alerts ADD CONSTRAINT account_ops_alerts_state_check CHECK(state IN ('pending','sending','sent','failed','suppressed','resolved'));
ALTER TABLE account_ops_alerts ADD COLUMN details JSONB NOT NULL DEFAULT 'null'::jsonb;
ALTER TABLE account_ops_alerts ADD COLUMN deliveries JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE account_ops_alerts ADD COLUMN identity TEXT NOT NULL DEFAULT '';
