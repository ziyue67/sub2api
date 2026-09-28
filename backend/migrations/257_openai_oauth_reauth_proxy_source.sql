-- Distinguish account routing, a selected managed proxy, and the managed Mihomo pool.
ALTER TABLE openai_oauth_reauth_configs
    ADD COLUMN IF NOT EXISTS proxy_source TEXT;

UPDATE openai_oauth_reauth_configs
SET proxy_source = CASE
    WHEN proxy_id IS NOT NULL THEN 'managed_proxy'
    ELSE 'account'
END
WHERE proxy_source IS NULL OR BTRIM(proxy_source) = '';

ALTER TABLE openai_oauth_reauth_configs
    ALTER COLUMN proxy_source SET DEFAULT 'account',
    ALTER COLUMN proxy_source SET NOT NULL;

ALTER TABLE openai_oauth_reauth_configs
    DROP CONSTRAINT IF EXISTS openai_oauth_reauth_configs_proxy_source_check;
ALTER TABLE openai_oauth_reauth_configs
    ADD CONSTRAINT openai_oauth_reauth_configs_proxy_source_check CHECK (
        (proxy_source = 'managed_proxy' AND proxy_id IS NOT NULL)
        OR (proxy_source IN ('account', 'mihomo') AND proxy_id IS NULL)
    );
