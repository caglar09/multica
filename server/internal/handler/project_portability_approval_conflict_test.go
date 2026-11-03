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

func TestSyncProjectRejectsApprovalProvenanceConflict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fixtureID := uuid.NewString()
	goal := "Preserve imported approval provenance"
	content := json.RawMessage(`{"summary":"Imported intent","requirements":[{"id":"REQ-1","text":"Keep source history","rationale":"Preserve immutable evidence","source":"brief"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}`)
	digest := projectPortabilityConflictDigest(t, goal, content)
	sourceRevisionID := uuid.NewString()
	sourceRef := "approval-conflict-" + fixtureID
	subjectRevision := int64(1)
	actorID := testUserID
	approval := projectbundle.PortableApproval{
		ID: uuid.NewString(), IdempotencyKey: "source-approval:" + fixtureID,
		SubjectType: "specification_revision", SubjectID: sourceRevisionID,
		SubjectRevision: &subjectRevision, SubjectDigest: digest,
		Decision: "approved", ActorType: "human", ActorID: &actorID,
		ActorDisplay: "Source workspace admin", Rationale: "Approved source revision",
		SourceEndpoint: "/api/projects/source/autonomous/spec/approve", Origin: "local",
		OriginRef: json.RawMessage(`{"source":"approval-conflict-gate"}`), ActedAt: "2026-10-21T10:00:00Z",
	}
	specification := projectbundle.PortableSpecification{
		Goal: goal, Summary: "Imported intent",
		Requirements:              []projectbundle.PortableSpecificationRequirement{{ID: "REQ-1", Text: "Keep source history", Rationale: "Preserve immutable evidence", Source: "brief"}},
		NonFunctionalRequirements: []string{}, Constraints: []string{}, NonGoals: []string{}, DefinitionOfDone: []string{}, AcceptanceExpectations: []string{},
	}
	plan := projectbundle.PortablePlan{
		Goal:          goal,
		Requirements:  []projectbundle.PlanRequirement{{ID: "REQ-1", Text: "Keep source history", Rationale: "Preserve immutable evidence"}},
		Specification: &specification,
		Nodes:         []projectbundle.PlanNode{{Key: "portable-task", Title: "Preserve imported intent", RequirementIDs: []string{"REQ-1"}}},
	}
	status := "todo"
	baseline := projectbundle.Bundle{
		SchemaVersion: projectbundle.SchemaVersionV2,
		Project:       projectbundle.PortableProject{Title: "Approval conflict baseline " + fixtureID},
		Tasks:         []projectbundle.PortableTask{{Key: "portable-task", Title: "Preserve imported intent", Status: &status}},
		Bootstrap:     &projectbundle.PortableBootstrap{Plan: &plan},
		Approvals:     []projectbundle.PortableApproval{approval},
		SpecificationRevisions: []projectbundle.PortableSpecificationRevision{{
			ID: sourceRevisionID, Revision: 1, SourceKind: "planner", SourceRef: &sourceRef,
			Goal: goal, Content: content, Digest: digest, CreatedAt: "2026-10-21T10:00:00Z",
		}},
	}
	importResponse := httptest.NewRecorder()
	testHandler.ImportProject(importResponse, newRequest(http.MethodPost, "/api/projects/import", projectbundle.ImportRequest{Bundle: &baseline}))
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("baseline ImportProject status = %d, want 201: %s", importResponse.Code, importResponse.Body.String())
	}
	var imported projectbundle.PortabilityResult
	if err := json.Unmarshal(importResponse.Body.Bytes(), &imported); err != nil || imported.ProjectID == "" {
		t.Fatalf("decode baseline ImportProject response %q: %v", importResponse.Body.String(), err)
	}
	t.Cleanup(func() { cleanupImportedAdoptionProjectFixture(t, imported.ProjectID) })

	var projectID pgtype.UUID
	if err := projectID.Scan(imported.ProjectID); err != nil {
		t.Fatalf("parse imported project ID: %v", err)
	}
	changedDigest := strings.Repeat("f", 64)
	changedApproval := approval
	changedApproval.SubjectDigest = changedDigest
	changedApproval.Rationale = "Divergent payload under the same source idempotency key"
	syncBundle := baseline
	syncBundle.Project = projectbundle.PortableProject{Title: "Must not be applied"}
	changedTaskTitle := "Must not be applied"
	changedTaskStatus := "done"
	syncBundle.Tasks = []projectbundle.PortableTask{{Key: "portable-task", Title: changedTaskTitle, Status: &changedTaskStatus}}
	syncBundle.Approvals = []projectbundle.PortableApproval{changedApproval}

	previewRequest := newRequest(http.MethodPost, "/api/projects/"+imported.ProjectID+"/sync", projectbundle.SyncRequest{Bundle: &syncBundle, DryRun: true})
	previewRequest = withURLParam(previewRequest, "id", imported.ProjectID)
	previewResponse := httptest.NewRecorder()
	testHandler.SyncProject(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("SyncProject dry-run status = %d, want 200 with conflict details: %s", previewResponse.Code, previewResponse.Body.String())
	}
	var previewResult projectbundle.PortabilityResult
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewResult); err != nil {
		t.Fatalf("decode SyncProject dry-run response: %v", err)
	}
	if previewResult.Preview == nil || len(previewResult.Preview.ApprovalConflicts) == 0 {
		t.Fatalf("SyncProject dry-run did not expose approval conflicts: %s", previewResponse.Body.String())
	}

	applyRequest := newRequest(http.MethodPost, "/api/projects/"+imported.ProjectID+"/sync", projectbundle.SyncRequest{Bundle: &syncBundle})
	applyRequest = withURLParam(applyRequest, "id", imported.ProjectID)
	applyResponse := httptest.NewRecorder()
	testHandler.SyncProject(applyResponse, applyRequest)
	if applyResponse.Code != http.StatusConflict {
		t.Fatalf("SyncProject conflict status = %d, want 409: %s", applyResponse.Code, applyResponse.Body.String())
	}
	var storedProjectTitle, storedTaskTitle, storedTaskStatus string
	if err := testPool.QueryRow(ctx, `SELECT title FROM project WHERE id=$1`, projectID).Scan(&storedProjectTitle); err != nil {
		t.Fatalf("read project after rejected sync: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT title,status FROM issue WHERE project_id=$1 AND metadata->>'multica_portable_key'='portable-task'`, projectID).Scan(&storedTaskTitle, &storedTaskStatus); err != nil {
		t.Fatalf("read task after rejected sync: %v", err)
	}
	if storedProjectTitle != baseline.Project.Title || storedTaskTitle != "Preserve imported intent" || storedTaskStatus != "todo" {
		t.Fatalf("rejected sync mutated state: project=%q task=%q status=%q", storedProjectTitle, storedTaskTitle, storedTaskStatus)
	}

	contradictoryImport := baseline
	contradictoryImport.Project.Title = "Contradictory import " + fixtureID
	otherApproval := approval
	otherApproval.ID = uuid.NewString()
	otherApproval.IdempotencyKey = "second-source-approval:" + fixtureID
	otherApproval.SubjectDigest = changedDigest
	otherApproval.Rationale = "Contradictory approved digest"
	contradictoryImport.Approvals = []projectbundle.PortableApproval{approval, otherApproval}
	dryImportRequest := newRequest(http.MethodPost, "/api/projects/import", projectbundle.ImportRequest{Bundle: &contradictoryImport, DryRun: true})
	dryImportResponse := httptest.NewRecorder()
	testHandler.ImportProject(dryImportResponse, dryImportRequest)
	if dryImportResponse.Code != http.StatusOK {
		t.Fatalf("ImportProject dry-run status = %d, want 200 with conflict details: %s", dryImportResponse.Code, dryImportResponse.Body.String())
	}
	var importPreview projectbundle.PortabilityResult
	if err := json.Unmarshal(dryImportResponse.Body.Bytes(), &importPreview); err != nil {
		t.Fatalf("decode ImportProject dry-run response: %v", err)
	}
	if importPreview.Preview == nil || len(importPreview.Preview.ApprovalConflicts) == 0 {
		t.Fatalf("ImportProject dry-run did not expose contradictory approvals: %s", dryImportResponse.Body.String())
	}

	applyImportRequest := newRequest(http.MethodPost, "/api/projects/import", projectbundle.ImportRequest{Bundle: &contradictoryImport})
	applyImportResponse := httptest.NewRecorder()
	testHandler.ImportProject(applyImportResponse, applyImportRequest)
	if applyImportResponse.Code != http.StatusConflict {
		t.Fatalf("ImportProject conflict status = %d, want 409: %s", applyImportResponse.Code, applyImportResponse.Body.String())
	}
	var created int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM project WHERE workspace_id=$1 AND title=$2`, testWorkspaceID, contradictoryImport.Project.Title).Scan(&created); err != nil {
		t.Fatalf("count rejected imported projects: %v", err)
	}
	if created != 0 {
		t.Fatalf("rejected import created %d project rows", created)
	}
	t.Log("PROJECT_SYNC_APPROVAL_CONFLICT_GATE_PASS")
}

func projectPortabilityConflictDigest(t *testing.T, goal string, content json.RawMessage) string {
	t.Helper()
	var digest string
	if err := testPool.QueryRow(context.Background(), `
		SELECT encode(digest(convert_to(jsonb_build_object('goal',$1::text,'specification',$2::jsonb)::text,'UTF8'),'sha256'),'hex')
	`, goal, string(content)).Scan(&digest); err != nil {
		t.Fatalf("digest approval conflict fixture specification: %v", err)
	}
	return digest
}
