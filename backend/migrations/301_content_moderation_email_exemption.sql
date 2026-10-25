-- Forward only. Existing exemptions never imply a moderation email exemption.
ALTER TABLE content_moderation_user_state
    ADD COLUMN IF NOT EXISTS email_exempt BOOLEAN NOT NULL DEFAULT FALSE;

-- Snapshot the resulting switch for future actions; do not rewrite past actions.
ALTER TABLE content_moderation_user_actions
    ADD COLUMN IF NOT EXISTS email_exempt BOOLEAN NOT NULL DEFAULT FALSE;
