-- Store only AES-GCM ciphertext, separate from account details and JSON exports.
CREATE TABLE IF NOT EXISTS openai_session_cookies (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    cookie_ciphertext TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
