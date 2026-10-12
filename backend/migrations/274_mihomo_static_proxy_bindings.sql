ALTER TABLE proxies ADD COLUMN IF NOT EXISTS mihomo_node_id varchar(128);

CREATE UNIQUE INDEX IF NOT EXISTS idx_proxies_mihomo_node_id_active
    ON proxies (mihomo_node_id)
    WHERE mihomo_node_id IS NOT NULL AND deleted_at IS NULL;
