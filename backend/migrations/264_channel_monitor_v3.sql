-- Channel monitor V3: an admin-curated status page over the passive V2
-- aggregates of real user requests. Components only map groups to rows on the
-- page; V3 sends no requests and stores no facts of its own.

CREATE TABLE IF NOT EXISTS channel_monitor_v3_categories (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(64) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS channel_monitor_v3_components (
    id BIGSERIAL PRIMARY KEY,
    category_id BIGINT REFERENCES channel_monitor_v3_categories(id) ON DELETE SET NULL,
    name VARCHAR(100) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    -- Empty means every model requested in the group.
    model VARCHAR(100) NOT NULL DEFAULT '',
    -- 0 inherits channel_monitor_v3_config.degraded_ttft_ms.
    degraded_ttft_ms INTEGER NOT NULL DEFAULT 0
        CHECK (degraded_ttft_ms = 0 OR degraded_ttft_ms BETWEEN 1000 AND 120000),
    show_multiplier BOOLEAN NOT NULL DEFAULT TRUE,
    visibility VARCHAR(16) NOT NULL DEFAULT 'group' CHECK (visibility IN ('group', 'public')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_components_order
    ON channel_monitor_v3_components (category_id, sort_order, id);

CREATE TABLE IF NOT EXISTS channel_monitor_v3_config (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version INTEGER NOT NULL DEFAULT 1,
    interval_minutes INTEGER NOT NULL DEFAULT 5 CHECK (interval_minutes IN (1, 2, 3, 5, 10, 15, 30, 60)),
    cells INTEGER NOT NULL DEFAULT 90 CHECK (cells BETWEEN 30 AND 120),
    availability_range VARCHAR(8) NOT NULL DEFAULT '7d' CHECK (availability_range IN ('24h', '7d', '30d')),
    down_error_rate NUMERIC(5, 4) NOT NULL DEFAULT 0.2 CHECK (down_error_rate > 0 AND down_error_rate <= 1),
    degraded_error_rate NUMERIC(5, 4) NOT NULL DEFAULT 0.05 CHECK (degraded_error_rate > 0 AND degraded_error_rate <= 1),
    degraded_ttft_ms INTEGER NOT NULL DEFAULT 10000 CHECK (degraded_ttft_ms BETWEEN 1000 AND 120000),
    min_requests INTEGER NOT NULL DEFAULT 1 CHECK (min_requests BETWEEN 1 AND 1000),
    -- Errors a user caused do not count against a channel (same defaults as V2).
    ignored_error_categories TEXT[] NOT NULL DEFAULT ARRAY[
        'authentication', 'client_cancelled', 'content_policy', 'context_limit',
        'group_access', 'model_unsupported', 'not_found', 'quota_or_balance'
    ]::TEXT[],
    featured_component_id BIGINT REFERENCES channel_monitor_v3_components(id) ON DELETE SET NULL,
    footer_note VARCHAR(500) NOT NULL DEFAULT '',
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO channel_monitor_v3_config (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE channel_monitor_v3_components IS
    'V3 status-page rows; health comes from channel_monitor_v2_* passive facts of the group.';
