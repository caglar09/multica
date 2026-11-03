package projectbundle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	projectorchestration "github.com/multica-ai/multica/server/internal/projectorchestration"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestImportPortableApprovalHistoryIsProvenanceOnly(t *testing.T) {
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
	actorName := "Project provenance gate " + fixtureID
	actorEmail := "project-provenance-gate-" + fixtureID + "@example.com"
	var userID, workspaceID pgtype.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ($1,$2) RETURNING id`, actorName, actorEmail).Scan(&userID); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO workspace (name,slug,description,issue_prefix) VALUES ($1,$2,'','PAG') RETURNING id`, "Project provenance gate "+fixtureID, "project-provenance-gate-"+fixtureID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert fixture workspace: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("insert fixture owner: %v", err)
	}
	preview := &PortabilityPreview{}
	sourceProjectID, err := createProject(ctx, tx, workspaceID, userID, PortableProject{Title: "Source history " + fixtureID}, preview)
	if err != nil {
		t.Fatalf("create source project: %v", err)
	}

	oldSpecification := PortableSpecification{
		Goal: "Import and locally adopt project intent", Summary: "Initial imported intent.",
		Requirements:              []PortableSpecificationRequirement{{ID: "REQ-OLD", Text: "Keep a source history", Rationale: "Preserve provenance", Source: "brief"}},
		NonFunctionalRequirements: []string{}, Constraints: []string{}, NonGoals: []string{}, DefinitionOfDone: []string{}, AcceptanceExpectations: []string{},
	}
	currentSpecification := PortableSpecification{
		Goal: "Import and locally adopt project intent", Summary: "Current imported intent.",
		Requirements:              []PortableSpecificationRequirement{{ID: "REQ-CURRENT", Text: "Require local adoption", Rationale: "Imported approval is not local authority", Source: "brief"}},
		NonFunctionalRequirements: []string{}, Constraints: []string{}, NonGoals: []string{}, DefinitionOfDone: []string{}, AcceptanceExpectations: []string{},
	}
	contentOne, err := portableSpecificationContent(oldSpecification)
	if err != nil {
		t.Fatalf("marshal first specification history: %v", err)
	}
	contentTwo, err := portableSpecificationContent(currentSpecification)
	if err != nil {
		t.Fatalf("marshal current specification history: %v", err)
	}
	sourceRevisionIDs := make([]string, 0, 2)
	sourceRevisions := make([]PortableSpecificationRevision, 0, 2)
	for index, item := range []struct {
		specification PortableSpecification
		content       []byte
		kind          string
	}{{oldSpecification, contentOne, "planner"}, {currentSpecification, contentTwo, "change_request"}} {
		var sourceRevisionID string
		sourceRef := "source-history-" + fixtureID + "-" + item.kind
		if err := tx.QueryRow(ctx, `
			INSERT INTO autonomous_project_specification_revision(
				workspace_id,project_id,revision,source_kind,source_ref,goal,specification
			) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb) RETURNING id::text
		`, workspaceID, sourceProjectID, index+1, item.kind, sourceRef, item.specification.Goal, item.content).Scan(&sourceRevisionID); err != nil {
			t.Fatalf("insert source specification revision %d: %v", index+1, err)
		}
		var digest string
		if err := tx.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, sourceRevisionID).Scan(&digest); err != nil {
			t.Fatalf("digest source specification revision %d: %v", index+1, err)
		}
		sourceRevisionIDs = append(sourceRevisionIDs, sourceRevisionID)
		sourceRefValue := sourceRef
		sourceRevisions = append(sourceRevisions, PortableSpecificationRevision{
			ID: sourceRevisionID, Revision: int64(index + 1), SourceKind: item.kind,
			SourceRef: &sourceRefValue, Goal: item.specification.Goal, Content: item.content,
			Digest: digest, CreatedAt: "2026-10-20T12:00:00Z",
		})
	}
	var sourceSpecificationDigest string
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, sourceRevisionIDs[1]).Scan(&sourceSpecificationDigest); err != nil {
		t.Fatalf("read current source specification digest: %v", err)
	}
	sourceApprovalID := uuid.NewString()
	sourceActorID := uuidString(userID)
	sourceSubjectRevision := int64(2)
	actedAt := "2026-10-21T10:11:12.123456789Z"
	rawSourceOriginRef := json.RawMessage(`{"origin_ref_key":"source-review-42"}`)
	sourceApproval := PortableApproval{
		ID: sourceApprovalID, IdempotencyKey: "source-spec-approval:" + fixtureID,
		SubjectType: "specification_revision", SubjectID: sourceRevisionIDs[1],
		SubjectRevision: &sourceSubjectRevision, SubjectDigest: sourceSpecificationDigest,
		Decision: "approved", ActorType: "human", ActorID: &sourceActorID,
		ActorDisplay: "Source workspace admin", Rationale: "Approved in the source workspace",
		SourceEndpoint: "/api/projects/source/autonomous/spec/approve", Origin: "local",
		OriginRef: rawSourceOriginRef, ActedAt: actedAt,
	}
	taskStatus, taskCategory := "in_progress", "started"
	plan := PortablePlan{
		Goal:          currentSpecification.Goal,
		Requirements:  []PlanRequirement{{ID: "REQ-CURRENT", Text: "Require local adoption", Rationale: "Imported approval is not local authority"}},
		Specification: &currentSpecification,
		Nodes:         []PlanNode{{Key: "portable-task-1", Title: "Require local adoption", Kind: "implementation", RequirementIDs: []string{"REQ-CURRENT"}, AcceptanceCriteria: []string{"Local task state is retained after adoption."}}},
		Edges:         []PlanEdge{},
	}
	bundle := Bundle{
		SchemaVersion: SchemaVersionV2,
		Project:       PortableProject{Title: "Imported provenance " + fixtureID},
		Tasks: []PortableTask{{
			Key: "portable-task-1", Title: "Require local adoption",
			Status: &taskStatus, StatusCategory: &taskCategory,
			Assignee: &PortableActorRef{Type: "member", Name: actorName, Email: actorEmail},
		}},
		Bootstrap:              &PortableBootstrap{Plan: &plan},
		Approvals:              []PortableApproval{sourceApproval},
		SpecificationRevisions: sourceRevisions,
	}
	if err := ValidateBundleForImport(bundle); err != nil {
		t.Fatalf("validate portable provenance bundle: %v", err)
	}
	store := &PostgresStore{}
	result, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, nil, bundle, ApplyOptions{})
	if err != nil {
		t.Fatalf("import provenance bundle: %v", err)
	}
	var targetProjectID pgtype.UUID
	if err := targetProjectID.Scan(result.ProjectID); err != nil {
		t.Fatalf("parse imported project id: %v", err)
	}
	var issueID, planID, importedSpecificationID pgtype.UUID
	var status string
	var assigneeType pgtype.Text
	var assigneeID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id,status,assignee_type,assignee_id FROM issue
		WHERE workspace_id=$1 AND project_id=$2 AND metadata->>'multica_portable_key'='portable-task-1'
	`, workspaceID, targetProjectID).Scan(&issueID, &status, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("read imported task: %v", err)
	}
	if status != "todo" || assigneeType.Valid || assigneeID.Valid {
		t.Fatalf("imported task = status %q, assignee (%v,%v); want todo and unassigned", status, assigneeType, assigneeID)
	}
	var importedRevisionCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_specification_revision
		WHERE workspace_id=$1 AND project_id=$2 AND source_kind='project_bootstrap' AND source_ref LIKE 'portable-import:v1:%'
	`, workspaceID, targetProjectID).Scan(&importedRevisionCount); err != nil {
		t.Fatalf("count imported specification revisions: %v", err)
	}
	if importedRevisionCount != 2 {
		t.Fatalf("imported specification revision count = %d, want 2", importedRevisionCount)
	}
	if err := tx.QueryRow(ctx, `
		SELECT h.specification_revision_id,p.id
		FROM autonomous_project_specification_head h
		JOIN autonomous_project_plan p ON p.workspace_id=h.workspace_id AND p.project_id=h.project_id
		WHERE h.workspace_id=$1 AND h.project_id=$2
		ORDER BY p.revision DESC LIMIT 1
	`, workspaceID, targetProjectID).Scan(&importedSpecificationID, &planID); err != nil {
		t.Fatalf("read imported specification head and plan: %v", err)
	}
	var importedSourceRef string
	if err := tx.QueryRow(ctx, `
		SELECT source_ref FROM autonomous_project_specification_revision WHERE id=$1
	`, importedSpecificationID).Scan(&importedSourceRef); err != nil {
		t.Fatalf("read imported specification source ref: %v", err)
	}
	metadataJSON, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(importedSourceRef, portableSpecificationImportPrefix))
	if err != nil {
		t.Fatalf("decode imported specification source provenance: %v", err)
	}
	var importedSource portableSpecificationImportSource
	if err := json.Unmarshal(metadataJSON, &importedSource); err != nil {
		t.Fatalf("unmarshal imported specification source provenance: %v", err)
	}
	if importedSource.ID != sourceRevisionIDs[1] || importedSource.Revision != 2 || importedSource.SourceKind != "change_request" || importedSource.Digest != sourceSpecificationDigest {
		t.Fatalf("current imported specification provenance = %+v", importedSource)
	}
	if importedSource.SourceRef == nil || *importedSource.SourceRef != "source-history-"+fixtureID+"-change_request" {
		t.Fatalf("imported source reference = %v, want original source ref", importedSource.SourceRef)
	}
	var linkedSpecificationID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT specification_revision_id FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&linkedSpecificationID); err != nil {
		t.Fatalf("read plan specification link: %v", err)
	}
	if importedSpecificationID != linkedSpecificationID {
		t.Fatalf("imported plan specification = %v, want current imported history %v", linkedSpecificationID, importedSpecificationID)
	}
	var importedCurrentDigest string
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, importedSpecificationID).Scan(&importedCurrentDigest); err != nil {
		t.Fatalf("digest imported current specification: %v", err)
	}
	if importedCurrentDigest != sourceSpecificationDigest {
		t.Fatalf("imported specification digest = %s, want source digest %s", importedCurrentDigest, sourceSpecificationDigest)
	}
	var localSpecificationApproved, planExecutable bool
	var planStatus string
	var approvedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_specification_approved($1)`, importedSpecificationID).Scan(&localSpecificationApproved); err != nil {
		t.Fatalf("check imported specification approval predicate: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT status,approved_at,autonomous_project_plan_executable(id) FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&planStatus, &approvedAt, &planExecutable); err != nil {
		t.Fatalf("check imported plan execution gate: %v", err)
	}
	if localSpecificationApproved || planStatus != "awaiting_approval" || approvedAt.Valid || planExecutable {
		t.Fatalf("imported approval granted authority: spec approved=%t plan status=%q approved_at=%t executable=%t", localSpecificationApproved, planStatus, approvedAt.Valid, planExecutable)
	}
	var importedApprovalID string
	var importedIdempotencyKey, importedOrigin, importedSourceEndpoint, importedActorDisplay string
	var importedActorID pgtype.UUID
	var importedOriginRef []byte
	if err := tx.QueryRow(ctx, `
		SELECT id::text,idempotency_key,origin,actor_id,source_endpoint,actor_display,origin_ref
		FROM autonomous_project_approval_event WHERE workspace_id=$1 AND project_id=$2 AND origin='imported'
	`, workspaceID, targetProjectID).Scan(&importedApprovalID, &importedIdempotencyKey, &importedOrigin, &importedActorID, &importedSourceEndpoint, &importedActorDisplay, &importedOriginRef); err != nil {
		t.Fatalf("read imported approval provenance event: %v", err)
	}
	if importedApprovalID == sourceApproval.ID || importedOrigin != "imported" || importedActorID.Valid ||
		importedIdempotencyKey != "portable-import:"+sourceApproval.ID || importedSourceEndpoint != sourceApproval.SourceEndpoint || importedActorDisplay != sourceApproval.ActorDisplay {
		t.Fatalf("imported approval row did not retain safe provenance: id=%q origin=%q actor_id=%v key=%q endpoint=%q actor_display=%q", importedApprovalID, importedOrigin, importedActorID, importedIdempotencyKey, importedSourceEndpoint, importedActorDisplay)
	}
	var importedWrapper struct {
		Version        int              `json:"version"`
		ImportedVia    string           `json:"imported_via"`
		SourceApproval PortableApproval `json:"source_approval"`
	}
	if err := json.Unmarshal(importedOriginRef, &importedWrapper); err != nil {
		t.Fatalf("decode imported approval provenance wrapper: %v", err)
	}
	if importedWrapper.Version != 1 || importedWrapper.ImportedVia != "/api/projects/import" ||
		importedWrapper.SourceApproval.ID != sourceApproval.ID ||
		importedWrapper.SourceApproval.IdempotencyKey != sourceApproval.IdempotencyKey ||
		importedWrapper.SourceApproval.SubjectType != sourceApproval.SubjectType ||
		importedWrapper.SourceApproval.SubjectID != sourceApproval.SubjectID ||
		importedWrapper.SourceApproval.SubjectRevision == nil || *importedWrapper.SourceApproval.SubjectRevision != *sourceApproval.SubjectRevision ||
		importedWrapper.SourceApproval.SubjectDigest != sourceApproval.SubjectDigest ||
		importedWrapper.SourceApproval.Decision != sourceApproval.Decision ||
		importedWrapper.SourceApproval.ActorType != sourceApproval.ActorType ||
		importedWrapper.SourceApproval.ActorID == nil || *importedWrapper.SourceApproval.ActorID != sourceActorID ||
		importedWrapper.SourceApproval.ActorDisplay != sourceApproval.ActorDisplay ||
		importedWrapper.SourceApproval.SourceEndpoint != sourceApproval.SourceEndpoint ||
		importedWrapper.SourceApproval.Origin != sourceApproval.Origin ||
		importedWrapper.SourceApproval.Rationale != sourceApproval.Rationale ||
		importedWrapper.SourceApproval.ActedAt != sourceApproval.ActedAt ||
		!jsonDocumentsEquivalent(importedWrapper.SourceApproval.OriginRef, rawSourceOriginRef) {
		t.Fatalf("source approval fields were not preserved in origin_ref: %#v", importedWrapper)
	}
	var nodeRequirementIDsRaw []byte
	if err := tx.QueryRow(ctx, `SELECT requirement_ids FROM autonomous_project_plan_node WHERE plan_id=$1 AND node_key='portable-task-1'`, planID).Scan(&nodeRequirementIDsRaw); err != nil {
		t.Fatalf("read imported plan-node requirement IDs: %v", err)
	}
	var nodeRequirementIDs []string
	if err := json.Unmarshal(nodeRequirementIDsRaw, &nodeRequirementIDs); err != nil || len(nodeRequirementIDs) != 1 || nodeRequirementIDs[0] != "REQ-CURRENT" {
		t.Fatalf("imported plan node requirement IDs = %#v, err %v", nodeRequirementIDs, err)
	}

	// A legacy dispatchable row must still be reset before adoption, including
	// when the incoming task values produce no task update.
	if _, err := tx.Exec(ctx, `UPDATE issue SET status='in_progress',assignee_type='member',assignee_id=$2 WHERE id=$1`, issueID, userID); err != nil {
		t.Fatalf("seed legacy dispatchable task state: %v", err)
	}
	targetProjectCopy := targetProjectID
	if _, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, &targetProjectCopy, bundle, ApplyOptions{}); err != nil {
		t.Fatalf("pre-adoption reimport: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT status,assignee_type,assignee_id FROM issue WHERE id=$1`, issueID).Scan(&status, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("read pre-adoption reimported task: %v", err)
	}
	if status != "todo" || assigneeType.Valid || assigneeID.Valid {
		t.Fatalf("pre-adoption reimport task = status %q, assignee (%v,%v); want todo and unassigned", status, assigneeType, assigneeID)
	}

	var localSpecDigest, localPlanDigest string
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, importedSpecificationID).Scan(&localSpecDigest); err != nil {
		t.Fatalf("digest adopted specification: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_plan_digest($1) FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&localPlanDigest); err != nil {
		t.Fatalf("digest adopted plan: %v", err)
	}
	var localSpecRevision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM autonomous_project_specification_revision WHERE id=$1`, importedSpecificationID).Scan(&localSpecRevision); err != nil {
		t.Fatalf("read adopted specification revision: %v", err)
	}
	var localPlanRevision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&localPlanRevision); err != nil {
		t.Fatalf("read adopted plan revision: %v", err)
	}
	originRef, err := json.Marshal(map[string]string{"specification_digest": localSpecDigest, "plan_digest": localPlanDigest})
	if err != nil {
		t.Fatalf("marshal local adoption provenance: %v", err)
	}
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID: workspaceID, ProjectID: targetProjectID,
		IdempotencyKey: "test-local-import-spec:" + uuidString(planID),
		SubjectType:    "specification_revision", SubjectID: uuidString(importedSpecificationID),
		SubjectRevision: pgtype.Int8{Int64: localSpecRevision, Valid: true}, SubjectDigest: localSpecDigest,
		Decision: "approved", ActorType: "human", ActorID: userID, ActorDisplay: actorName,
		Rationale: "Locally adopted exact imported specification", SourceEndpoint: "/test/import/adopt", Origin: "local", OriginRef: originRef,
	}); err != nil {
		t.Fatalf("record local specification approval fixture: %v", err)
	}
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID: workspaceID, ProjectID: targetProjectID,
		IdempotencyKey: "test-local-import-adoption:" + uuidString(planID),
		SubjectType:    "import_adoption", SubjectID: uuidString(planID),
		SubjectRevision: pgtype.Int8{Int64: localPlanRevision, Valid: true}, SubjectDigest: localPlanDigest,
		Decision: "approved", ActorType: "human", ActorID: userID, ActorDisplay: actorName,
		Rationale: "Locally adopted exact imported plan", SourceEndpoint: "/test/import/adopt", Origin: "local", OriginRef: originRef,
	}); err != nil {
		t.Fatalf("record local import adoption fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE issue SET status='in_progress',assignee_type='member',assignee_id=$2 WHERE id=$1`, issueID, userID); err != nil {
		t.Fatalf("set locally adopted task state: %v", err)
	}
	if _, err := store.applyInTransaction(ctx, tx, db.New(tx), workspaceID, userID, &targetProjectCopy, bundle, ApplyOptions{}); err != nil {
		t.Fatalf("post-adoption reimport: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT status,assignee_type,assignee_id FROM issue WHERE id=$1`, issueID).Scan(&status, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("read post-adoption task state: %v", err)
	}
	if status != "in_progress" || !assigneeType.Valid || assigneeType.String != "member" || !assigneeID.Valid || assigneeID != userID {
		t.Fatalf("post-adoption reimport task = status %q, assignee (%v,%v); want local in_progress/member state", status, assigneeType, assigneeID)
	}
	if err := tx.QueryRow(ctx, `SELECT status,approved_at,autonomous_project_plan_executable(id) FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&planStatus, &approvedAt, &planExecutable); err != nil {
		t.Fatalf("recheck plan after adoption: %v", err)
	}
	if planStatus != "awaiting_approval" || approvedAt.Valid || planExecutable {
		t.Fatalf("adopted plan was approved or activated: status=%q approved_at=%t executable=%t", planStatus, approvedAt.Valid, planExecutable)
	}

	t.Log("PROJECT_IMPORT_PROVENANCE_GATE_PASS")
}
