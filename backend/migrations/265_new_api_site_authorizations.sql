-- User management credentials are shared by site and upstream user, never by
-- host alone. The cipher uses the configured fixed application secret key.
CREATE TABLE IF NOT EXISTS new_api_site_authorizations (
 id BIGSERIAL PRIMARY KEY,
 site_url TEXT NOT NULL,
 upstream_user_id BIGINT NOT NULL CHECK (upstream_user_id > 0),
 access_token_ciphertext TEXT NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE (site_url, upstream_user_id)
);
CREATE TABLE IF NOT EXISTS new_api_account_bindings (
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 authorization_id BIGINT NOT NULL REFERENCES new_api_site_authorizations(id) ON DELETE CASCADE,
 upstream_token_id BIGINT NOT NULL CHECK (upstream_token_id > 0),
 account_fingerprint TEXT NOT NULL,
 token_group TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS new_api_account_bindings_authorization_id_idx ON new_api_account_bindings(authorization_id);

-- Account credentials can also change through bulk edits, imports and token
-- refresh helpers. Invalidate the binding in the same row transaction on every
-- write path; unrelated model mappings and account metadata keep it intact.
CREATE OR REPLACE FUNCTION invalidate_new_api_account_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.platform IS DISTINCT FROM NEW.platform
    OR OLD.type IS DISTINCT FROM NEW.type
    OR OLD.proxy_id IS DISTINCT FROM NEW.proxy_id
    OR OLD.credentials -> 'api_key' IS DISTINCT FROM NEW.credentials -> 'api_key'
    OR OLD.credentials -> 'base_url' IS DISTINCT FROM NEW.credentials -> 'base_url'
    OR OLD.credentials -> 'header_override_enabled' IS DISTINCT FROM NEW.credentials -> 'header_override_enabled'
    OR OLD.credentials -> 'header_overrides' IS DISTINCT FROM NEW.credentials -> 'header_overrides'
 THEN
  DELETE FROM new_api_account_bindings WHERE account_id = OLD.id;
  IF FOUND OR OLD.extra ->> 'upstream_billing_provider' = 'new_api' THEN
   NEW.extra := COALESCE(NEW.extra, '{}'::jsonb) - 'upstream_billing_provider' - 'upstream_billing_probe';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS new_api_account_binding_identity_guard ON accounts;
CREATE TRIGGER new_api_account_binding_identity_guard
BEFORE UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION invalidate_new_api_account_binding();
