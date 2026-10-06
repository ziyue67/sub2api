CREATE TABLE IF NOT EXISTS astra_scheduling_states (
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 state JSONB NOT NULL DEFAULT '{}'
);
