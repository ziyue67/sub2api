-- Account proxy lanes
--
-- A lane is one independently schedulable egress for an account.  The
-- account credentials remain in `accounts`; this table only describes the
-- egress and its per-lane scheduling limits.  Existing accounts keep their
-- legacy `accounts.proxy_id`/`accounts.concurrency` behaviour until at least
-- one lane is configured for them.

CREATE TABLE IF NOT EXISTS account_proxy_lanes (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    -- A proxy cannot be removed while a lane still references it.  SET NULL
    -- would leave transport='proxy' rows violating the invariant below.
    proxy_id BIGINT REFERENCES proxies(id) ON DELETE RESTRICT,
    name VARCHAR(100) NOT NULL,
    transport VARCHAR(20) NOT NULL DEFAULT 'proxy',
    concurrency INTEGER NOT NULL DEFAULT 1,
    weight INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 50,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    schedulable BOOLEAN NOT NULL DEFAULT TRUE,
    cooldown_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT account_proxy_lanes_transport_check
        CHECK (transport IN ('proxy', 'direct')),
    CONSTRAINT account_proxy_lanes_transport_proxy_check
        CHECK ((transport = 'direct' AND proxy_id IS NULL)
            OR (transport = 'proxy' AND proxy_id IS NOT NULL)),
    CONSTRAINT account_proxy_lanes_concurrency_check
        CHECK (concurrency >= 0),
    CONSTRAINT account_proxy_lanes_weight_check
        CHECK (weight >= 0),
    CONSTRAINT account_proxy_lanes_priority_check
        CHECK (priority >= 0)
);

-- Names are the stable human/API identity of a lane within an account.
CREATE UNIQUE INDEX IF NOT EXISTS account_proxy_lanes_account_name_uq
    ON account_proxy_lanes (account_id, name);

-- Service validation treats lane names case-insensitively.  Keep that
-- invariant race-safe at the database boundary as well (the legacy exact
-- index above is retained for compatibility with existing query plans).
CREATE UNIQUE INDEX IF NOT EXISTS account_proxy_lanes_account_name_ci_uq
    ON account_proxy_lanes (account_id, LOWER(name));

-- Do not accidentally mount the same proxy twice for one account.  The
-- partial predicate deliberately excludes direct lanes (their proxy_id is
-- NULL and an account may keep more than one direct lane if desired).
CREATE UNIQUE INDEX IF NOT EXISTS account_proxy_lanes_account_proxy_uq
    ON account_proxy_lanes (account_id, proxy_id)
    WHERE transport = 'proxy' AND proxy_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS account_proxy_lanes_account_schedulable_idx
    ON account_proxy_lanes (account_id, status, schedulable);

CREATE INDEX IF NOT EXISTS account_proxy_lanes_account_priority_idx
    ON account_proxy_lanes (account_id, priority, id);

CREATE INDEX IF NOT EXISTS account_proxy_lanes_proxy_idx
    ON account_proxy_lanes (proxy_id);
