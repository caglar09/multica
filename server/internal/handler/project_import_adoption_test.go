package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectbundle"
)

func TestAdoptImportedProjectPlanRequiresLocalAdminAndApproval(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fixtureID := uuid.NewString()
	title := "Imported adoption gate " + fixtureID
	oldContent := json.RawMessage(`{"summary":"Old imported specification","requirements":[{"id":"REQ-OLD","text":"Preserve history","rationale":"Keep the original intent","source":"brief"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}`)
	currentContent := json.RawMessage(`{"summary":"Current imported specification","requirements":[{"id":"REQ-CURRENT","text":"Require local adoption","rationale":"Imported approval is provenance only","source":"brief"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}`)
	goal := "Import and locally adopt project intent"
	oldDigest := importedSpecificationDigest(t, goal, oldContent)
	currentDigest := importedSpecificationDigest(t, goal, currentContent)
	oldSourceID, currentSourceID := uuid.NewString(), uuid.NewString()
	oldSourceRef, currentSourceRef := "source-revision-old-"+fixtureID, "source-revision-current-"+fixtureID
	currentRevisionNumber := int64(2)
	sourceActorID := uuid.NewString()
	sourceApproval := projectbundle.PortableApproval{
		ID: uuid.NewString(), IdempotencyKey: "source-approval:" + fixtureID,
		SubjectType: "specification_revision", SubjectID: currentSourceID,
		SubjectRevision: &currentRevisionNumber, SubjectDigest: currentDigest,
		Decision: "approved", ActorType: "human", ActorID: &sourceActorID,
		ActorDisplay: "Source administrator", Rationale: "Approved in the source workspace",
		SourceEndpoint: "/api/projects/source/autonomous/spec/approve", Origin: "local",
		OriginRef: json.RawMessage(`{"source":"preserved"}`), ActedAt: "2026-10-21T10:11:12.123456789Z",
	}
	currentSpecification := projectbundle.PortableSpecification{
		Goal: goal, Summary: "Current imported specification",
		Requirements:              []projectbundle.PortableSpecificationRequirement{{ID: "REQ-CURRENT", Text: "Require local adoption", Rationale: "Imported approval is provenance only", Source: "brief"}},
		NonFunctionalRequirements: []string{}, Constraints: []string{}, NonGoals: []string{}, DefinitionOfDone: []string{}, AcceptanceExpectations: []string{},
	}
	taskStatus, taskCategory := "in_progress", "started"
	bundle := projectbundle.Bundle{
		SchemaVersion: projectbundle.SchemaVersionV2,
		Project:       projectbundle.PortableProject{Title: title},
		Tasks: []projectbundle.PortableTask{{
			Key: "portable-task-1", Title: "Require local adoption", Status: &taskStatus, StatusCategory: &taskCategory,
		}},
		Bootstrap: &projectbundle.PortableBootstrap{Plan: &projectbundle.PortablePlan{
			Goal:          goal,
			Requirements:  []projectbundle.PlanRequirement{{ID: "REQ-CURRENT", Text: "Require local adoption", Rationale: "Imported approval is provenance only"}},
			Specification: &currentSpecification,
			Nodes:         []projectbundle.PlanNode{{Key: "portable-task-1", Title: "Require local adoption", Kind: "implementation", RequirementIDs: []string{"REQ-CURRENT"}}},
		}},
		Approvals: []projectbundle.PortableApproval{sourceApproval},
		SpecificationRevisions: []projectbundle.PortableSpecificationRevision{
			{ID: oldSourceID, Revision: 1, SourceKind: "planner", SourceRef: &oldSourceRef, Goal: goal, Content: oldContent, Digest: oldDigest, CreatedAt: "2026-10-20T12:00:00Z"},
			{ID: currentSourceID, Revision: 2, SourceKind: "change_request", SourceRef: &currentSourceRef, Goal: goal, Content: currentContent, Digest: currentDigest, CreatedAt: "2026-10-21T12:00:00Z"},
		},
	}
	body, err := json.Marshal(map[string]any{"bundle": bundle})
	if err != nil {
		t.Fatalf("marshal imported bundle: %v", err)
	}
	importRequest := httptest.NewRequest(http.MethodPost, "/api/projects/import", strings.NewReader(string(body)))
	importRequest.Header.Set("Content-Type", "application/json")
	importRequest.Header.Set("X-User-ID", testUserID)
	importRequest.Header.Set("X-Workspace-ID", testWorkspaceID)
	importResponse := httptest.NewRecorder()
	testHandler.ImportProject(importResponse, importRequest)
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("import project status = %d, want 201: %s", importResponse.Code, importResponse.Body.String())
	}
	var imported struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(importResponse.Body.Bytes(), &imported); err != nil || imported.ProjectID == "" {
		t.Fatalf("decode imported project response %q: %v", importResponse.Body.String(), err)
	}
	t.Cleanup(func() { cleanupImportedAdoptionProjectFixture(t, imported.ProjectID) })

	var projectID, specificationID, planID, issueID pgtype.UUID
	if err := projectID.Scan(imported.ProjectID); err != nil {
		t.Fatalf("parse imported project ID: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT h.specification_revision_id,p.id
		FROM autonomous_project_specification_head h
		JOIN autonomous_project_plan p ON p.workspace_id=h.workspace_id AND p.project_id=h.project_id
		WHERE h.workspace_id=$1 AND h.project_id=$2 ORDER BY p.revision DESC LIMIT 1
	`, testWorkspaceID, projectID).Scan(&specificationID, &planID); err != nil {
		t.Fatalf("read imported adoption targets: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT id FROM issue WHERE project_id=$1 AND metadata->>'multica_portable_key'='portable-task-1'`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("read imported task issue: %v", err)
	}
	var specificationDigest, planDigest string
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, specificationID).Scan(&specificationDigest); err != nil {
		t.Fatalf("digest imported current specification: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_plan_digest($1) FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&planDigest); err != nil {
		t.Fatalf("digest imported plan: %v", err)
	}
	var importedApprovals, localApprovals int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE origin='imported'),count(*) FILTER (WHERE origin='local') FROM autonomous_project_approval_event WHERE project_id=$1`, projectID).Scan(&importedApprovals, &localApprovals); err != nil {
		t.Fatalf("count imported approval events: %v", err)
	}
	if importedApprovals != 1 || localApprovals != 0 {
		t.Fatalf("approval counts before adoption = imported %d, local %d; want 1 and 0", importedApprovals, localApprovals)
	}

	memberName := "Imported adoption non-admin " + fixtureID
	memberEmail := "imported-adoption-non-admin-" + fixtureID + "@example.com"
	var memberID pgtype.UUID
	if err := testPool.QueryRow(ctx, `INSERT INTO "user"(name,email) VALUES($1,$2) RETURNING id`, memberName, memberEmail).Scan(&memberID); err != nil {
		t.Fatalf("insert non-admin user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, testWorkspaceID, memberID); err != nil {
		t.Fatalf("insert non-admin workspace member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, memberID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, memberID)
	})

	callAdopt := func(actorID, specDigest, importedPlanDigest string) *httptest.ResponseRecorder {
		req := newRequest("POST", "/api/projects/"+imported.ProjectID+"/autonomous/import/adopt", map[string]string{
			"specification_digest": specDigest,
			"plan_digest":          importedPlanDigest,
		})
		req.Header.Set("X-User-ID", actorID)
		req = withURLParam(req, "id", imported.ProjectID)
		response := httptest.NewRecorder()
		testHandler.AdoptImportedProjectPlan(response, req)
		return response
	}
	response := callAdopt(uuidToString(memberID), specificationDigest, planDigest)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-admin adoption status = %d, want 403: %s", response.Code, response.Body.String())
	}
	response = callAdopt(testUserID, strings.Repeat("0", 64), planDigest)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale specification adoption status = %d, want 409: %s", response.Code, response.Body.String())
	}
	response = callAdopt(testUserID, specificationDigest, strings.Repeat("0", 64))
	if response.Code != http.StatusConflict {
		t.Fatalf("stale plan adoption status = %d, want 409: %s", response.Code, response.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_approval_event WHERE project_id=$1 AND origin='local'`, projectID).Scan(&localApprovals); err != nil {
		t.Fatalf("count local events after rejected adoption attempts: %v", err)
	}
	if localApprovals != 0 {
		t.Fatalf("stale or unauthorized adoption wrote %d local approval events", localApprovals)
	}

	response = callAdopt(testUserID, specificationDigest, planDigest)
	if response.Code != http.StatusOK {
		t.Fatalf("admin adoption status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var adopted struct {
		Adopted               bool   `json:"adopted"`
		SpecificationDigest   string `json:"specification_digest"`
		SpecificationRevision int64  `json:"specification_revision"`
		PlanDigest            string `json:"plan_digest"`
		PlanRevision          int64  `json:"plan_revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &adopted); err != nil {
		t.Fatalf("decode adoption result: %v", err)
	}
	if !adopted.Adopted || adopted.SpecificationDigest != specificationDigest || adopted.PlanDigest != planDigest || adopted.SpecificationRevision < 1 || adopted.PlanRevision < 1 {
		t.Fatalf("adoption response does not match exact imported digests: %+v", adopted)
	}
	var approved, executable bool
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_specification_approved($1)`, specificationID).Scan(&approved); err != nil {
		t.Fatalf("check local specification approval: %v", err)
	}
	if !approved {
		t.Fatal("local adoption did not approve the current imported specification revision")
	}
	var adoptionEventCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND subject_type='import_adoption'
		  AND subject_id=$3 AND subject_digest=$4 AND origin='local' AND actor_id=$5
		  AND origin_ref->>'specification_digest'=$6 AND origin_ref->>'plan_digest'=$4
	`, testWorkspaceID, projectID, uuidToString(planID), planDigest, testUserID, specificationDigest).Scan(&adoptionEventCount); err != nil {
		t.Fatalf("read local adoption event: %v", err)
	}
	if adoptionEventCount != 1 {
		t.Fatalf("matching local import_adoption events = %d, want 1", adoptionEventCount)
	}
	var planStatus string
	var approvedAt pgtype.Timestamptz
	var approvedBy pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT status,approved_at,approved_by,autonomous_project_plan_executable(id) FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&planStatus, &approvedAt, &approvedBy, &executable); err != nil {
		t.Fatalf("read plan execution state after adoption: %v", err)
	}
	if planStatus != "awaiting_approval" || approvedAt.Valid || approvedBy.Valid || executable {
		t.Fatalf("adoption changed plan execution state: status=%q approved_at=%t approved_by=%t executable=%t", planStatus, approvedAt.Valid, approvedBy.Valid, executable)
	}
	var importedTaskStatus string
	var taskAssigneeType pgtype.Text
	var taskAssigneeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT status,assignee_type,assignee_id FROM issue WHERE id=$1`, issueID).Scan(&importedTaskStatus, &taskAssigneeType, &taskAssigneeID); err != nil {
		t.Fatalf("read task state after adoption: %v", err)
	}
	if importedTaskStatus != "todo" || taskAssigneeType.Valid || taskAssigneeID.Valid {
		t.Fatalf("adoption changed task dispatch state: status=%q assignee=(%v,%v)", importedTaskStatus, taskAssigneeType, taskAssigneeID)
	}
	t.Log("PROJECT_IMPORT_ADOPTION_GATE_PASS")
}

func importedSpecificationDigest(t *testing.T, goal string, content json.RawMessage) string {
	t.Helper()
	var digest string
	if err := testPool.QueryRow(context.Background(), `
		SELECT encode(digest(convert_to(jsonb_build_object('goal',$1::text,'specification',$2::jsonb)::text,'UTF8'),'sha256'),'hex')
	`, goal, string(content)).Scan(&digest); err != nil {
		t.Fatalf("compute imported specification digest: %v", err)
	}
	return digest
}

func cleanupImportedAdoptionProjectFixture(t *testing.T, projectID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Errorf("begin imported adoption fixture cleanup: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('multica.approval_purge','on',true)`); err != nil {
		t.Errorf("enable approval event cleanup: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_approval_event WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete imported adoption approval events: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_plan_edge WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete imported adoption plan edges: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_plan_node WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete imported adoption plan nodes: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_plan WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete imported adoption plans: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_specification_head WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete imported adoption specification head: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_specification_revision WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete imported adoption specification history: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit imported adoption fixture history cleanup: %v", err)
		return
	}
	cleanupProjectPortabilityFixture(t, projectID)
}
