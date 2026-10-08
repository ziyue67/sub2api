-- Support tickets ("网站工单"): users open text tickets and talk with admins.
-- The code says "support ticket" because "ticket" already names Codex turn tickets.

CREATE TABLE IF NOT EXISTS support_tickets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Category name as chosen at creation; renaming a category keeps old tickets readable.
    category VARCHAR(32) NOT NULL DEFAULT '',
    title VARCHAR(100) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'replied', 'closed')),
    message_count INTEGER NOT NULL DEFAULT 0,
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_message_role VARCHAR(8) NOT NULL DEFAULT 'user' CHECK (last_message_role IN ('user', 'admin')),
    -- An admin wrote since the user last opened the ticket.
    user_unread BOOLEAN NOT NULL DEFAULT FALSE,
    -- The user wrote since an admin last opened the ticket.
    admin_unread BOOLEAN NOT NULL DEFAULT TRUE,
    closed_at TIMESTAMPTZ,
    closed_by_role VARCHAR(8) NOT NULL DEFAULT '' CHECK (closed_by_role IN ('', 'user', 'admin')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_support_tickets_user_activity
    ON support_tickets (user_id, last_message_at DESC);
CREATE INDEX IF NOT EXISTS idx_support_tickets_status_activity
    ON support_tickets (status, last_message_at DESC);

CREATE TABLE IF NOT EXISTS support_ticket_messages (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author_role VARCHAR(8) NOT NULL CHECK (author_role IN ('user', 'admin')),
    author_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 5000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_support_ticket_messages_ticket
    ON support_ticket_messages (ticket_id, id);
-- Per-author flood limits count recent messages.
CREATE INDEX IF NOT EXISTS idx_support_ticket_messages_author_time
    ON support_ticket_messages (author_user_id, created_at);
