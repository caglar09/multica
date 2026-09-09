ALTER TABLE chat_message DROP COLUMN IF EXISTS client_message_id;
ALTER TABLE chat_session DROP COLUMN IF EXISTS session_kind;
