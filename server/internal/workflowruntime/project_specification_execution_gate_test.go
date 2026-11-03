package workflowruntime

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/workflow"
)

func TestProjectRestartRequiresApprovedSpecification(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	revisionID := uuid.New()
	var revision int64
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(revision),0)+1 FROM autonomous_project_specification_revision
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID).Scan(&revision); err != nil {
		t.Fatalf("read next specification revision: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_specification_revision (
			id,workspace_id,project_id,revision,source_kind,source_ref,goal,specification
		) VALUES ($1,$2,$3,$4,'project_bootstrap',$5,'Release an approved project',
		         '{"summary":"Pending scope","requirements":[{"id":"REQ-1","text":"Release safely","rationale":"Production scope","source":"brief"}]}'::jsonb)
	`, revisionID, fixture.workspaceID, fixture.projectID, revision, "restart-gate-"+uuid.NewString()); err != nil {
		t.Fatalf("insert pending specification revision: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_specification_head (workspace_id,project_id,specification_revision_id,revision)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (workspace_id,project_id) DO UPDATE
		SET specification_revision_id=EXCLUDED.specification_revision_id,revision=EXCLUDED.revision,updated_at=now()
	`, fixture.workspaceID, fixture.projectID, revisionID, revision); err != nil {
		t.Fatalf("activate pending specification revision: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET specification_revision_id=$2, status='active', approved_at=now(),
		    plan_digest=autonomous_project_plan_digest(id)
		WHERE id=$1
	`, fixture.planID, revisionID); err != nil {
		t.Fatalf("link current plan to pending specification: %v", err)
	}
	store := projectorchestration.NewStore(fixture.pool)
	runtime := fixture.runtime(nil)
	runtime.store = workflow.NewPostgresStore(fixture.pool)

	if err := runtime.RestartProjectWorkflow(ctx, fixture.workspaceID, fixture.projectID); !errors.Is(err, projectorchestration.ErrPlanApprovalRequired) {
		t.Fatalf("restart error = %v, want specification approval required", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE autonomous_project_plan SET status='completed' WHERE id=$1`, fixture.planID); err != nil {
		t.Fatalf("complete plan before repair regression: %v", err)
	}
	if err := store.ResumeCompletedPlanForDiscoveredWork(ctx, fixture.workspaceID, fixture.projectID, fixture.planID); !errors.Is(err, projectorchestration.ErrPlanApprovalRequired) {
		t.Fatalf("repair error = %v, want specification approval required", err)
	}
	var status string
	if err := fixture.pool.QueryRow(ctx, `SELECT status FROM autonomous_project_plan WHERE id=$1`, fixture.planID).Scan(&status); err != nil {
		t.Fatalf("read plan after blocked restart/repair: %v", err)
	}
	if status != "completed" {
		t.Fatalf("plan status after blocked restart/repair = %q, want completed", status)
	}
	t.Log("PROJECT_RESTART_SPECIFICATION_GATE_PASS")
}
