UPDATE chat_session SET session_kind = 'standard'
WHERE session_kind = 'autonomous_project_planning';
ALTER TABLE chat_session DROP CONSTRAINT IF EXISTS chat_session_session_kind_check;
ALTER TABLE chat_session ADD CONSTRAINT chat_session_session_kind_check
    CHECK (session_kind IN ('standard', 'autonomous_project_leader'));
