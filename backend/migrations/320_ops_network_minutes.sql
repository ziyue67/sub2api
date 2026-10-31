-- Process-local deployment network namespace. No address/interface identifiers.
CREATE TABLE IF NOT EXISTS ops_network_minutes (
	 id BIGSERIAL PRIMARY KEY,
  instance_id UUID NOT NULL,
  sampled_at TIMESTAMPTZ NOT NULL,
  egress_mbps DOUBLE PRECISION,
  tcp_retrans_percent DOUBLE PRECISION,
  qdisc_drops BIGINT,
  egress_status TEXT NOT NULL,
  tcp_status TEXT NOT NULL,
  qdisc_status TEXT NOT NULL,
  window_seconds DOUBLE PRECISION NOT NULL,
  UNIQUE (instance_id, sampled_at)
);
CREATE INDEX IF NOT EXISTS ops_network_minutes_time_idx ON ops_network_minutes(sampled_at DESC);
