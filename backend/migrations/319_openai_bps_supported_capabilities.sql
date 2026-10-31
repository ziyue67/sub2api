-- Managed BPS has a native Responses adapter. Chat/Messages generic bridges do
-- not enter it, so never advertise those routes until a real bridge exists.
UPDATE openai_auth_connections
SET capabilities=capabilities-'chat_completions',revision=revision+1,updated_at=NOW()
WHERE kind='bps' AND capabilities ? 'chat_completions';
