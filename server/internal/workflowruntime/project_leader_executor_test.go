package workflowruntime

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestApprovalLedgerChangeSurfaces(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	actorID := d2ParseUUID(t, fixture.ownerID)
	var logicalNodeID string
	if err := fixture.pool.QueryRow(ctx, `
		UPDATE autonomous_project_plan_node
		SET status='pending'
		WHERE id=$1
		RETURNING logical_node_id::text
	`, fixture.nodeID).Scan(&logicalNodeID); err != nil {
		t.Fatalf("make change target not started and read logical node: %v", err)
	}
	targetNodeID := uuid.MustParse(logicalNodeID)
	proposal, err := json.Marshal(projectorchestration.ProjectChangeProposal{
		ProposalID:       "approval-ledger-change-" + uuid.NewString(),
		Summary:          "Clarify the deployment task",
		Rationale:        "Keep the current task scope while making its outcome explicit.",
		BasePlanRevision: 1,
		Operations: []projectorchestration.ProjectChangeOperation{{
			Type:   projectorchestration.ProjectLeaderOpUpdateNotStartedNode,
			NodeID: &targetNodeID,
			Reason: "Clarify the title without changing the delivery boundary.",
			Patch:  map[string]any{"title": "Deploy the approved release"},
		}},
	})
	if err != nil {
		t.Fatalf("encode project leader proposal: %v", err)
	}
	var changeID pgtype.UUID
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_change_request (
			workspace_id,project_id,request_key,request_type,state,source,request_text,
			proposal,impact,base_plan_id
		) VALUES ($1,$2,$3,'requirement_change','approval_required','project_director',
		         'Clarify the deployment task',$4,'{}'::jsonb,$5)
		RETURNING id
	`, fixture.workspaceID, fixture.projectID, "approval-ledger-"+uuid.NewString(), proposal, fixture.planID).Scan(&changeID); err != nil {
		t.Fatalf("insert approval-required change request: %v", err)
	}
	var initialHistoryActorType string
	var initialHistoryActorRef pgtype.Text
	if err := fixture.pool.QueryRow(ctx, `
		SELECT actor_type,actor_ref FROM autonomous_project_change_request_event
		WHERE change_request_id=$1 AND from_state IS NULL AND to_state='approval_required'
	`, changeID).Scan(&initialHistoryActorType, &initialHistoryActorRef); err != nil {
		t.Fatalf("read initial system change history event: %v", err)
	}
	if initialHistoryActorType != "system" || initialHistoryActorRef.Valid {
		t.Fatalf("initial change history actor = (%q,%q), want system without actor ref", initialHistoryActorType, initialHistoryActorRef.String)
	}
	t.Cleanup(func() { purgeWorkflowApprovalEvents(t, fixture, fixture.projectID) })

	idempotencyKey := "approval-ledger-job-" + uuid.NewString()
	if err := NewMikaProjectLeaderExecutor(fixture.pool, nil).ApproveChange(
		ctx, fixture.workspaceID, fixture.projectID, changeID, actorID, idempotencyKey,
	); err != nil {
		t.Fatalf("approve project leader change: %v", err)
	}
	var state string
	if err := fixture.pool.QueryRow(ctx, `SELECT state FROM autonomous_project_change_request WHERE id=$1`, changeID).Scan(&state); err != nil {
		t.Fatalf("read approved change state: %v", err)
	}
	if state != "approved" {
		t.Fatalf("change request state = %q, want approved", state)
	}
	var historyActorType, historyActorRef string
	if err := fixture.pool.QueryRow(ctx, `
		SELECT actor_type,actor_ref FROM autonomous_project_change_request_event
		WHERE change_request_id=$1 AND from_state='approval_required' AND to_state='approved'
		ORDER BY created_at DESC,id DESC LIMIT 1
	`, changeID).Scan(&historyActorType, &historyActorRef); err != nil {
		t.Fatalf("read approved change history actor: %v", err)
	}
	if historyActorType != "human" || historyActorRef != fixture.ownerID {
		t.Fatalf("approval history actor = (%q,%q), want human %s", historyActorType, historyActorRef, fixture.ownerID)
	}
	var proposalDigest string
	if err := fixture.pool.QueryRow(ctx, `SELECT autonomous_project_change_proposal_digest($1)`, changeID).Scan(&proposalDigest); err != nil {
		t.Fatalf("calculate approved proposal digest: %v", err)
	}
	var eventCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND idempotency_key=$3
	`, fixture.workspaceID, fixture.projectID, "change-request-approve:"+changeID.String()).Scan(&eventCount); err != nil {
		t.Fatalf("count change approval ledger events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("change approval ledger event count = %d, want exactly 1", eventCount)
	}
	var subjectType, subjectID, eventDigest, decision, actorType, actorDisplay, rationale string
	var eventActorID pgtype.UUID
	if err := fixture.pool.QueryRow(ctx, `
		SELECT subject_type, subject_id, subject_digest, decision,
		       actor_type, actor_id, actor_display, rationale
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND idempotency_key=$3
	`, fixture.workspaceID, fixture.projectID, "change-request-approve:"+changeID.String()).Scan(
		&subjectType, &subjectID, &eventDigest, &decision, &actorType, &eventActorID, &actorDisplay, &rationale,
	); err != nil {
		t.Fatalf("read change approval ledger event: %v", err)
	}
	if subjectType != "change_request" || subjectID != changeID.String() || eventDigest != proposalDigest ||
		decision != "approved" || actorType != "human" || eventActorID != actorID || actorDisplay != fixture.ownerID ||
		rationale == "" {
		t.Fatalf("change approval ledger = count:%d subject:(%q,%q,%q) decision:%q actor:(%q,%q,%q) rationale:%q",
			eventCount, subjectType, subjectID, eventDigest, decision, actorType, eventActorID.String(), actorDisplay, rationale)
	}
	var jobCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_control_plane_job
		WHERE workspace_id=$1 AND project_id=$2 AND job_type='plan_mutation' AND idempotency_key=$3
	`, fixture.workspaceID, fixture.projectID, idempotencyKey).Scan(&jobCount); err != nil {
		t.Fatalf("count queued plan mutation jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("queued plan mutation jobs = %d, want exactly 1", jobCount)
	}
	t.Log("APPROVAL_RUNTIME_SURFACES_GATE_PASS")
}

func TestProjectLeaderSpecificationProposalRemainsAwaitingApproval(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	projectFixture := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID)
	projectIDText := projectFixture.Project(t, "Project leader specification draft")
	projectID := d2ParseUUID(t, projectIDText)
	t.Cleanup(func() {
		if err := projectorchestration.NewStore(fixture.pool).CleanupProject(context.Background(), fixture.workspaceID, projectID); err != nil {
			t.Errorf("clean project leader specification draft project: %v", err)
		}
	})
	runtimeID := projectFixture.Runtime(t, "Project Manager proposal test")
	agentID := projectFixture.Agent(t, "Project Manager proposal test", runtimeID)
	sessionID := projectFixture.ChatSession(t, agentID, testutil.Cols{
		"project_id":   projectIDText,
		"session_kind": projectLeaderSessionKind,
	})
	taskID := d2ParseUUID(t, projectFixture.Insert(t, "agent_task_queue", testutil.Cols{
		"runtime_id":      runtimeID,
		"agent_id":        agentID,
		"chat_session_id": sessionID,
		"status":          "completed",
	}))

	specification := projectorchestration.ProjectSpecification{
		Goal:    "Deliver a dependable project planning workflow",
		Summary: "The project starts with a reviewable requirements specification.",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-001", Text: "A project manager drafts structured requirements before planning.",
			Rationale: "The first plan must be grounded in explicit user intent.", Source: "project brief",
		}},
		DefinitionOfDone: []string{"An owner can review the specification before a plan is created."},
	}
	proposal := projectorchestration.ProjectLeaderSpecificationProposal{
		ProposalID:    "initial-spec-" + uuid.NewString(),
		Rationale:     "The request does not define testable scope, so the PM recorded a structured draft for owner review.",
		Specification: specification,
	}
	var initialPlanCount, initialHeadCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, projectID).Scan(&initialPlanCount); err != nil {
		t.Fatalf("count initial project plans: %v", err)
	}
	if initialPlanCount != 0 {
		t.Fatalf("initial project plans = %d, want none", initialPlanCount)
	}
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, projectID).Scan(&initialHeadCount); err != nil {
		t.Fatalf("count initial specification heads: %v", err)
	}
	if initialHeadCount != 0 {
		t.Fatalf("initial specification heads = %d, want none", initialHeadCount)
	}
	response, err := json.Marshal(projectorchestration.ProjectLeaderResponse{
		Message:               "I drafted the initial project specification for approval.",
		SpecificationProposal: &proposal,
	})
	if err != nil {
		t.Fatalf("encode project leader specification proposal: %v", err)
	}
	completion, err := json.Marshal(struct {
		Output string `json:"output"`
	}{Output: string(response)})
	if err != nil {
		t.Fatalf("encode project leader completion envelope: %v", err)
	}
	if err := NewMikaProjectLeaderExecutor(fixture.pool, nil).ProcessCompletion(ctx, taskID, completion); err != nil {
		t.Fatalf("process initial project leader specification proposal: %v", err)
	}

	var planCount, headCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, projectID).Scan(&planCount); err != nil {
		t.Fatalf("count project plans: %v", err)
	}
	if planCount != 0 {
		t.Fatalf("project plans = %d, want none before specification approval", planCount)
	}
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, projectID).Scan(&headCount); err != nil {
		t.Fatalf("count specification heads: %v", err)
	}
	if headCount != initialHeadCount {
		t.Fatalf("specification heads = %d, want unchanged initial count %d", headCount, initialHeadCount)
	}

	var changeIDText, state, proposedRevisionID string
	var basePlanID pgtype.UUID
	var rawProposal []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT id::text,state,proposed_specification_revision_id::text,base_plan_id,proposal
		FROM autonomous_project_change_request
		WHERE workspace_id=$1 AND project_id=$2 AND request_key=$3
	`, fixture.workspaceID, projectID, "project-leader-specification:"+proposal.ProposalID).Scan(
		&changeIDText, &state, &proposedRevisionID, &basePlanID, &rawProposal,
	); err != nil {
		t.Fatalf("load initial specification approval request: %v", err)
	}
	if state != string(projectorchestration.ChangeApprovalRequired) {
		t.Fatalf("initial specification request state = %q, want %q", state, projectorchestration.ChangeApprovalRequired)
	}
	if proposedRevisionID == "" || basePlanID.Valid {
		t.Fatalf("initial specification request revision/base plan = (%q,%v), want linked draft and no plan", proposedRevisionID, basePlanID.Valid)
	}
	var storedProposal projectorchestration.ProjectLeaderSpecificationProposal
	if err := json.Unmarshal(rawProposal, &storedProposal); err != nil {
		t.Fatalf("decode stored project leader specification proposal: %v", err)
	}
	if !reflect.DeepEqual(storedProposal, proposal) {
		t.Fatalf("stored specification proposal = %#v, want %#v", storedProposal, proposal)
	}

	var sourceKind string
	var sourceRef string
	var revision int64
	var rawSpecification []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT source_kind,source_ref,revision,specification
		FROM autonomous_project_specification_revision
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
	`, proposedRevisionID, fixture.workspaceID, projectID).Scan(&sourceKind, &sourceRef, &revision, &rawSpecification); err != nil {
		t.Fatalf("load proposed specification revision: %v", err)
	}
	var storedSpecification projectorchestration.ProjectSpecification
	if err := json.Unmarshal(rawSpecification, &storedSpecification); err != nil {
		t.Fatalf("decode proposed specification revision: %v", err)
	}
	if !reflect.DeepEqual(storedSpecification, specification) {
		t.Fatalf("stored specification = %#v, want %#v", storedSpecification, specification)
	}
	if sourceKind != "change_request" || sourceRef != changeIDText || revision <= 0 {
		t.Fatalf("specification revision provenance = (%q,%q,%d), want linked change request and positive revision", sourceKind, sourceRef, revision)
	}

	changeID := d2ParseUUID(t, changeIDText)
	ownerID := d2ParseUUID(t, fixture.ownerID)
	if err := NewMikaProjectLeaderExecutor(fixture.pool, nil).ApproveChange(ctx, fixture.workspaceID, projectID, changeID, ownerID, ""); err == nil {
		t.Fatal("approve initial specification through change request, want no-plan rejection")
	}
	var finalState string
	if err := fixture.pool.QueryRow(ctx, `SELECT state FROM autonomous_project_change_request WHERE id=$1`, changeID).Scan(&finalState); err != nil {
		t.Fatalf("read specification request after change approval rejection: %v", err)
	}
	if finalState != string(projectorchestration.ChangeApprovalRequired) {
		t.Fatalf("initial specification request state after rejected change approval = %q, want %q", finalState, projectorchestration.ChangeApprovalRequired)
	}
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, projectID).Scan(&headCount); err != nil {
		t.Fatalf("recount specification heads after rejected change approval: %v", err)
	}
	if headCount != initialHeadCount {
		t.Fatalf("specification heads after rejected change approval = %d, want unchanged initial count %d", headCount, initialHeadCount)
	}
	t.Log("PROJECT_LEADER_SPEC_DRAFT_GATE_PASS")
}

func purgeWorkflowApprovalEvents(t *testing.T, fixture *autonomousDeploymentBridgeFixture, projectID pgtype.UUID) {
	t.Helper()
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Errorf("begin workflow approval event cleanup: %v", err)
		return
	}
	defer tx.Rollback(fixture.ctx)
	if _, err := tx.Exec(fixture.ctx, `SELECT set_config('multica.approval_purge','on',true)`); err != nil {
		t.Errorf("enable workflow approval event cleanup: %v", err)
		return
	}
	if _, err := tx.Exec(fixture.ctx, `DELETE FROM autonomous_project_approval_event WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete workflow approval events: %v", err)
		return
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Errorf("commit workflow approval event cleanup: %v", err)
	}
}
