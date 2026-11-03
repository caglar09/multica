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
)

type specificationApprovalFixture struct {
	changeRequestID string
	revisionID      string
	digest          string
}

func addSpecificationApprovalFixture(t *testing.T, projectID, requestKey string) specificationApprovalFixture {
	t.Helper()
	ctx := context.Background()
	var changeRequestID string
	proposal := json.RawMessage(`{"proposal_id":"` + strings.TrimPrefix(requestKey, "project-leader-specification:") + `","rationale":"Test specification proposal","specification":{"goal":"Ship an approved capability","summary":"A complete project specification","requirements":[{"id":"REQ-1","text":"Support approval","rationale":"Required by the owner","source":"brief"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}}`)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_change_request (
			workspace_id,project_id,request_key,request_type,state,source,source_ref,request_text,proposal,impact
		) VALUES ($1,$2,$3,'requirement_change','approval_required','project_director',$4,'Approve this specification',$5,'{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, projectID, requestKey, strings.TrimPrefix(requestKey, "project-leader-specification:"), proposal).Scan(&changeRequestID); err != nil {
		t.Fatalf("insert specification proposal change request: %v", err)
	}
	var revisionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_specification_revision (
			workspace_id,project_id,revision,source_kind,source_ref,goal,specification
		) VALUES (
			$1,$2,(SELECT COALESCE(MAX(revision),0)+1 FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2),
			'change_request',$3,'Ship an approved capability',$4::jsonb
		) RETURNING id
	`, testWorkspaceID, projectID, changeRequestID,
		`{"summary":"A complete project specification","requirements":[{"id":"REQ-1","text":"Support approval","rationale":"Required by the owner","source":"brief"}],"non_functional_requirements":[],"constraints":[],"non_goals":[],"definition_of_done":[],"acceptance_expectations":[]}`).Scan(&revisionID); err != nil {
		t.Fatalf("insert proposed specification revision: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE autonomous_project_change_request SET proposed_specification_revision_id=$2 WHERE id=$1`, changeRequestID, revisionID); err != nil {
		t.Fatalf("link proposed specification revision: %v", err)
	}
	var digest string
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, revisionID).Scan(&digest); err != nil {
		t.Fatalf("calculate specification digest: %v", err)
	}
	t.Cleanup(func() {
		cleanupSpecificationApprovalFixture(t, ctx, projectID, changeRequestID, revisionID)
	})
	return specificationApprovalFixture{changeRequestID: changeRequestID, revisionID: revisionID, digest: digest}
}

func cleanupSpecificationApprovalFixture(t *testing.T, ctx context.Context, projectID, changeRequestID, revisionID string) {
	t.Helper()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Errorf("begin specification approval fixture cleanup: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('multica.approval_purge','on',true)`); err != nil {
		t.Errorf("enable specification approval event cleanup: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_approval_event WHERE project_id=$1 AND subject_id=$2`, projectID, revisionID); err != nil {
		t.Errorf("delete specification approval event: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_change_request WHERE id=$1`, changeRequestID); err != nil {
		t.Errorf("delete specification approval change request: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2 AND specification_revision_id=$3`, testWorkspaceID, projectID, revisionID); err != nil {
		t.Errorf("delete specification approval head: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_specification_revision WHERE id=$1`, revisionID); err != nil {
		t.Errorf("delete specification approval revision: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit specification approval fixture cleanup: %v", err)
	}
}

func callProjectSpecificationDecision(t *testing.T, handler http.HandlerFunc, projectID, changeRequestID, digest, reason, userID string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]string{"change_request_id": changeRequestID, "specification_digest": digest}
	if reason != "" {
		body["reason"] = reason
	}
	req := withURLParam(newRequestAs(userID, http.MethodPost, "/api/projects/"+projectID+"/autonomous/spec/decision", body), "id", projectID)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestProjectSpecificationApprovalLifecycle(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	project := createProjectPermissionTestProject(t, "project specification approval lifecycle")

	approval := addSpecificationApprovalFixture(t, project.ID, "project-leader-specification:approve-"+uuid.NewString())
	rejection := addSpecificationApprovalFixture(t, project.ID, "project-leader-specification:reject-"+uuid.NewString())
	generic := addSpecificationApprovalFixture(t, project.ID, "ordinary-change-"+uuid.NewString())

	runtimeID := createHandlerTestRuntime(t, "Specification approval PM runtime")
	agentID := createHandlerTestAgentForRuntime(t, "Specification approval PM", []byte("[]"), runtimeID)
	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id,agent_id,creator_id,title,runtime_id,project_id,session_kind,explicitly_created_at
		) VALUES ($1,$2,$3,'Project Manager',$4,$5,'autonomous_project_leader',now())
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, runtimeID, project.ID).Scan(&sessionID); err != nil {
		t.Fatalf("insert project manager session: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM chat_session WHERE id=$1`, sessionID) })

	// Workspace membership alone cannot approve a proposed specification.
	memberID := uuid.NewString()
	w := callProjectSpecificationDecision(t, testHandler.ApproveProjectSpecification, project.ID, approval.changeRequestID, approval.digest, "", memberID)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin approval status = %d, want 403: %s", w.Code, w.Body.String())
	}

	// A stale digest is a conflict and leaves the proposal off-head and pending.
	w = callProjectSpecificationDecision(t, testHandler.ApproveProjectSpecification, project.ID, approval.changeRequestID, strings.Repeat("0", 64), "", testUserID)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale approval status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var state string
	if err := testPool.QueryRow(ctx, `SELECT state FROM autonomous_project_change_request WHERE id=$1`, approval.changeRequestID).Scan(&state); err != nil {
		t.Fatalf("read pending proposal state: %v", err)
	}
	if state != "approval_required" {
		t.Fatalf("stale digest changed proposal state to %q", state)
	}
	var headID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT specification_revision_id FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`, testWorkspaceID, project.ID).Scan(&headID); err == nil {
		t.Fatalf("stale digest activated specification head %s", uuidToString(headID))
	}

	w = callProjectSpecificationDecision(t, testHandler.ApproveProjectSpecification, project.ID, approval.changeRequestID, approval.digest, "", testUserID)
	if w.Code != http.StatusOK {
		t.Fatalf("exact-digest approval status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var approved bool
	if err := testPool.QueryRow(ctx, `
		SELECT c.state='approved' AND h.specification_revision_id=c.proposed_specification_revision_id
		FROM autonomous_project_change_request c
		JOIN autonomous_project_specification_head h ON h.workspace_id=c.workspace_id AND h.project_id=c.project_id
		WHERE c.id=$1
	`, approval.changeRequestID).Scan(&approved); err != nil {
		t.Fatalf("assert atomic approved head and proposal: %v", err)
	}
	if !approved {
		t.Fatal("approved proposal did not become the active specification head")
	}
	assertSpecificationApprovalLedgerEvent(t, project.ID, approval, "approved", "Approved the exact reviewed specification digest")

	// Rejection requires both an exact digest and a non-empty trimmed reason.
	w = callProjectSpecificationDecision(t, testHandler.RejectProjectSpecification, project.ID, rejection.changeRequestID, rejection.digest, "  \t ", testUserID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty rejection reason status = %d, want 400: %s", w.Code, w.Body.String())
	}
	w = callProjectSpecificationDecision(t, testHandler.RejectProjectSpecification, project.ID, rejection.changeRequestID, strings.Repeat("0", 64), "Scope was not approved.", testUserID)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale rejection status = %d, want 409: %s", w.Code, w.Body.String())
	}
	const reason = "Scope was not approved."
	w = callProjectSpecificationDecision(t, testHandler.RejectProjectSpecification, project.ID, rejection.changeRequestID, rejection.digest, "  "+reason+"  ", testUserID)
	if w.Code != http.StatusOK {
		t.Fatalf("exact-digest rejection status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var rejected bool
	var storedReason string
	if err := testPool.QueryRow(ctx, `SELECT state='rejected',error FROM autonomous_project_change_request WHERE id=$1`, rejection.changeRequestID).Scan(&rejected, &storedReason); err != nil {
		t.Fatalf("read rejected proposal: %v", err)
	}
	if !rejected || storedReason != reason {
		t.Fatalf("rejected proposal = (%v, %q), want (true, %q)", rejected, storedReason, reason)
	}
	var messageRole, message string
	if err := testPool.QueryRow(ctx, `SELECT role,content FROM chat_message WHERE chat_session_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, sessionID).Scan(&messageRole, &message); err != nil {
		t.Fatalf("read project manager rejection message: %v", err)
	}
	if messageRole != "user" || !strings.Contains(message, "System notification") || !strings.Contains(message, reason) {
		t.Fatalf("project manager message = (%q, %q), want rejection system notification with reason", messageRole, message)
	}
	assertSpecificationApprovalLedgerEvent(t, project.ID, rejection, "rejected", reason)
	if err := testPool.QueryRow(ctx, `SELECT specification_revision_id FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`, testWorkspaceID, project.ID).Scan(&headID); err != nil {
		t.Fatalf("read specification head after rejection: %v", err)
	}
	if uuidToString(headID) != approval.revisionID {
		t.Fatalf("rejection changed active specification head to %s, want %s", uuidToString(headID), approval.revisionID)
	}

	// A generic local change request carrying a revision cannot enter this
	// dedicated consent path or inherit the specification approval event.
	w = callProjectSpecificationDecision(t, testHandler.ApproveProjectSpecification, project.ID, generic.changeRequestID, generic.digest, "", testUserID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("generic change request approval status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT state FROM autonomous_project_change_request WHERE id=$1`, generic.changeRequestID).Scan(&state); err != nil {
		t.Fatalf("read generic change state: %v", err)
	}
	if state != "approval_required" {
		t.Fatalf("dedicated spec endpoint changed generic change state to %q", state)
	}
	var genericEvents int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_approval_event WHERE project_id=$1 AND subject_id=$2`, project.ID, generic.revisionID).Scan(&genericEvents); err != nil {
		t.Fatalf("read generic proposal approval events: %v", err)
	}
	if genericEvents != 0 {
		t.Fatalf("generic change request created %d specification approval events", genericEvents)
	}

	t.Log("PROJECT_SPECIFICATION_APPROVAL_GATE_PASS")
}

func assertSpecificationApprovalLedgerEvent(t *testing.T, projectID string, fixture specificationApprovalFixture, decision, reason string) {
	t.Helper()
	var subjectRevision int64
	var subjectDigest, gotDecision, actorType, origin, rationale string
	var actorID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT subject_revision,subject_digest,decision,actor_type,actor_id,origin,rationale
		FROM autonomous_project_approval_event
		WHERE project_id=$1 AND subject_type='specification_revision' AND subject_id=$2
		ORDER BY acted_at DESC,id DESC LIMIT 1
	`, projectID, fixture.revisionID).Scan(&subjectRevision, &subjectDigest, &gotDecision, &actorType, &actorID, &origin, &rationale); err != nil {
		t.Fatalf("read %s specification approval event: %v", decision, err)
	}
	if subjectRevision < 1 || subjectDigest != fixture.digest || gotDecision != decision || actorType != "human" ||
		uuidToString(actorID) != testUserID || origin != "local" || rationale != reason {
		t.Fatalf("specification %s event = revision:%d digest:%s decision:%s actor:%s/%s want:%s origin:%s rationale:%q",
			decision, subjectRevision, subjectDigest, gotDecision, actorType, uuidToString(actorID), testUserID, origin, rationale)
	}
}
