CREATE TABLE IF NOT EXISTS autonomous_project_control (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    paused BOOLEAN NOT NULL DEFAULT FALSE,
    paused_at TIMESTAMPTZ,
    paused_by UUID,
    replan_requested_at TIMESTAMPTZ,
    replan_requested_by UUID,
    replan_completed_at TIMESTAMPTZ,
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_autonomous_project_control_workspace
ON autonomous_project_control (workspace_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_autonomous_project_control_replan
ON autonomous_project_control (replan_requested_at)
WHERE replan_requested_at IS NOT NULL
  AND (replan_completed_at IS NULL OR replan_completed_at < replan_requested_at);
