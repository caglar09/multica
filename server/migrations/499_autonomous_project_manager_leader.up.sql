-- Keep the autonomous team coordinator role aligned with the squad leader.
-- Issue routing still uses the implementation role; this only repairs the
-- squad-level leader identity for teams provisioned before the coordinator
-- became the canonical leader.
UPDATE squad s
SET leader_id = tm.agent_id
FROM autonomous_project_team team
JOIN autonomous_project_team_member tm
  ON tm.team_id = team.id
 AND tm.role = 'product_manager'
 AND tm.active = TRUE
WHERE s.id = team.squad_id
  AND s.leader_id IS DISTINCT FROM tm.agent_id;
