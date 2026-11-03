package projectbundle

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	projectorchestration "github.com/multica-ai/multica/server/internal/projectorchestration"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestPortableApprovalRoundTripPreservesSourceFields(t *testing.T) {
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
		t.Fatalf("begin rollback-only round-trip fixture: %v", err)
	}
	defer tx.Rollback(ctx)
	fixtureID := uuid.NewString()
	fixtureName := "Portable approval round-trip " + fixtureID
	var workspaceID, userID pgtype.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO "user"(name,email) VALUES($1,$2) RETURNING id`, fixtureName, "approval-roundtrip-"+fixtureID+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("insert round-trip fixture user: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO workspace(name,slug,description,issue_prefix) VALUES($1,$2,'','ART') RETURNING id`, fixtureName, "approval-roundtrip-"+fixtureID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert round-trip fixture workspace: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("insert round-trip fixture owner: %v", err)
	}

	oldSpecification := PortableSpecification{
		Goal: "Archived goal stored outside JSON", Summary: "First immutable revision",
		Requirements:              []PortableSpecificationRequirement{{ID: "REQ-OLD", Text: "Retain original intent", Rationale: "Keep the source history", Source: "brief"}},
		NonFunctionalRequirements: []string{}, Constraints: []string{"Do not rewrite history"}, NonGoals: []string{}, DefinitionOfDone: []string{}, AcceptanceExpectations: []string{},
	}
	currentSpecification := PortableSpecification{
		Goal: "Current goal stored in the database column", Summary: "Current immutable revision",
		Requirements:              []PortableSpecificationRequirement{{ID: "REQ-CURRENT", Text: "Preserve requirement IDs", Rationale: "Round-trip without losing links", Source: "brief"}},
		NonFunctionalRequirements: []string{"Approval provenance stays attributable"}, Constraints: []string{}, NonGoals: []string{}, DefinitionOfDone: []string{"Source approvals round-trip"}, AcceptanceExpectations: []string{"Requirement identifiers are stable"},
	}
	oldContent, err := portableSpecificationContent(oldSpecification)
	if err != nil {
		t.Fatalf("marshal archived specification content: %v", err)
	}
	currentContent, err := portableSpecificationContent(currentSpecification)
	if err != nil {
		t.Fatalf("marshal current specification content: %v", err)
	}
	oldDigest := portableRoundTripSpecificationDigest(t, tx, oldSpecification.Goal, oldContent)
	currentDigest := portableRoundTripSpecificationDigest(t, tx, currentSpecification.Goal, currentContent)
	oldID, currentID := uuid.NewString(), uuid.NewString()
	oldSourceRef, currentSourceRef := "source-old-"+fixtureID, "source-current-"+fixtureID
	oldRevision, currentRevision := int64(1), int64(2)
	oldCreatedAt, currentCreatedAt := "2026-10-18T08:00:00Z", "2026-10-19T08:00:00Z"
	actorID := uuidString(userID)
	approvals := []PortableApproval{
		{
			ID: uuid.NewString(), IdempotencyKey: "source-imported-event:" + fixtureID,
			SubjectType: "manual_issue", SubjectID: "source-issue-" + fixtureID,
			SubjectDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Decision:      "rejected", ActorType: "human", ActorDisplay: "Imported source reviewer",
			Rationale: "The source event remains provenance", SourceEndpoint: "/source/reviews/issue",
			Origin: "imported", OriginRef: json.RawMessage(`{"origin_ref":"archive:77"}`), ActedAt: "2026-10-20T08:00:00Z",
		},
		{
			ID: uuid.NewString(), IdempotencyKey: "portable-import:source-imported-" + fixtureID,
			SubjectType: "manual_issue", SubjectID: "source-imported-issue-" + fixtureID,
			SubjectDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			Decision:      "approved", ActorType: "human", ActorDisplay: "Imported reviewer without local actor",
			Rationale: "Preserve the raw imported approval reference", SourceEndpoint: "/source/reviews/imported-issue",
			Origin: "imported", OriginRef: json.RawMessage(`{"source_archive":"archive:88"}`), ActedAt: "2026-10-20T12:00:00Z",
		},
		{
			ID: uuid.NewString(), IdempotencyKey: "source-specification-approval:" + fixtureID,
			SubjectType: "specification_revision", SubjectID: currentID,
			SubjectRevision: &currentRevision, SubjectDigest: currentDigest,
			Decision: "approved", ActorType: "human", ActorID: &actorID,
			ActorDisplay: "Source workspace owner", Rationale: "Reviewed the current source specification",
			SourceEndpoint: "/source/projects/specification/approve", Origin: "local",
			OriginRef: json.RawMessage(`{"review_request":"req-42","review_round":3}`), ActedAt: "2026-10-21T09:00:00Z",
		},
		{
			ID: uuid.NewString(), IdempotencyKey: "source-plan-policy:" + fixtureID,
			SubjectType: "plan", SubjectID: "source-plan-" + fixtureID,
			SubjectDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Decision:      "approved", ActorType: "system", ActorDisplay: "Source policy engine",
			PolicyRef: stringPointer("portable-plan-policy:v7"), Rationale: "Source plan satisfied policy",
			SourceEndpoint: "/source/system/plan-approval", Origin: "local",
			OriginRef: json.RawMessage(`{"policy_version":7}`), ActedAt: "2026-10-22T10:30:00Z",
		},
	}
	plan := PortablePlan{
		Goal:          currentSpecification.Goal,
		Requirements:  []PlanRequirement{{ID: "REQ-CURRENT", Text: "Preserve requirement IDs", Rationale: "Round-trip without losing links"}},
		Specification: &currentSpecification,
		Nodes:         []PlanNode{{Key: "portable-task", Title: "Preserve requirement links", Kind: "implementation", RequirementIDs: []string{"REQ-CURRENT"}, AcceptanceCriteria: []string{"The imported node keeps its source requirement ID."}}},
		Edges:         []PlanEdge{},
	}
	taskStatus := "todo"
	initial := Bundle{
		SchemaVersion: SchemaVersionV2,
		Project:       PortableProject{Title: "Approval round-trip source " + fixtureID},
		Tasks:         []PortableTask{{Key: "portable-task", Title: "Preserve requirement links", Status: &taskStatus}},
		Bootstrap:     &PortableBootstrap{Plan: &plan},
		Approvals:     approvals,
		SpecificationRevisions: []PortableSpecificationRevision{
			{ID: oldID, Revision: oldRevision, SourceKind: "planner", SourceRef: &oldSourceRef, Goal: oldSpecification.Goal, Content: oldContent, Digest: oldDigest, CreatedAt: oldCreatedAt},
			{ID: currentID, Revision: currentRevision, SourceKind: "change_request", SourceRef: &currentSourceRef, Goal: currentSpecification.Goal, Content: currentContent, Digest: currentDigest, CreatedAt: currentCreatedAt},
		},
	}
	if err := ValidateBundleForImport(initial); err != nil {
		t.Fatalf("validate round-trip source bundle: %v", err)
	}

	store := &PostgresStore{}
	firstImport, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, nil, initial, ApplyOptions{})
	if err != nil {
		t.Fatalf("create first imported project: %v", err)
	}
	var firstProjectID pgtype.UUID
	if err := firstProjectID.Scan(firstImport.ProjectID); err != nil {
		t.Fatalf("parse first imported project ID: %v", err)
	}
	firstExport, err := exportProject(ctx, tx, workspaceID, firstProjectID)
	if err != nil {
		t.Fatalf("export first imported project: %v", err)
	}
	if len(firstExport.Approvals) != len(approvals) {
		t.Fatalf("first export approval count = %d, want %d", len(firstExport.Approvals), len(approvals))
	}
	for index := range approvals {
		if !portableApprovalRoundTripEqual(firstExport.Approvals[index], approvals[index]) {
			t.Fatalf("first export approval[%d] did not preserve source fields/order: got %#v, want %#v", index, firstExport.Approvals[index], approvals[index])
		}
	}
	assertRoundTripSpecificationHistory(t, firstExport, initial)

	secondImport, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, nil, firstExport, ApplyOptions{})
	if err != nil {
		t.Fatalf("import first export into second project: %v", err)
	}
	var secondProjectID pgtype.UUID
	if err := secondProjectID.Scan(secondImport.ProjectID); err != nil {
		t.Fatalf("parse second imported project ID: %v", err)
	}
	var importedCount, importedWithoutActor int
	if err := tx.QueryRow(ctx, `
		SELECT count(*),count(*) FILTER (WHERE actor_id IS NULL)
		FROM autonomous_project_approval_event WHERE workspace_id=$1 AND project_id=$2 AND origin='imported'
	`, workspaceID, secondProjectID).Scan(&importedCount, &importedWithoutActor); err != nil {
		t.Fatalf("check imported approvals remain provenance: %v", err)
	}
	if importedCount != len(approvals) || importedWithoutActor != len(approvals) {
		t.Fatalf("second project imported approval rows=%d with NULL actors=%d; want %d provenance rows", importedCount, importedWithoutActor, len(approvals))
	}
	var importedSpecificationID pgtype.UUID
	var importedSpecificationRevision int64
	var specificationDigest string
	if err := tx.QueryRow(ctx, `
		SELECT h.specification_revision_id,r.revision,autonomous_project_specification_digest(r.id)
		FROM autonomous_project_specification_head h
		JOIN autonomous_project_specification_revision r ON r.id=h.specification_revision_id
		WHERE h.workspace_id=$1 AND h.project_id=$2
	`, workspaceID, secondProjectID).Scan(&importedSpecificationID, &importedSpecificationRevision, &specificationDigest); err != nil {
		t.Fatalf("read imported specification adoption target: %v", err)
	}
	var specApproved bool
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_specification_approved($1)`, importedSpecificationID).Scan(&specApproved); err != nil {
		t.Fatalf("check imported approval did not grant local authority: %v", err)
	}
	if specApproved {
		t.Fatal("imported approvals granted local specification authority")
	}
	var planID pgtype.UUID
	var planRevision int64
	var planDigest string
	if err := tx.QueryRow(ctx, `
		SELECT id,revision,plan_digest FROM autonomous_project_plan
		WHERE workspace_id=$1 AND project_id=$2 ORDER BY revision DESC LIMIT 1
	`, workspaceID, secondProjectID).Scan(&planID, &planRevision, &planDigest); err != nil {
		t.Fatalf("read imported plan for adoption: %v", err)
	}
	var actorUUID pgtype.UUID
	if err := actorUUID.Scan(uuidString(userID)); err != nil {
		t.Fatalf("parse local adoption actor ID: %v", err)
	}
	originRef, err := json.Marshal(map[string]string{
		"specification_revision_id": uuidString(importedSpecificationID),
		"specification_digest":      specificationDigest,
		"plan_id":                   uuidString(planID),
		"plan_digest":               planDigest,
	})
	if err != nil {
		t.Fatalf("marshal local adoption provenance: %v", err)
	}
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID: workspaceID, ProjectID: secondProjectID,
		IdempotencyKey: "import-adoption-spec:" + uuidString(planID) + ":" + specificationDigest,
		SubjectType:    "specification_revision", SubjectID: uuidString(importedSpecificationID),
		SubjectRevision: pgtype.Int8{Int64: importedSpecificationRevision, Valid: true}, SubjectDigest: specificationDigest,
		Decision: "approved", ActorType: "human", ActorID: actorUUID, ActorDisplay: fixtureName,
		Rationale:      "Approved the exact imported specification during adoption",
		SourceEndpoint: "/api/projects/:id/autonomous/import/adopt", Origin: "local", OriginRef: originRef,
	}); err != nil {
		t.Fatalf("record local specification adoption approval: %v", err)
	}
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID: workspaceID, ProjectID: secondProjectID,
		IdempotencyKey: "import-adoption:" + uuidString(planID) + ":" + planDigest,
		SubjectType:    "import_adoption", SubjectID: uuidString(planID),
		SubjectRevision: pgtype.Int8{Int64: planRevision, Valid: true}, SubjectDigest: planDigest,
		Decision: "approved", ActorType: "human", ActorID: actorUUID, ActorDisplay: fixtureName,
		Rationale:      "Adopted the imported plan after reviewing exact digests",
		SourceEndpoint: "/api/projects/:id/autonomous/import/adopt", Origin: "local", OriginRef: originRef,
	}); err != nil {
		t.Fatalf("record local plan adoption event: %v", err)
	}
	secondExport, err := exportProject(ctx, tx, workspaceID, secondProjectID)
	if err != nil {
		t.Fatalf("export adopted project: %v", err)
	}
	if len(secondExport.Approvals) != len(approvals)+2 {
		t.Fatalf("adopted export approval count = %d, want %d source plus two local adoption events", len(secondExport.Approvals), len(approvals)+2)
	}
	for index := range approvals {
		if !portableApprovalRoundTripEqual(secondExport.Approvals[index], approvals[index]) {
			t.Fatalf("adopted export source approval[%d] changed or moved: got %#v, want %#v", index, secondExport.Approvals[index], approvals[index])
		}
	}
	localAdoptionSubjects := make(map[string]int, 2)
	for _, approval := range secondExport.Approvals[len(approvals):] {
		if approval.Origin != "local" {
			t.Fatalf("adoption addition is not a local approval: %#v", approval)
		}
		localAdoptionSubjects[approval.SubjectType]++
	}
	if localAdoptionSubjects["specification_revision"] != 1 || localAdoptionSubjects["import_adoption"] != 1 {
		t.Fatalf("adoption additions do not contain one local specification approval and one import-adoption event: %#v", secondExport.Approvals[len(approvals):])
	}
	assertRoundTripSpecificationHistory(t, secondExport, initial)
	var finalImportedCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_approval_event WHERE workspace_id=$1 AND project_id=$2 AND origin='imported'`, workspaceID, secondProjectID).Scan(&finalImportedCount); err != nil {
		t.Fatalf("count imported provenance after adoption: %v", err)
	}
	if finalImportedCount != len(approvals) {
		t.Fatalf("local adoption changed imported provenance row count to %d, want %d", finalImportedCount, len(approvals))
	}
	t.Log("PROJECT_BUNDLE_APPROVAL_ROUND_TRIP_GATE_PASS")
}

func portableRoundTripSpecificationDigest(t *testing.T, q Queryer, goal string, content []byte) string {
	t.Helper()
	var digest string
	if err := q.QueryRow(context.Background(), `
		SELECT encode(digest(convert_to(jsonb_build_object('goal',$1::text,'specification',$2::jsonb)::text,'UTF8'),'sha256'),'hex')
	`, goal, string(content)).Scan(&digest); err != nil {
		t.Fatalf("digest round-trip specification: %v", err)
	}
	return digest
}

func portableApprovalRoundTripEqual(got, want PortableApproval) bool {
	return got.ID == want.ID && got.IdempotencyKey == want.IdempotencyKey &&
		got.SubjectType == want.SubjectType && got.SubjectID == want.SubjectID &&
		equalInt64Pointer(got.SubjectRevision, want.SubjectRevision) && got.SubjectDigest == want.SubjectDigest &&
		got.Decision == want.Decision && got.ActorType == want.ActorType && equalStringPointer(got.ActorID, want.ActorID) &&
		got.ActorDisplay == want.ActorDisplay && equalStringPointer(got.PolicyRef, want.PolicyRef) &&
		got.Rationale == want.Rationale && got.SourceEndpoint == want.SourceEndpoint && got.Origin == want.Origin &&
		equalJSON(got.OriginRef, want.OriginRef) && got.ActedAt == want.ActedAt
}

func assertRoundTripSpecificationHistory(t *testing.T, got, want Bundle) {
	t.Helper()
	if len(got.SpecificationRevisions) != len(want.SpecificationRevisions) {
		t.Fatalf("exported specification revision count = %d, want %d", len(got.SpecificationRevisions), len(want.SpecificationRevisions))
	}
	for index, expected := range want.SpecificationRevisions {
		actual := got.SpecificationRevisions[index]
		if actual.Goal != expected.Goal || actual.Digest != expected.Digest || !jsonDocumentsEquivalent(actual.Content, expected.Content) {
			t.Fatalf("specification revision[%d] lost Goal or immutable content: got %#v, want Goal=%q digest=%q content=%s", index, actual, expected.Goal, expected.Digest, expected.Content)
		}
	}
	if got.Bootstrap == nil || got.Bootstrap.Plan == nil || got.Bootstrap.Plan.Specification == nil {
		t.Fatal("export omitted the current project specification")
	}
	current := got.Bootstrap.Plan.Specification
	wantCurrent := want.SpecificationRevisions[len(want.SpecificationRevisions)-1]
	if current.Goal != wantCurrent.Goal || current.Summary != "Current immutable revision" || len(current.Requirements) != 1 || current.Requirements[0].ID != "REQ-CURRENT" {
		t.Fatalf("current specification did not preserve DB Goal and requirement IDs: %#v", current)
	}
	if got.Bootstrap.Plan.Goal != wantCurrent.Goal {
		t.Fatalf("plan goal = %q, want DB goal %q", got.Bootstrap.Plan.Goal, wantCurrent.Goal)
	}
	if len(got.Bootstrap.Plan.Nodes) != 1 || len(got.Bootstrap.Plan.Nodes[0].RequirementIDs) != 1 || got.Bootstrap.Plan.Nodes[0].RequirementIDs[0] != "REQ-CURRENT" {
		t.Fatalf("plan node requirement IDs = %#v, want [REQ-CURRENT]", got.Bootstrap.Plan.Nodes)
	}
}
