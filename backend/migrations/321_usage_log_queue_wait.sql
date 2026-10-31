-- Pre-forward queue wait: request arrival at this gateway -> start of the
-- upstream forward attempt (auth, body read, routing, user/account admission).
-- NULL means historical rows or a path without an ingress clock.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS queue_wait_ms INTEGER;
