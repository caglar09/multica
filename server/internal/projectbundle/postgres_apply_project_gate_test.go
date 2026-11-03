package projectbundle

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectImportKeepsTasksUnassignedUntilLocalAdoption(t *testing.T) {
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

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback-only import fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	fixtureID := uuid.NewString()
	actorName := "Project import gate " + fixtureID
	actorEmail := "project-import-gate-" + fixtureID + "@example.com"
	var userID, workspaceID pgtype.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, actorName, actorEmail).Scan(&userID); err != nil {
		t.Fatalf("insert transaction-scoped user: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', 'PIG') RETURNING id
	`, "Project import gate "+fixtureID, "project-import-gate-"+fixtureID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert transaction-scoped workspace: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("insert transaction-scoped workspace member: %v", err)
	}

	sourceStatus := "in_progress"
	sourceStatusCategory := "started"
	bundle := Bundle{
		SchemaVersion: SchemaVersionV2,
		Project:       PortableProject{Title: "Imported execution gate " + fixtureID},
		Tasks: []PortableTask{{
			Key:            "portable-task-1",
			Title:          "Keep imported work local until adoption",
			Status:         &sourceStatus,
			StatusCategory: &sourceStatusCategory,
			Assignee:       &PortableActorRef{Type: "member", Name: actorName, Email: actorEmail},
		}},
		Bootstrap: &PortableBootstrap{Plan: &PortablePlan{
			Goal: "Import provenance without granting execution",
			Specification: &PortableSpecification{
				Goal:                      "Import provenance without granting execution",
				Summary:                   "Imported intent requires local adoption.",
				Requirements:              []PortableSpecificationRequirement{{ID: "REQ-IMPORT-1", Text: "Keep imported work gated", Rationale: "Local adoption is required", Source: "user"}},
				NonFunctionalRequirements: []string{},
				Constraints:               []string{},
				NonGoals:                  []string{},
				DefinitionOfDone:          []string{},
				AcceptanceExpectations:    []string{},
			},
			Nodes: []PlanNode{{
				Key:                "portable-task-1",
				Title:              "Keep imported work local until adoption",
				Kind:               "implementation",
				AcceptanceCriteria: []string{"Imported issue is not assigned or dispatchable."},
				RequirementIDs:     []string{"REQ-IMPORT-1"},
			}},
		}},
	}
	if err := ValidateBundleForImport(bundle); err != nil {
		t.Fatalf("validate imported bundle: %v", err)
	}

	store := &PostgresStore{}
	if _, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, nil, bundle, ApplyOptions{}); err != nil {
		t.Fatalf("apply project import: %v", err)
	}

	var issueID, projectID, assigneeID pgtype.UUID
	var status string
	var assigneeType pgtype.Text
	if err := tx.QueryRow(ctx, `
		SELECT id, project_id, status, assignee_type, assignee_id
		FROM issue
		WHERE workspace_id = $1 AND metadata->>'multica_portable_key' = $2
	`, workspaceID, "portable-task-1").Scan(&issueID, &projectID, &status, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("read imported task issue: %v", err)
	}
	if status != "todo" {
		t.Fatalf("imported task status = %q, want todo regardless of source status %q", status, sourceStatus)
	}
	if assigneeType.Valid || assigneeID.Valid {
		t.Fatalf("imported task assignee = (%v, %v), want unassigned despite matching source actor", assigneeType, assigneeID)
	}

	// Recreate an issue row left dispatchable by the older importer, then sync
	// the same bundle. The source state matches the row, so this also proves the
	// import guard runs when no source-side field diff would trigger updateTask.
	if _, err := tx.Exec(ctx, `
		UPDATE issue SET status = 'in_progress', assignee_type = 'member', assignee_id = $3
		WHERE id = $1 AND workspace_id = $2
	`, issueID, workspaceID, userID); err != nil {
		t.Fatalf("seed prior imported task state: %v", err)
	}
	projectIDForSync := projectID
	if _, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, &projectIDForSync, bundle, ApplyOptions{}); err != nil {
		t.Fatalf("sync imported project: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT status, assignee_type, assignee_id FROM issue WHERE id = $1 AND workspace_id = $2
	`, issueID, workspaceID).Scan(&status, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("read synced imported task issue: %v", err)
	}
	if status != "todo" {
		t.Fatalf("synced imported task status = %q, want todo", status)
	}
	if assigneeType.Valid || assigneeID.Valid {
		t.Fatalf("synced imported task assignee = (%v, %v), want unassigned", assigneeType, assigneeID)
	}

	var planID pgtype.UUID
	var planStatus string
	var approvedAt pgtype.Timestamptz
	var executable bool
	if err := tx.QueryRow(ctx, `
		SELECT id, status, approved_at, autonomous_project_plan_executable(id)
		FROM autonomous_project_plan WHERE workspace_id = $1 AND project_id = $2
		ORDER BY revision DESC LIMIT 1
	`, workspaceID, projectID).Scan(&planID, &planStatus, &approvedAt, &executable); err != nil {
		t.Fatalf("read imported plan execution gate: %v", err)
	}
	if planStatus != "awaiting_approval" || approvedAt.Valid || executable {
		t.Fatalf("imported plan gate = status %q, approved_at valid %t, executable %t; want awaiting_approval, no approval, and ineligible", planStatus, approvedAt.Valid, executable)
	}

	var requirementIDsJSON []byte
	var materializedIssueID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT requirement_ids, materialized_issue_id
		FROM autonomous_project_plan_node WHERE plan_id = $1 AND node_key = $2
	`, planID, "portable-task-1").Scan(&requirementIDsJSON, &materializedIssueID); err != nil {
		t.Fatalf("read imported plan node: %v", err)
	}
	var requirementIDs []string
	if err := json.Unmarshal(requirementIDsJSON, &requirementIDs); err != nil {
		t.Fatalf("decode imported plan node requirement IDs: %v", err)
	}
	if len(requirementIDs) != 1 || requirementIDs[0] != "REQ-IMPORT-1" {
		t.Fatalf("imported plan node requirement IDs = %#v, want [REQ-IMPORT-1]", requirementIDs)
	}
	if !materializedIssueID.Valid || materializedIssueID != issueID {
		t.Fatalf("imported plan node materialized issue = %v, want %v", materializedIssueID, issueID)
	}

	t.Log("PROJECT_IMPORT_DISPATCH_GATE_PASS")
}
