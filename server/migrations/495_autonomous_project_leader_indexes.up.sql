CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_chat_session_project_leader
    ON chat_session (workspace_id, project_id, creator_id)
    WHERE session_kind = 'autonomous_project_leader' AND status = 'active';
