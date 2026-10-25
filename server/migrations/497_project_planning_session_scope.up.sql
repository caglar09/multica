ALTER TABLE chat_session DROP CONSTRAINT IF EXISTS chat_session_session_kind_check;
ALTER TABLE chat_session ADD CONSTRAINT chat_session_session_kind_check
    CHECK (session_kind IN ('standard', 'autonomous_project_leader', 'autonomous_project_planning'));

-- Recover existing planner history without deleting messages or changing runs.
-- A title alone is not enough: require the project's provisioned manager or
-- the former internal planner carrier as well.
UPDATE chat_session s
SET session_kind = 'autonomous_project_planning'
WHERE s.session_kind = 'standard'
  AND s.project_id IS NOT NULL
  AND s.title = 'Autonomous Project Planning'
  AND (
    EXISTS (SELECT 1 FROM agent a WHERE a.id = s.agent_id
            AND a.workspace_id = s.workspace_id AND a.system_key = 'autonomous_project_planner')
    OR EXISTS (
      SELECT 1 FROM autonomous_project_team t
      JOIN autonomous_project_team_member tm ON tm.team_id = t.id
      WHERE t.workspace_id = s.workspace_id AND t.project_id = s.project_id
        AND tm.agent_id = s.agent_id AND tm.role = 'product_manager'
    )
  );
