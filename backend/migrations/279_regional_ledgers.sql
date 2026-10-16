-- Primary lease authority. Grants are removed from spendable users.balance at
-- issuance. Expiry never refunds them: closure requires regional drain/outbox
-- acknowledgement. Lease IDs and event IDs remain permanent deduplication keys.
CREATE TABLE IF NOT EXISTS regional_quota_leases (
 lease_id VARCHAR(63) PRIMARY KEY,
 region_id VARCHAR(63) NOT NULL,
 user_id BIGINT NOT NULL REFERENCES users(id),
 granted BIGINT NOT NULL CHECK (granted > 0 AND granted % 10 = 0),
 spent BIGINT NOT NULL DEFAULT 0 CHECK (spent >= 0 AND spent % 10 = 0),
 issued_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open','closed')),
 CHECK (spent <= granted), CHECK (expires_at > issued_at)
);
CREATE INDEX IF NOT EXISTS regional_quota_leases_user_idx ON regional_quota_leases(user_id, region_id);
CREATE TABLE IF NOT EXISTS regional_settled_events (
 event_id VARCHAR(63) PRIMARY KEY,
 lease_id VARCHAR(63) NOT NULL REFERENCES regional_quota_leases(lease_id),
 event JSONB NOT NULL,
 settled_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Local data plane tables. Region checks in the ledger prevent reuse across
-- regions. Deploy these in the LOCAL database, never point the request ledger
-- at the authority database. No foreign keys to replicated users/accounts.
CREATE TABLE IF NOT EXISTS regional_local_leases (
 lease_id VARCHAR(63) PRIMARY KEY,
 lease JSONB NOT NULL,
 region_id VARCHAR(63) NOT NULL,
 user_id BIGINT NOT NULL,
 granted BIGINT NOT NULL CHECK (granted > 0 AND granted % 10 = 0),
 spent BIGINT NOT NULL DEFAULT 0 CHECK (spent >= 0 AND spent % 10 = 0),
 reserved BIGINT NOT NULL DEFAULT 0 CHECK (reserved >= 0 AND reserved % 10 = 0),
 state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open','draining','closed')),
 CHECK (spent <= granted AND reserved <= granted - spent)
);
CREATE TABLE IF NOT EXISTS regional_local_requests (
 request_id VARCHAR(63) PRIMARY KEY,
 lease_id VARCHAR(63) NOT NULL REFERENCES regional_local_leases(lease_id),
 request JSONB NOT NULL,
 maximum BIGINT NOT NULL CHECK (maximum > 0 AND maximum % 10 = 0),
 state TEXT NOT NULL CHECK (state IN ('reserved','started','completed','canceled'))
);
CREATE TABLE IF NOT EXISTS regional_usage_outbox (
 event_id VARCHAR(63) PRIMARY KEY REFERENCES regional_local_requests(request_id),
 event JSONB NOT NULL,
 acknowledged BOOLEAN NOT NULL DEFAULT false
);
