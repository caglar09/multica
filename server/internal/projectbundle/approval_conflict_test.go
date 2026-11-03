package projectbundle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestPortabilityApprovalConflictsAreVisibleAndBlockSync(t *testing.T) {
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
		t.Fatalf("begin rollback-only portability conflict fixture: %v", err)
	}
	defer tx.Rollback(ctx)
	fixtureID := uuid.NewString()
	name := "Portability conflict gate " + fixtureID
	var workspaceID, userID pgtype.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO "user"(name,email) VALUES($1,$2) RETURNING id`, name, "portability-conflict-"+fixtureID+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("insert conflict fixture user: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO workspace(name,slug,description,issue_prefix) VALUES($1,$2,'','PCG') RETURNING id`, name, "portability-conflict-"+fixtureID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert conflict fixture workspace: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("insert conflict fixture owner: %v", err)
	}

	goal := "Preserve imported approval provenance"
	oldContent := json.RawMessage(`{"summary":"Old imported intent","requirements":[{"id":"REQ-OLD","text":"Preserve history","rationale":"Keep the old revision","source":"brief"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}`)
	currentContent := json.RawMessage(`{"summary":"Current imported intent","requirements":[{"id":"REQ-CURRENT","text":"Reject contradictory approvals","rationale":"Approval digests are immutable","source":"brief"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}`)
	oldDigest := approvalConflictSpecificationDigest(t, tx, goal, oldContent)
	currentDigest := approvalConflictSpecificationDigest(t, tx, goal, currentContent)
	oldSourceID, currentSourceID := uuid.NewString(), uuid.NewString()
	oldSourceRef, currentSourceRef := "conflict-history-old-"+fixtureID, "conflict-history-current-"+fixtureID
	actorID := uuidString(userID)
	approvedRevision := int64(2)
	approval := PortableApproval{
		ID: uuid.NewString(), IdempotencyKey: "source-approval:" + fixtureID,
		SubjectType: "specification_revision", SubjectID: currentSourceID,
		SubjectRevision: &approvedRevision, SubjectDigest: currentDigest,
		Decision: "approved", ActorType: "human", ActorID: &actorID,
		ActorDisplay: "Source owner", Rationale: "Approved the source revision",
		SourceEndpoint: "/api/projects/source/autonomous/spec/approve", Origin: "local",
		OriginRef: json.RawMessage(`{"source_request":"approved-2"}`), ActedAt: "2026-10-21T10:00:00Z",
	}
	specification := PortableSpecification{
		Goal: goal, Summary: "Current imported intent",
		Requirements:              []PortableSpecificationRequirement{{ID: "REQ-CURRENT", Text: "Reject contradictory approvals", Rationale: "Approval digests are immutable", Source: "brief"}},
		NonFunctionalRequirements: []string{}, Constraints: []string{}, NonGoals: []string{}, DefinitionOfDone: []string{}, AcceptanceExpectations: []string{},
	}
	plan := PortablePlan{
		Goal:          goal,
		Requirements:  []PlanRequirement{{ID: "REQ-CURRENT", Text: "Reject contradictory approvals"}},
		Specification: &specification,
		Nodes:         []PlanNode{{Key: "portable-task", Title: "Keep imported issue", RequirementIDs: []string{"REQ-CURRENT"}}},
	}
	taskStatus := "in_progress"
	initial := Bundle{
		SchemaVersion: SchemaVersionV2,
		Project:       PortableProject{Title: "Conflict baseline " + fixtureID},
		Tasks:         []PortableTask{{Key: "portable-task", Title: "Keep imported issue", Status: &taskStatus}},
		Bootstrap:     &PortableBootstrap{Plan: &plan},
		Approvals:     []PortableApproval{approval},
		SpecificationRevisions: []PortableSpecificationRevision{
			{ID: oldSourceID, Revision: 1, SourceKind: "planner", SourceRef: &oldSourceRef, Goal: goal, Content: oldContent, Digest: oldDigest, CreatedAt: "2026-10-20T10:00:00Z"},
			{ID: currentSourceID, Revision: 2, SourceKind: "change_request", SourceRef: &currentSourceRef, Goal: goal, Content: currentContent, Digest: currentDigest, CreatedAt: "2026-10-21T10:00:00Z"},
		},
	}
	if err := ValidateBundleForImport(initial); err != nil {
		t.Fatalf("validate initial import bundle: %v", err)
	}
	store := &PostgresStore{}
	created, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, nil, initial, ApplyOptions{})
	if err != nil {
		t.Fatalf("create baseline imported project: %v", err)
	}
	var projectID pgtype.UUID
	if err := projectID.Scan(created.ProjectID); err != nil {
		t.Fatalf("parse baseline project id: %v", err)
	}

	// Exact imported approval payloads and the same specification history are
	// no-ops across repeated syncs.
	if _, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, &projectID, initial, ApplyOptions{}); err != nil {
		t.Fatalf("reapply identical imported bundle: %v", err)
	}
	var approvalCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_approval_event WHERE project_id=$1`, projectID).Scan(&approvalCount); err != nil {
		t.Fatalf("count approval events after idempotent sync: %v", err)
	}
	if approvalCount != 1 {
		t.Fatalf("identical import created %d approval events, want one", approvalCount)
	}
	var initialStatus string
	var initialAssigneeType pgtype.Text
	var initialAssigneeID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT status,assignee_type,assignee_id FROM issue WHERE project_id=$1 AND metadata->>'multica_portable_key'='portable-task'`, projectID).Scan(&initialStatus, &initialAssigneeType, &initialAssigneeID); err != nil {
		t.Fatalf("read task after import: %v", err)
	}
	if initialStatus != "todo" || initialAssigneeType.Valid || initialAssigneeID.Valid {
		t.Fatalf("imported task = status %q, assignee (%v,%v); want todo and unassigned", initialStatus, initialAssigneeType, initialAssigneeID)
	}

	current, err := exportPortabilityComparisonBundle(ctx, tx, workspaceID, projectID)
	if err != nil {
		t.Fatalf("export current bundle for conflict preview: %v", err)
	}
	contradictoryDigest := strings.Repeat("f", 64)
	changedApproval := approval
	changedApproval.SubjectDigest = contradictoryDigest
	changedApproval.Rationale = "Conflicting source payload under the same idempotency key"
	contradictoryApproval := changedApproval
	contradictoryApproval.ID = uuid.NewString()
	contradictoryApproval.IdempotencyKey = "source-approval-contradictory:" + fixtureID
	contradictoryApproval.Rationale = "Conflicting approved digest under a distinct idempotency key"
	olderHistory := initial.SpecificationRevisions[:1]
	changedTaskTitle := "Would update before conflict"
	newTaskStatus := "done"
	resourceLabel := "Would create before conflict"
	resourcePosition := int32(1)
	incoming := initial
	incoming.Project = PortableProject{Title: "Should not be applied"}
	incoming.Tasks = []PortableTask{
		{Key: "portable-task", Title: changedTaskTitle, Status: &newTaskStatus},
		{Key: "new-task", Title: "Would create before conflict"},
	}
	incoming.Resources = []PortableResource{{
		ResourceType: "link", Label: &resourceLabel, ResourceRef: json.RawMessage(`{"url":"https://example.com/conflict"}`), Position: &resourcePosition,
	}}
	incoming.Approvals = []PortableApproval{changedApproval, contradictoryApproval}
	incoming.SpecificationRevisions = olderHistory

	preview := BuildPreview(current, incoming, false)
	if len(preview.ApprovalConflicts) == 0 {
		t.Fatal("preview did not report the divergent idempotency payload / approved digest")
	}
	containsConflict := func(fragment string) bool {
		for _, conflict := range preview.ApprovalConflicts {
			if strings.Contains(conflict, fragment) {
				return true
			}
		}
		return false
	}
	if !containsConflict("idempotency key") || !containsConflict("conflicting digests") {
		t.Fatalf("preview omitted a typed approval conflict: %#v", preview.ApprovalConflicts)
	}
	staleReported := false
	for _, change := range preview.SpecificationChanges {
		if strings.Contains(change, "behind local imported head") {
			staleReported = true
		}
	}
	if !staleReported {
		t.Fatalf("preview did not report stale imported specification history: %#v", preview.SpecificationChanges)
	}

	var oldHeadID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT specification_revision_id FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID).Scan(&oldHeadID); err != nil {
		t.Fatalf("read pre-conflict specification head: %v", err)
	}
	err = func() error {
		_, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, &projectID, incoming, ApplyOptions{})
		return err
	}()
	if !errors.Is(err, ErrPortabilityConflict) || !errors.Is(err, ErrApprovalLedgerConflict) ||
		!errors.Is(err, ErrApprovalProvenanceConflict) || !errors.Is(err, ErrSpecificationHistoryConflict) {
		t.Fatalf("apply conflict error = %v, want portability, ledger, provenance, and specification-history conflicts", err)
	}
	var projectTitle string
	var taskCount, resourceCount int
	var storedTaskTitle, storedTaskStatus string
	var currentHeadID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT title FROM project WHERE id=$1`, projectID).Scan(&projectTitle); err != nil {
		t.Fatalf("read project after rejected sync: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM issue WHERE project_id=$1`, projectID).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks after rejected sync: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT title,status FROM issue WHERE project_id=$1 AND metadata->>'multica_portable_key'='portable-task'`, projectID).Scan(&storedTaskTitle, &storedTaskStatus); err != nil {
		t.Fatalf("read existing task after rejected sync: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM project_resource WHERE project_id=$1`, projectID).Scan(&resourceCount); err != nil {
		t.Fatalf("count resources after rejected sync: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT specification_revision_id FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID).Scan(&currentHeadID); err != nil {
		t.Fatalf("read specification head after rejected sync: %v", err)
	}
	if projectTitle != initial.Project.Title || taskCount != 1 || storedTaskTitle != "Keep imported issue" || storedTaskStatus != "todo" || resourceCount != 0 || currentHeadID != oldHeadID {
		t.Fatalf("conflict mutated project state: title=%q tasks=%d task_title=%q task_status=%q resources=%d head=%v", projectTitle, taskCount, storedTaskTitle, storedTaskStatus, resourceCount, currentHeadID)
	}
	t.Log("PROJECT_BUNDLE_APPROVAL_CONFLICT_GATE_PASS")
}

func approvalConflictSpecificationDigest(t *testing.T, tx pgx.Tx, goal string, content json.RawMessage) string {
	t.Helper()
	var digest string
	if err := tx.QueryRow(context.Background(), `
		SELECT encode(digest(convert_to(jsonb_build_object('goal',$1::text,'specification',$2::jsonb)::text,'UTF8'),'sha256'),'hex')
	`, goal, string(content)).Scan(&digest); err != nil {
		t.Fatalf("digest conflict fixture specification: %v", err)
	}
	return digest
}
