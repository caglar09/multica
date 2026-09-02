DROP TABLE IF EXISTS autonomous_project_team_analysis;

ALTER TABLE autonomous_project_team_member
    DROP COLUMN IF EXISTS active,
    DROP COLUMN IF EXISTS reason,
    DROP COLUMN IF EXISTS responsibilities,
    DROP COLUMN IF EXISTS capabilities,
    DROP COLUMN IF EXISTS role_family;

ALTER TABLE autonomous_project_team
    DROP COLUMN IF EXISTS last_planned_at,
    DROP COLUMN IF EXISTS plan_revision,
    DROP COLUMN IF EXISTS planner_model,
    DROP COLUMN IF EXISTS planner_name;
