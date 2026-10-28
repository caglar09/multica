package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectWorkflowTaskAdmissionRejectsPausedProject(t *testing.T) {
	ctx := context.Background()
	pool := sharedTestPool(t)
	var projectID, workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid(), gen_random_uuid()`).Scan(&projectID, &workspaceID); err != nil {
		t.Fatalf("create project control IDs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_control (project_id, workspace_id, paused)
		VALUES ($1, $2, true)
	`, projectID, workspaceID); err != nil {
		t.Fatalf("seed paused project control: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id=$1`, projectID)
	})

	svc := NewTaskService(db.New(pool), pool, nil, nil)
	_, err := svc.createAgentTaskForOrigin(ctx, db.Issue{
		ProjectID:   projectID,
		WorkspaceID: workspaceID,
	}, OriginDerived, db.CreateAgentTaskParams{})
	if !errors.Is(err, ErrProjectAutonomousPaused) {
		t.Fatalf("createAgentTaskForOrigin() error = %v, want ErrProjectAutonomousPaused", err)
	}
}


func TestProjectWorkflowTaskAdmissionRejectsCancelledProject(t *testing.T) {
	ctx := context.Background()
	pool := sharedTestPool(t)
	var projectID, workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid(), gen_random_uuid()`).Scan(&projectID, &workspaceID); err != nil {
		t.Fatalf("create project control IDs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_control (project_id, workspace_id, cancelled_at)
		VALUES ($1, $2, now())
	`, projectID, workspaceID); err != nil {
		t.Fatalf("seed cancelled project control: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id=$1`, projectID)
	})

	svc := NewTaskService(db.New(pool), pool, nil, nil)
	_, err := svc.createAgentTaskForOrigin(ctx, db.Issue{
		ProjectID:   projectID,
		WorkspaceID: workspaceID,
	}, OriginDerived, db.CreateAgentTaskParams{})
	if !errors.Is(err, ErrProjectAutonomousCancelled) {
		t.Fatalf("createAgentTaskForOrigin() error = %v, want ErrProjectAutonomousCancelled", err)
	}
}
