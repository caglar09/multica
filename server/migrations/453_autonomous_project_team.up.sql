-- Durable project-specific technology team registry for autonomous software work.
--
-- These tables intentionally carry no foreign keys, matching the repository's
-- application-owned lifecycle policy. Provisioning is serialized per project
-- and writes the team, generated agents, squad and registry in one transaction.
CREATE TABLE IF NOT EXISTS autonomous_project_team (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    squad_id UUID NOT NULL,
    planner_version INTEGER NOT NULL DEFAULT 1,
    intent TEXT NOT NULL,
    plan JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'active',
    created_by_agent_id UUID NOT NULL,
    owner_user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, project_id),
    CHECK (status IN ('active', 'archived'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_team_member (
    team_id UUID NOT NULL,
    role TEXT NOT NULL,
    agent_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, role),
    UNIQUE (team_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_autonomous_project_team_member_agent
ON autonomous_project_team_member (agent_id);
