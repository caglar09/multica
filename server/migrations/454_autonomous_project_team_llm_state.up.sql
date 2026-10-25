ALTER TABLE autonomous_project_team
    ADD COLUMN IF NOT EXISTS planner_name TEXT NOT NULL DEFAULT 'heuristic',
    ADD COLUMN IF NOT EXISTS planner_model TEXT,
    ADD COLUMN IF NOT EXISTS plan_revision BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS last_planned_at TIMESTAMPTZ;

ALTER TABLE autonomous_project_team_member
    ADD COLUMN IF NOT EXISTS role_family TEXT NOT NULL DEFAULT 'engineering',
    ADD COLUMN IF NOT EXISTS capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS responsibilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE IF NOT EXISTS autonomous_project_team_analysis (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL,
    source_type TEXT NOT NULL,
    source_id UUID NOT NULL,
    source_revision TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    planner_name TEXT NOT NULL,
    planner_model TEXT,
    plan JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (team_id, source_type, source_id, source_revision)
);

CREATE INDEX IF NOT EXISTS idx_autonomous_project_team_analysis_team
ON autonomous_project_team_analysis (team_id, created_at DESC);
