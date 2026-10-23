-- Forward only. Existing auto-ban exemptions never imply review exemptions.
-- Preserve account status, hit generations/counts, versions and existing actions.
ALTER TABLE content_moderation_user_state
    ADD COLUMN IF NOT EXISTS keyword_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS api_audit_exempt BOOLEAN NOT NULL DEFAULT FALSE;

-- Record all three resulting switches in each future administrative action.
-- Historical actions retain false for the new exemption types.
ALTER TABLE content_moderation_user_actions
    ADD COLUMN IF NOT EXISTS keyword_exempt BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS api_audit_exempt BOOLEAN NOT NULL DEFAULT FALSE;
