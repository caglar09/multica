-- Restore the leader selected by the pre-migration provisioning rule.
UPDATE squad s
SET leader_id = tm.agent_id
FROM autonomous_project_team team
JOIN autonomous_project_team_member tm
  ON tm.team_id = team.id
 AND tm.role = COALESCE(NULLIF(team.plan->>'implementation_role', ''), 'fullstack_engineer')
 AND tm.active = TRUE
WHERE s.id = team.squad_id
  AND s.leader_id IS DISTINCT FROM tm.agent_id;
