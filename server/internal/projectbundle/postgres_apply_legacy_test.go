package projectbundle

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestLegacyV1BundleImportRemainsUnapproved(t *testing.T) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	fixtureID := uuid.NewString()
	base := testutil.New(pool, "", "")
	actorName := "Legacy bundle importer " + fixtureID
	actorEmail := "legacy-bundle-import-" + fixtureID + "@example.com"
	userID := base.User(t, actorName, actorEmail)
	workspaceID := base.Workspace(t, "Legacy bundle workspace "+fixtureID, "legacy-bundle-"+fixtureID, testutil.Cols{"issue_prefix": "T9"})
	base.Member(t, workspaceID, userID, "owner")

	goal := "Import legacy intent without local approval"
	legacyTaskStatus := "in_progress"
	legacyTaskCategory := "started"
	bundle := Bundle{
		SchemaVersion: SchemaVersionV1,
		Project:       PortableProject{Title: "Legacy unapproved import " + fixtureID},
		Tasks: []PortableTask{{
			Key:            "legacy-task-1",
			Title:          "Keep legacy task undispatched",
			Status:         &legacyTaskStatus,
			StatusCategory: &legacyTaskCategory,
			Assignee:       &PortableActorRef{Type: "member", Name: actorName, Email: actorEmail},
		}},
		Bootstrap: &PortableBootstrap{Plan: &PortablePlan{
			Goal:         goal,
			Requirements: []PlanRequirement{{ID: "REQ-LEGACY-1", Text: "Import the original intent", Rationale: "Legacy exports predate approval fields"}},
			Specification: &PortableSpecification{
				Goal:                      goal,
				Summary:                   "A v1 bundle carries project intent without approval history.",
				Requirements:              []PortableSpecificationRequirement{{ID: "REQ-LEGACY-1", Text: "Import the original intent", Rationale: "Legacy exports predate approval fields", Source: "user"}},
				NonFunctionalRequirements: []string{},
				Constraints:               []string{},
				NonGoals:                  []string{},
				DefinitionOfDone:          []string{},
				AcceptanceExpectations:    []string{},
			},
			Nodes: []PlanNode{{
				Key:                "legacy-task-1",
				Title:              "Keep legacy task undispatched",
				Kind:               "implementation",
				AcceptanceCriteria: []string{"The task remains unassigned until a local approval exists."},
				RequirementIDs:     []string{"REQ-LEGACY-1"},
			}},
			Edges: []PlanEdge{},
		}},
	}
	if len(bundle.Approvals) != 0 || len(bundle.SpecificationRevisions) != 0 {
		t.Fatal("legacy v1 fixture must omit approval and specification-history fields")
	}
	if err := ValidateBundleForImport(bundle); err != nil {
		t.Fatalf("validate schema-v1 project bundle: %v", err)
	}

	var workspaceUUID, userUUID pgtype.UUID
	if err := workspaceUUID.Scan(workspaceID); err != nil {
		t.Fatalf("parse fixture workspace ID: %v", err)
	}
	if err := userUUID.Scan(userID); err != nil {
		t.Fatalf("parse fixture user ID: %v", err)
	}
	store := NewPostgresStore(db.New(pool), pool, pool, nil)
	result, err := store.ApplyPortability(ctx, workspaceUUID, userUUID, nil, bundle, ApplyOptions{})
	if err != nil {
		t.Fatalf("import schema-v1 bundle through PostgresStore: %v", err)
	}
	if result.ProjectID == "" {
		t.Fatal("schema-v1 import returned an empty project ID")
	}
	t.Cleanup(func() { cleanupLegacyV1ImportedProject(t, pool, result.ProjectID) })

	var projectID pgtype.UUID
	if err := projectID.Scan(result.ProjectID); err != nil {
		t.Fatalf("parse imported project ID: %v", err)
	}
	var planID, specificationID pgtype.UUID
	var planStatus string
	var approvedAt pgtype.Timestamptz
	var executable, specificationApproved bool
	if err := pool.QueryRow(ctx, `
		SELECT p.id,p.status,p.approved_at,p.specification_revision_id,
		       autonomous_project_plan_executable(p.id),
		       autonomous_project_specification_approved(p.specification_revision_id)
		FROM autonomous_project_plan p
		WHERE p.workspace_id=$1 AND p.project_id=$2
		ORDER BY p.revision DESC LIMIT 1
	`, workspaceUUID, projectID).Scan(&planID, &planStatus, &approvedAt, &specificationID, &executable, &specificationApproved); err != nil {
		t.Fatalf("read imported legacy plan approval gate: %v", err)
	}
	if planStatus != "awaiting_approval" || approvedAt.Valid || executable || specificationApproved {
		t.Fatalf("legacy plan state = status %q approved_at=%t executable=%t specification_approved=%t; want awaiting_approval and fully gated", planStatus, approvedAt.Valid, executable, specificationApproved)
	}

	var approvalRows, localApprovalRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*),count(*) FILTER (WHERE origin='local')
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2
	`, workspaceUUID, projectID).Scan(&approvalRows, &localApprovalRows); err != nil {
		t.Fatalf("count legacy project approval events: %v", err)
	}
	if approvalRows != 0 || localApprovalRows != 0 {
		t.Fatalf("legacy v1 import created %d approval events (%d local); want none", approvalRows, localApprovalRows)
	}

	var issueID, assigneeID pgtype.UUID
	var issueStatus string
	var assigneeType pgtype.Text
	if err := pool.QueryRow(ctx, `
		SELECT id,status,assignee_type,assignee_id
		FROM issue
		WHERE workspace_id=$1 AND project_id=$2 AND metadata->>'multica_portable_key'='legacy-task-1'
	`, workspaceUUID, projectID).Scan(&issueID, &issueStatus, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("read legacy imported issue: %v", err)
	}
	if issueStatus != "todo" || assigneeType.Valid || assigneeID.Valid {
		t.Fatalf("legacy imported task = status %q, assignee (%v,%v); want todo and unassigned", issueStatus, assigneeType, assigneeID)
	}
	var issueQueueRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, issueID).Scan(&issueQueueRows); err != nil {
		t.Fatalf("check legacy issue dispatch queue: %v", err)
	}
	if issueQueueRows != 0 {
		t.Fatalf("legacy imported task has %d dispatch queue rows; want none", issueQueueRows)
	}

	var originalHeadID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		SELECT specification_revision_id FROM autonomous_project_specification_head
		WHERE workspace_id=$1 AND project_id=$2
	`, workspaceUUID, projectID).Scan(&originalHeadID); err != nil {
		t.Fatalf("read legacy specification head before stale sync: %v", err)
	}
	var originalPlanCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2`, workspaceUUID, projectID).Scan(&originalPlanCount); err != nil {
		t.Fatalf("count legacy plans before stale sync: %v", err)
	}

	staleBundle := bundle
	staleBundle.Project = PortableProject{Title: "Stale bundle must not update project"}
	staleBundle.Tasks = []PortableTask{{Key: "legacy-task-1", Title: "Stale bundle must not update task"}}
	stalePlan := *bundle.Bootstrap.Plan
	staleSpecification := *stalePlan.Specification
	staleSpecification.Goal = "Older legacy goal"
	staleSpecification.Summary = "An older schema-v1 snapshot"
	stalePlan.Goal = staleSpecification.Goal
	stalePlan.Specification = &staleSpecification
	staleBootstrap := *bundle.Bootstrap
	staleBootstrap.Plan = &stalePlan
	staleBundle.Bootstrap = &staleBootstrap
	preview, err := store.PreviewPortability(ctx, workspaceUUID, &projectID, staleBundle, ApplyOptions{})
	if err != nil {
		t.Fatalf("preview stale schema-v1 sync: %v", err)
	}
	legacyConflictReported := false
	for _, change := range preview.SpecificationChanges {
		if strings.Contains(change, "legacy plan specification differs") {
			legacyConflictReported = true
		}
	}
	if !legacyConflictReported {
		t.Fatalf("preview did not report stale schema-v1 specification conflict: %#v", preview.SpecificationChanges)
	}
	if _, err := store.ApplyPortability(ctx, workspaceUUID, userUUID, &projectID, staleBundle, ApplyOptions{}); !errors.Is(err, ErrPortabilityConflict) || !errors.Is(err, ErrSpecificationHistoryConflict) {
		t.Fatalf("stale schema-v1 sync error = %v, want portability and specification-history conflicts", err)
	}
	var currentProjectTitle, currentTaskTitle string
	var currentTaskCount, currentPlanCount int
	var currentHeadID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT title FROM project WHERE id=$1 AND workspace_id=$2`, projectID, workspaceUUID).Scan(&currentProjectTitle); err != nil {
		t.Fatalf("read project after rejected stale sync: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE workspace_id=$1 AND project_id=$2`, workspaceUUID, projectID).Scan(&currentTaskCount); err != nil {
		t.Fatalf("count tasks after rejected stale sync: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT title FROM issue
		WHERE workspace_id=$1 AND project_id=$2 AND metadata->>'multica_portable_key'='legacy-task-1'
	`, workspaceUUID, projectID).Scan(&currentTaskTitle); err != nil {
		t.Fatalf("read task after rejected stale sync: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2`, workspaceUUID, projectID).Scan(&currentPlanCount); err != nil {
		t.Fatalf("count plans after rejected stale sync: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT specification_revision_id FROM autonomous_project_specification_head
		WHERE workspace_id=$1 AND project_id=$2
	`, workspaceUUID, projectID).Scan(&currentHeadID); err != nil {
		t.Fatalf("read specification head after rejected stale sync: %v", err)
	}
	if currentProjectTitle != bundle.Project.Title || currentTaskCount != 1 || currentTaskTitle != "Keep legacy task undispatched" ||
		currentPlanCount != originalPlanCount || currentHeadID != originalHeadID {
		t.Fatalf("stale schema-v1 sync mutated state: project=%q tasks=%d/%q plans=%d head=%v", currentProjectTitle, currentTaskCount, currentTaskTitle, currentPlanCount, currentHeadID)
	}
	t.Log("T9_LEGACY_V1_IMPORT_UNAPPROVED_GATE_PASS")
}

func cleanupLegacyV1ImportedProject(t *testing.T, pool *pgxpool.Pool, projectID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("begin legacy project fixture cleanup: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('multica.approval_purge','on',true)`); err != nil {
		t.Errorf("enable legacy project approval cleanup: %v", err)
		return
	}
	for _, statement := range []string{
		`DELETE FROM autonomous_project_approval_event WHERE project_id=$1`,
		`DELETE FROM issue_dependency WHERE issue_id IN (SELECT id FROM issue WHERE project_id=$1) OR depends_on_issue_id IN (SELECT id FROM issue WHERE project_id=$1)`,
		`DELETE FROM issue_to_label WHERE issue_id IN (SELECT id FROM issue WHERE project_id=$1)`,
		`DELETE FROM activity_log WHERE issue_id IN (SELECT id FROM issue WHERE project_id=$1)`,
		`DELETE FROM issue_child_event WHERE parent_id IN (SELECT id FROM issue WHERE project_id=$1) OR child_id IN (SELECT id FROM issue WHERE project_id=$1)`,
		`DELETE FROM comment WHERE issue_id IN (SELECT id FROM issue WHERE project_id=$1)`,
		`DELETE FROM agent_task_queue WHERE issue_id IN (SELECT id FROM issue WHERE project_id=$1)`,
		`DELETE FROM issue WHERE project_id=$1`,
		`DELETE FROM project_resource WHERE project_id=$1`,
		`DELETE FROM autonomous_project_plan_edge WHERE project_id=$1`,
		`DELETE FROM autonomous_project_plan_node WHERE project_id=$1`,
		`DELETE FROM autonomous_project_plan WHERE project_id=$1`,
		`DELETE FROM autonomous_project_specification_head WHERE project_id=$1`,
		`DELETE FROM autonomous_project_specification_revision WHERE project_id=$1`,
		`DELETE FROM autonomous_project_bootstrap WHERE project_id=$1`,
		`DELETE FROM autonomous_project_brain_entry WHERE project_id=$1`,
		`DELETE FROM project WHERE id=$1`,
	} {
		if _, err := tx.Exec(ctx, statement, projectID); err != nil {
			t.Errorf("cleanup legacy project fixture with %q: %v", statement, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit legacy project fixture cleanup: %v", err)
	}
}
