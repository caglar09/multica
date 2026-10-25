ALTER TABLE chat_session
    ADD COLUMN IF NOT EXISTS session_kind TEXT NOT NULL DEFAULT 'standard'
        CHECK (session_kind IN ('standard', 'autonomous_project_leader'));

-- Client message ids are only interpreted by the Project Leader handler. The
-- nullable column keeps ordinary chat clients and their existing queries
-- unchanged while providing a durable deduplication key for leader chat.
ALTER TABLE chat_message
    ADD COLUMN IF NOT EXISTS client_message_id TEXT;
