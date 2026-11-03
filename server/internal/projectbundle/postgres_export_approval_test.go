package projectbundle

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestExportProjectIncludesPortableApprovalsAndSpecifications(t *testing.T) {
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

	base := testutil.New(pool, "", "")
	userID := base.User(t, "Project bundle export test", "project-bundle-export-"+uuid.NewString()+"@example.com")
	workspaceID := base.Workspace(t, "Project bundle export test", "project-bundle-export-"+uuid.NewString(), testutil.Cols{"issue_prefix": "PBE"})
	base.Member(t, workspaceID, userID, "owner")
	fixture := testutil.New(pool, workspaceID, userID)
	projectID := fixture.Project(t, "Portable approval export")

	initialSpecification := `{"goal":"Preserve project intent","summary":"Initial intent","requirements":[{"id":"REQ-1","text":"Keep the source history","rationale":"Every revision must remain reviewable","source":"user"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}`
	currentSpecification := `{"goal":"Preserve project intent","summary":"Current authoritative intent","requirements":[{"id":"REQ-1","text":"Keep the source history","rationale":"Every revision must remain reviewable","source":"user"},{"id":"REQ-2","text":"Keep approval provenance","rationale":"Imported decisions remain attributable","source":"user"}],"non_functional_requirements":["Export in a stable order"],"constraints":[],"non_goals":[],"definition_of_done":["All history is exported"],"acceptance_expectations":[]}`
	initialRevisionID := fixture.Insert(t, "autonomous_project_specification_revision", testutil.Cols{
		"workspace_id":  workspaceID,
		"project_id":    projectID,
		"revision":      int64(1),
		"source_kind":   "project_bootstrap",
		"source_ref":    "initial-specification",
		"goal":          "Preserve project intent",
		"specification": testutil.Raw("'" + initialSpecification + "'::jsonb"),
		"created_at":    testutil.Raw("'2026-10-31T08:00:00Z'::timestamptz"),
	})
	currentRevisionID := fixture.Insert(t, "autonomous_project_specification_revision", testutil.Cols{
		"workspace_id":  workspaceID,
		"project_id":    projectID,
		"revision":      int64(2),
		"source_kind":   "change_request",
		"source_ref":    "change:approval-provenance",
		"goal":          "Preserve project intent",
		"specification": testutil.Raw("'" + currentSpecification + "'::jsonb"),
		"created_at":    testutil.Raw("'2026-11-01T08:00:00Z'::timestamptz"),
	})
	fixture.Cleanup(t, `DELETE FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID)
	fixture.Exec(t, `INSERT INTO autonomous_project_specification_head (workspace_id,project_id,specification_revision_id,revision)
		VALUES ($1,$2,$3,2)`, workspaceID, projectID, currentRevisionID)

	planID := fixture.Insert(t, "autonomous_project_plan", testutil.Cols{
		"workspace_id":              workspaceID,
		"project_id":                projectID,
		"revision":                  int64(2),
		"source_revision":           "export-test-revision",
		"planner_name":              "export-test-planner",
		"goal":                      "Build the execution plan",
		"specification":             testutil.Raw(`'{"requirements":["legacy plan requirement"]}'::jsonb`),
		"specification_revision_id": currentRevisionID,
		"status":                    "active",
		"plan_digest":               strings.Repeat("a", 64),
		"created_at":                testutil.Raw("'2026-11-01T09:00:00Z'::timestamptz"),
	})
	fixture.Insert(t, "autonomous_project_plan_node", testutil.Cols{
		"workspace_id":        workspaceID,
		"project_id":          projectID,
		"plan_id":             planID,
		"node_key":            "implement-history",
		"kind":                "implementation",
		"title":               "Preserve portable history",
		"description":         "Keep revisions and decisions attributable.",
		"status":              "pending",
		"priority":            1,
		"acceptance_criteria": testutil.Raw(`'["History is ordered and complete"]'::jsonb`),
		"requirement_ids":     testutil.Raw(`'["REQ-1","REQ-2"]'::jsonb`),
	})

	var initialDigest, currentDigest string
	if err := pool.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, initialRevisionID).Scan(&initialDigest); err != nil {
		t.Fatalf("compute initial specification digest: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, currentRevisionID).Scan(&currentDigest); err != nil {
		t.Fatalf("compute current specification digest: %v", err)
	}
	approvalEvents := []struct {
		idempotencyKey string
		subjectType    string
		subjectID      string
		subjectRev     int64
		subjectDigest  string
		decision       string
		actorType      string
		actorID        any
		actorDisplay   string
		policyRef      any
		rationale      string
		sourceEndpoint string
		origin         string
		originRef      string
		actedAt        time.Time
	}{
		{
			idempotencyKey: "approval-local-human",
			subjectType:    "specification_revision",
			subjectID:      currentRevisionID,
			subjectRev:     2,
			subjectDigest:  currentDigest,
			decision:       "approved",
			actorType:      "human",
			actorID:        userID,
			actorDisplay:   "Project owner",
			rationale:      "Reviewed current intent",
			sourceEndpoint: "/projects/approve-specification",
			origin:         "local",
			originRef:      `{"request_id":"local-approval-1"}`,
			actedAt:        time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			idempotencyKey: "approval-imported-human",
			subjectType:    "specification_revision",
			subjectID:      initialRevisionID,
			subjectRev:     1,
			subjectDigest:  initialDigest,
			decision:       "approved",
			actorType:      "human",
			actorDisplay:   "Imported reviewer",
			sourceEndpoint: "/imports/project-bundle",
			origin:         "imported",
			originRef:      `{"source_bundle":"archive-42","source_approval_id":"review-1"}`,
			actedAt:        time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			idempotencyKey: "approval-system-policy",
			subjectType:    "plan",
			subjectID:      planID,
			subjectRev:     2,
			subjectDigest:  strings.Repeat("a", 64),
			decision:       "approved",
			actorType:      "system",
			actorDisplay:   "Policy engine",
			policyRef:      "autonomous-plan-policy-v4",
			rationale:      "Plan meets configured policy",
			sourceEndpoint: "/system/project-plan-approval",
			origin:         "local",
			originRef:      `{"policy_version":"4"}`,
			actedAt:        time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC),
		},
	}
	t.Cleanup(func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('multica.approval_purge','on',true)`); err != nil {
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_approval_event WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID); err != nil {
			return
		}
		_ = tx.Commit(ctx)
	})
	for _, event := range approvalEvents {
		if _, err := pool.Exec(ctx, `
			INSERT INTO autonomous_project_approval_event (
				workspace_id, project_id, idempotency_key, subject_type, subject_id, subject_revision,
				subject_digest, decision, actor_type, actor_id, actor_display, policy_ref, rationale,
				source_endpoint, origin, origin_ref, acted_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb,$17)
		`, workspaceID, projectID, event.idempotencyKey, event.subjectType, event.subjectID,
			event.subjectRev, event.subjectDigest, event.decision, event.actorType, event.actorID,
			event.actorDisplay, event.policyRef, event.rationale, event.sourceEndpoint,
			event.origin, event.originRef, event.actedAt); err != nil {
			t.Fatalf("insert approval provenance fixture %s: %v", event.idempotencyKey, err)
		}
	}
	store := NewPostgresStore(db.New(pool), pool, pool, nil)
	bundle, err := store.ExportProject(ctx, exportPGUUID(t, workspaceID), exportPGUUID(t, projectID))
	if err != nil {
		t.Fatalf("export project: %v", err)
	}
	if bundle.SchemaVersion != SchemaVersionV2 {
		t.Fatalf("schema version = %q, want %q", bundle.SchemaVersion, SchemaVersionV2)
	}
	if len(bundle.SpecificationRevisions) != 2 {
		t.Fatalf("specification revision count = %d, want 2", len(bundle.SpecificationRevisions))
	}
	if bundle.SpecificationRevisions[0].ID != initialRevisionID || bundle.SpecificationRevisions[0].Revision != 1 || bundle.SpecificationRevisions[0].SourceKind != "project_bootstrap" || bundle.SpecificationRevisions[0].SourceRef == nil || *bundle.SpecificationRevisions[0].SourceRef != "initial-specification" {
		t.Fatalf("first specification revision = %#v", bundle.SpecificationRevisions[0])
	}
	if bundle.SpecificationRevisions[1].ID != currentRevisionID || bundle.SpecificationRevisions[1].Revision != 2 || bundle.SpecificationRevisions[1].SourceKind != "change_request" || bundle.SpecificationRevisions[1].SourceRef == nil || *bundle.SpecificationRevisions[1].SourceRef != "change:approval-provenance" {
		t.Fatalf("second specification revision = %#v", bundle.SpecificationRevisions[1])
	}
	if !jsonValuesEqual(t, bundle.SpecificationRevisions[1].Content, json.RawMessage(currentSpecification)) {
		t.Fatal("current specification revision content changed during export")
	}
	if bundle.Bootstrap == nil || bundle.Bootstrap.Plan == nil || bundle.Bootstrap.Plan.Specification == nil {
		t.Fatal("export is missing the current project specification")
	}
	current := bundle.Bootstrap.Plan.Specification
	if current.Goal != "Preserve project intent" || current.Summary != "Current authoritative intent" || len(current.Requirements) != 2 || current.Requirements[1].ID != "REQ-2" {
		t.Fatalf("current project specification = %#v", current)
	}
	if len(bundle.Bootstrap.Plan.Nodes) != 1 || len(bundle.Bootstrap.Plan.Nodes[0].RequirementIDs) != 2 || bundle.Bootstrap.Plan.Nodes[0].RequirementIDs[0] != "REQ-1" || bundle.Bootstrap.Plan.Nodes[0].RequirementIDs[1] != "REQ-2" {
		t.Fatalf("plan node requirement IDs = %#v", bundle.Bootstrap.Plan.Nodes)
	}
	if len(bundle.Approvals) != 3 {
		t.Fatalf("approval count = %d, want 3", len(bundle.Approvals))
	}
	for index, want := range []string{"approval-imported-human", "approval-local-human", "approval-system-policy"} {
		if got := bundle.Approvals[index].IdempotencyKey; got != want {
			t.Fatalf("approval[%d] idempotency key = %q, want %q", index, got, want)
		}
	}
	imported, local, system := bundle.Approvals[0], bundle.Approvals[1], bundle.Approvals[2]
	if imported.Origin != "imported" || imported.ActorType != "human" || imported.ActorID != nil || imported.ActorDisplay != "Imported reviewer" || imported.SourceEndpoint != "/imports/project-bundle" || imported.SubjectID != initialRevisionID || imported.SubjectRevision == nil || *imported.SubjectRevision != 1 || imported.SubjectDigest != initialDigest {
		t.Fatalf("imported approval provenance = %#v", imported)
	}
	if !jsonValuesEqual(t, imported.OriginRef, json.RawMessage(`{"source_bundle":"archive-42","source_approval_id":"review-1"}`)) {
		t.Fatal("imported approval origin_ref changed during export")
	}
	if local.Origin != "local" || local.ActorType != "human" || local.ActorID == nil || *local.ActorID != userID || local.Rationale != "Reviewed current intent" || local.SubjectDigest != currentDigest {
		t.Fatalf("local approval provenance = %#v", local)
	}
	if !jsonValuesEqual(t, local.OriginRef, json.RawMessage(`{"request_id":"local-approval-1"}`)) {
		t.Fatal("local approval origin_ref changed during export")
	}
	if system.ActorType != "system" || system.PolicyRef == nil || *system.PolicyRef != "autonomous-plan-policy-v4" || system.ActorID != nil || system.SubjectID != planID || system.Rationale != "Plan meets configured policy" {
		t.Fatalf("system approval provenance = %#v", system)
	}
	if !jsonValuesEqual(t, system.OriginRef, json.RawMessage(`{"policy_version":"4"}`)) {
		t.Fatal("system approval origin_ref changed during export")
	}

	fixture.Issue(t, "Malformed portable comments", testutil.Cols{
		"project_id": projectID,
		"metadata":   testutil.Raw(`'{"_multica_portable_comments":"{"}'::jsonb`),
	})
	if _, err := store.ExportProject(ctx, exportPGUUID(t, workspaceID), exportPGUUID(t, projectID)); err == nil || !strings.Contains(err.Error(), "decode comment provenance") {
		t.Fatalf("export with malformed stored comment provenance error = %v, want a decode error", err)
	}
	t.Log("PROJECT_BUNDLE_APPROVAL_EXPORT_GATE_PASS")
}

func exportPGUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse fixture UUID %q: %v", value, err)
	}
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}
}
