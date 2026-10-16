-- Shared upstream pool: reserve regional capacity without moving accounts.
-- Primary requests and regional grants must use this same budget authority;
-- legacy Redis-only acquisition cannot run while grants are active.
CREATE TABLE IF NOT EXISTS regional_slot_budgets (
 kind TEXT NOT NULL CHECK (kind IN ('account','user','api_key')),
 resource_id BIGINT NOT NULL CHECK (resource_id > 0),
 primary_used BIGINT NOT NULL DEFAULT 0 CHECK (primary_used >= 0),
 granted BIGINT NOT NULL DEFAULT 0 CHECK (granted >= 0),
 PRIMARY KEY(kind,resource_id)
);
CREATE TABLE IF NOT EXISTS regional_slot_grants (
 lease_id VARCHAR(63) PRIMARY KEY,
 kind TEXT NOT NULL,
 resource_id BIGINT NOT NULL,
 region_id VARCHAR(63) NOT NULL,
 slots INTEGER NOT NULL CHECK (slots > 0),
 revision BIGINT NOT NULL CHECK (revision > 0),
 issued_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > issued_at),
 state TEXT NOT NULL CHECK (state IN ('open','draining','closed')),
 FOREIGN KEY(kind,resource_id) REFERENCES regional_slot_budgets(kind,resource_id)
);
CREATE TABLE IF NOT EXISTS regional_primary_slots (
 kind TEXT NOT NULL,
 resource_id BIGINT NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 released BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(kind,resource_id,request_id),
 FOREIGN KEY(kind,resource_id) REFERENCES regional_slot_budgets(kind,resource_id)
);
CREATE TABLE IF NOT EXISTS regional_local_slot_grants (
 lease_id VARCHAR(63) PRIMARY KEY,
 grant_payload JSONB NOT NULL,
 region_id VARCHAR(63) NOT NULL,
 kind TEXT NOT NULL,
 resource_id BIGINT NOT NULL CHECK (resource_id > 0),
 slots INTEGER NOT NULL CHECK (slots > 0),
 used INTEGER NOT NULL DEFAULT 0 CHECK (used >= 0),
 state TEXT NOT NULL CHECK (state IN ('open','draining','closed')),
 CHECK (used <= slots)
);
CREATE TABLE IF NOT EXISTS regional_local_slots (
 lease_id VARCHAR(63) NOT NULL REFERENCES regional_local_slot_grants(lease_id),
 request_id VARCHAR(128) NOT NULL,
 kind TEXT NOT NULL,
 resource_id BIGINT NOT NULL,
 released BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(lease_id,request_id),
 UNIQUE(kind,resource_id,request_id)
);

CREATE INDEX IF NOT EXISTS regional_local_slot_grants_resource_idx ON regional_local_slot_grants(region_id,kind,resource_id,state);
