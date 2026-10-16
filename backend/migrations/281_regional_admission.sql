-- Permanent cancellation identities fence delayed commands across restarts.
CREATE TABLE regional_slot_cancellations (
 region_id TEXT NOT NULL,
 kind TEXT NOT NULL,
 resource_id BIGINT NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 PRIMARY KEY(region_id,kind,resource_id,request_id)
);
CREATE TABLE regional_key_queue (
 sequence BIGSERIAL UNIQUE NOT NULL,
 region_id TEXT NOT NULL,
 key_id BIGINT NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 deadline_ms BIGINT NOT NULL,
 key_limit INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('waiting','acquired','aborted','timeout')),
 PRIMARY KEY(region_id,key_id,request_id)
);
CREATE INDEX regional_key_queue_head ON regional_key_queue(region_id,key_id,sequence) WHERE state='waiting';
CREATE TABLE regional_broker_operations (
 operation_id VARCHAR(128) PRIMARY KEY,
 operation TEXT NOT NULL,
 payload JSONB NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','installed','rejected')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE regional_live_handoffs (
 region_id TEXT NOT NULL,
 lease_id VARCHAR(128) NOT NULL,
 identity JSONB NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('active','aborted','closed')),
 PRIMARY KEY(region_id,lease_id)
);
CREATE TABLE regional_runtime_members (
 epoch VARCHAR(128) NOT NULL,
 instance_id VARCHAR(128) NOT NULL,
 role TEXT NOT NULL,
 heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(epoch,instance_id)
);

CREATE TABLE regional_slot_issue_rejections (
 lease_id VARCHAR(63) PRIMARY KEY,
 payload JSONB NOT NULL
);

CREATE TABLE regional_cutover_evidence (
 epoch VARCHAR(128) PRIMARY KEY,
 inventory_sha256 CHAR(64) NOT NULL,
 payload JSONB NOT NULL,
 verified_at TIMESTAMPTZ NOT NULL
);
