-- Immutable content-addressed pricing. Application roles must not UPDATE or
-- DELETE published prices while requests/outbox/settlements refer to them.
CREATE TABLE IF NOT EXISTS regional_prices (
 version VARCHAR(64) PRIMARY KEY CHECK (version ~ '^[0-9a-f]{64}$'),
 price JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE FUNCTION reject_regional_price_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'regional pricing is immutable; publish a new version';
END;
$$;
CREATE TRIGGER regional_prices_immutable BEFORE UPDATE OR DELETE ON regional_prices
 FOR EACH ROW EXECUTE FUNCTION reject_regional_price_mutation();

-- Main-site in-flight money is deducted from the SAME spendable user row as
-- regional grants. Identity and unresolved holds survive process restart.
CREATE TABLE IF NOT EXISTS regional_primary_requests (
 request_id VARCHAR(63) PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 request JSONB NOT NULL,
 maximum BIGINT NOT NULL CHECK (maximum > 0 AND maximum % 10 = 0),
 state TEXT NOT NULL CHECK (state IN ('reserved','started','completed','canceled')),
 event JSONB
);
ALTER TABLE regional_primary_requests ADD COLUMN balance_funded BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE regional_quota_leases ADD COLUMN api_key_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE regional_quota_leases ADD COLUMN account_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE regional_quota_leases ADD COLUMN billing_mode TEXT NOT NULL DEFAULT '';
CREATE TABLE regional_budget_allocations (
 allocation_id VARCHAR(80) PRIMARY KEY,
 user_id BIGINT NOT NULL,
 api_key_id BIGINT NOT NULL,
 account_id BIGINT NOT NULL,
 subscription_id BIGINT NOT NULL DEFAULT 0,
 platform TEXT NOT NULL,
 maximum BIGINT NOT NULL CHECK(maximum>0 AND maximum%10=0),
 spent BIGINT NOT NULL DEFAULT 0 CHECK(spent>=0 AND spent%10=0),
 state TEXT NOT NULL DEFAULT 'held' CHECK(state IN ('held','closed')),
 CHECK(spent<=maximum)
);
CREATE INDEX regional_budget_key ON regional_budget_allocations(api_key_id) WHERE state='held';
CREATE INDEX regional_budget_account ON regional_budget_allocations(account_id) WHERE state='held';
CREATE INDEX regional_budget_user ON regional_budget_allocations(user_id,platform) WHERE state='held';

CREATE TABLE regional_funds_imports(epoch TEXT PRIMARY KEY, imported_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp());
CREATE TABLE regional_funds_members(epoch TEXT NOT NULL,instance_id TEXT NOT NULL,heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(epoch,instance_id));
