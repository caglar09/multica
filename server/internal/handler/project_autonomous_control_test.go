package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAutonomousPauseResumePersistsControlState(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous pause resume project")
	_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	})

	pauseReq := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/pause", nil), "id", project.ID)
	pauseW := httptest.NewRecorder()
	testHandler.PauseProjectAutonomous(pauseW, pauseReq)
	if pauseW.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200: %s", pauseW.Code, pauseW.Body.String())
	}

	var paused bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT paused FROM autonomous_project_control WHERE project_id = $1
	`, project.ID).Scan(&paused); err != nil {
		t.Fatalf("read paused control state: %v", err)
	}
	if !paused {
		t.Fatal("pause endpoint returned success but persisted paused=false")
	}

	resumeReq := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/resume", nil), "id", project.ID)
	resumeW := httptest.NewRecorder()
	testHandler.ResumeProjectAutonomous(resumeW, resumeReq)
	if resumeW.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200: %s", resumeW.Code, resumeW.Body.String())
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT paused FROM autonomous_project_control WHERE project_id = $1
	`, project.ID).Scan(&paused); err != nil {
		t.Fatalf("read resumed control state: %v", err)
	}
	if paused {
		t.Fatal("resume endpoint returned success but persisted paused=true")
	}
}

func TestStartProjectAutonomousPlanningRequiresExplicitHumanGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectPermissionTestProject(t, "autonomous planning gate project")
	createApprovedProjectSpecificationFixture(t, project.ID, "planning-start-gate-"+uuid.NewString())
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO autonomous_project_team_draft (
			project_id, workspace_id, plan, planner_name, status, confirmed_at, confirmed_by
		)
		VALUES ($1, $2, '{}'::jsonb, 'Mika', 'applied', now(), $3)
	`, project.ID, testWorkspaceID, testUserID)
	if err != nil {
		t.Fatalf("seed provisioned team draft: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_team_draft WHERE project_id = $1`, project.ID)
	})

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/planning/start", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.StartProjectAutonomousPlanning(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start planning status = %d, want 202: %s", w.Code, w.Body.String())
	}

	var startedAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `
		SELECT continuation_started_at
		FROM autonomous_project_team_draft
		WHERE project_id = $1
	`, project.ID).Scan(&startedAt); err != nil {
		t.Fatalf("read planning gate: %v", err)
	}
	if !startedAt.Valid {
		t.Fatal("planning gate did not persist the explicit start")
	}
}

func TestApprovalLedgerApprovalSurfaces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	project := createProjectPermissionTestProject(t, "approval ledger human surfaces project")
	projectID := parseUUID(project.ID)
	workspaceID := parseUUID(testWorkspaceID)
	actorID := parseUUID(testUserID)
	createApprovedProjectSpecificationFixture(t, project.ID, "approval-surfaces-"+uuid.NewString())

	teamPlan, err := json.Marshal(map[string]any{"roles": []map[string]string{{"role": "product_manager"}}})
	if err != nil {
		t.Fatalf("encode team plan: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_team_draft (
			project_id, workspace_id, plan, planner_name, status
		) VALUES ($1,$2,$3,'Mika','awaiting_configuration')
	`, projectID, workspaceID, teamPlan); err != nil {
		t.Fatalf("insert awaiting team draft: %v", err)
	}
	runtimeID := newTestRuntime(t, "Approval Ledger Human Surface Runtime", "online")
	confirmReq := withURLParam(newRequest(http.MethodPost,
		"/api/projects/"+project.ID+"/autonomous/team/confirm",
		map[string]any{"assignments": []map[string]any{{"role": "product_manager", "runtime_id": runtimeID, "skill_mode": "inherit"}}}), "id", project.ID)
	confirmW := httptest.NewRecorder()
	testHandler.ConfirmProjectAutonomousTeam(confirmW, confirmReq)
	if confirmW.Code != http.StatusAccepted {
		t.Fatalf("confirm team status = %d, want 202: %s", confirmW.Code, confirmW.Body.String())
	}
	var teamStatus, confirmedBy string
	if err := testPool.QueryRow(ctx, `
		SELECT status, confirmed_by::text FROM autonomous_project_team_draft WHERE project_id=$1
	`, projectID).Scan(&teamStatus, &confirmedBy); err != nil {
		t.Fatalf("read confirmed team draft: %v", err)
	}
	if teamStatus != "provisioning" || confirmedBy != testUserID {
		t.Fatalf("team confirmation projection = (%q, %q), want (provisioning, %s)", teamStatus, confirmedBy, testUserID)
	}
	var teamDigest string
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_team_selection_digest($1)`, projectID).Scan(&teamDigest); err != nil {
		t.Fatalf("calculate team selection digest: %v", err)
	}
	assertApprovalLedgerEvent(t, projectID, "team-confirm:"+project.ID+":"+teamDigest, "team_draft", project.ID,
		teamDigest, "approved", actorID, "Confirmed the configured autonomous team selection")

	if _, err := testPool.Exec(ctx, `UPDATE autonomous_project_team_draft SET status='applied' WHERE project_id=$1`, projectID); err != nil {
		t.Fatalf("mark team provisioned: %v", err)
	}
	startReq := withURLParam(newRequest(http.MethodPost,
		"/api/projects/"+project.ID+"/autonomous/planning/start", nil), "id", project.ID)
	startW := httptest.NewRecorder()
	testHandler.StartProjectAutonomousPlanning(startW, startReq)
	if startW.Code != http.StatusAccepted {
		t.Fatalf("start planning status = %d, want 202: %s", startW.Code, startW.Body.String())
	}
	var startedAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `SELECT continuation_started_at FROM autonomous_project_team_draft WHERE project_id=$1`, projectID).Scan(&startedAt); err != nil {
		t.Fatalf("read planning start projection: %v", err)
	}
	if !startedAt.Valid {
		t.Fatal("planning start did not persist continuation_started_at")
	}
	var startDigest string
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_team_selection_digest($1)`, projectID).Scan(&startDigest); err != nil {
		t.Fatalf("calculate planning start digest: %v", err)
	}
	assertApprovalLedgerEvent(t, projectID, "planning-start:"+project.ID+":"+startDigest, "planning_start", project.ID,
		startDigest, "approved", actorID, "Started planning for the confirmed autonomous team selection")

	var changeID pgtype.UUID
	proposal := json.RawMessage(`{"proposal_id":"approval-test","summary":"Clarify task","rationale":"Make acceptance explicit","base_plan_revision":1,"operations":[]}`)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_change_request (
			workspace_id,project_id,request_key,request_type,state,source,request_text,proposal,impact
		) VALUES ($1,$2,$3,'requirement_change','approval_required','project_director','Clarify a requirement',$4,'{}'::jsonb)
		RETURNING id
	`, workspaceID, projectID, "approval-ledger-"+uuid.NewString(), proposal).Scan(&changeID); err != nil {
		t.Fatalf("insert project leader change request: %v", err)
	}
	var initialHistoryActorType string
	var initialHistoryActorRef pgtype.Text
	if err := testPool.QueryRow(ctx, `
		SELECT actor_type,actor_ref FROM autonomous_project_change_request_event
		WHERE change_request_id=$1 AND from_state IS NULL AND to_state='approval_required'
	`, changeID).Scan(&initialHistoryActorType, &initialHistoryActorRef); err != nil {
		t.Fatalf("read initial system change history event: %v", err)
	}
	if initialHistoryActorType != "system" || initialHistoryActorRef.Valid {
		t.Fatalf("initial change history actor = (%q,%q), want system without actor ref", initialHistoryActorType, initialHistoryActorRef.String)
	}
	rejectionReason := "The proposed wording would remove an agreed acceptance condition."
	rejectReq := withURLParams(newRequest(http.MethodPost,
		"/api/projects/"+project.ID+"/autonomous/change-requests/"+uuidToString(changeID)+"/reject",
		map[string]string{"reason": rejectionReason}), "id", project.ID, "changeRequestId", uuidToString(changeID))
	rejectW := httptest.NewRecorder()
	testHandler.RejectProjectLeaderChange(rejectW, rejectReq)
	if rejectW.Code != http.StatusOK {
		t.Fatalf("reject change status = %d, want 200: %s", rejectW.Code, rejectW.Body.String())
	}
	var rejectedState, storedReason string
	if err := testPool.QueryRow(ctx, `SELECT state,error FROM autonomous_project_change_request WHERE id=$1`, changeID).Scan(&rejectedState, &storedReason); err != nil {
		t.Fatalf("read rejected change projection: %v", err)
	}
	if rejectedState != "rejected" || storedReason != rejectionReason {
		t.Fatalf("rejected change projection = (%q, %q), want rejected with supplied rationale", rejectedState, storedReason)
	}
	var rejectedHistoryActorType, rejectedHistoryActorRef string
	if err := testPool.QueryRow(ctx, `
		SELECT actor_type, actor_ref FROM autonomous_project_change_request_event
		WHERE change_request_id=$1 AND from_state='approval_required' AND to_state='rejected'
		ORDER BY created_at DESC,id DESC LIMIT 1
	`, changeID).Scan(&rejectedHistoryActorType, &rejectedHistoryActorRef); err != nil {
		t.Fatalf("read rejected change history: %v", err)
	}
	if rejectedHistoryActorType != "human" || rejectedHistoryActorRef != testUserID {
		t.Fatalf("rejection history actor = (%q, %q), want human %s", rejectedHistoryActorType, rejectedHistoryActorRef, testUserID)
	}
	var proposalDigest string
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_change_proposal_digest($1)`, changeID).Scan(&proposalDigest); err != nil {
		t.Fatalf("calculate rejected proposal digest: %v", err)
	}
	assertApprovalLedgerEvent(t, projectID, "change-request-reject:"+uuidToString(changeID), "change_request", uuidToString(changeID),
		proposalDigest, "rejected", actorID, rejectionReason)

	var escalationID pgtype.UUID
	escalationContext := json.RawMessage(`{"approval_scope":"manual_action","action":"change worker capacity"}`)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_escalation (workspace_id,project_id,category,status,summary,context)
		VALUES ($1,$2,'approval_required','open','Review manual action',$3)
		RETURNING id
	`, workspaceID, projectID, escalationContext).Scan(&escalationID); err != nil {
		t.Fatalf("insert non-production approval escalation: %v", err)
	}
	escalationNote := "Rejected because the action exceeds the approved operating scope."
	escalationReq := withURLParams(newRequest(http.MethodPost,
		"/api/projects/"+project.ID+"/autonomous/escalations/"+uuidToString(escalationID)+"/resolve",
		map[string]string{"decision": "rejected", "note": escalationNote}), "id", project.ID, "escalationId", uuidToString(escalationID))
	escalationW := httptest.NewRecorder()
	testHandler.ResolveProjectAutonomousEscalation(escalationW, escalationReq)
	if escalationW.Code != http.StatusOK {
		t.Fatalf("resolve non-production escalation status = %d, want 200: %s", escalationW.Code, escalationW.Body.String())
	}
	var escalationStatus string
	var resolution map[string]any
	if err := testPool.QueryRow(ctx, `SELECT status,resolution FROM autonomous_project_escalation WHERE id=$1`, escalationID).Scan(&escalationStatus, &resolution); err != nil {
		t.Fatalf("read non-production escalation projection: %v", err)
	}
	if escalationStatus != "resolved" || resolution["decision"] != "rejected" || resolution["note"] != escalationNote {
		t.Fatalf("non-production escalation resolution = (%q, %#v), want rejected with rationale", escalationStatus, resolution)
	}
	var escalationDigest string
	if err := testPool.QueryRow(ctx, `
		SELECT encode(digest(convert_to(context::text, 'UTF8'), 'sha256'), 'hex')
		FROM autonomous_project_escalation WHERE id=$1
	`, escalationID).Scan(&escalationDigest); err != nil {
		t.Fatalf("calculate non-production escalation digest: %v", err)
	}
	assertApprovalLedgerEvent(t, projectID, "escalation-resolution:"+uuidToString(escalationID), "escalation", uuidToString(escalationID),
		escalationDigest, "rejected", actorID, escalationNote)

	exerciseProductionEscalationApprovalLedger(t)
	t.Log("APPROVAL_HANDLER_SURFACES_GATE_PASS")
}

func assertApprovalLedgerEvent(t *testing.T, projectID pgtype.UUID, idempotencyKey, subjectType, subjectID, digest, decision string, actorID pgtype.UUID, rationale string) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM autonomous_project_approval_event
		WHERE project_id=$1 AND idempotency_key=$2
	`, projectID, idempotencyKey).Scan(&count); err != nil {
		t.Fatalf("count approval ledger event %q: %v", idempotencyKey, err)
	}
	if count != 1 {
		t.Fatalf("approval ledger event %q count = %d, want exactly 1", idempotencyKey, count)
	}
	var gotSubjectType, gotSubjectID, gotDigest, gotDecision, actorType, actorDisplay, gotRationale string
	var gotActorID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT subject_type, subject_id, subject_digest, decision,
		       actor_type, actor_id, actor_display, rationale
		FROM autonomous_project_approval_event
		WHERE project_id=$1 AND idempotency_key=$2
	`, projectID, idempotencyKey).Scan(&gotSubjectType, &gotSubjectID, &gotDigest, &gotDecision,
		&actorType, &gotActorID, &actorDisplay, &gotRationale); err != nil {
		t.Fatalf("read approval ledger event %q: %v", idempotencyKey, err)
	}
	if gotSubjectType != subjectType || gotSubjectID != subjectID || gotDigest != digest ||
		gotDecision != decision || actorType != "human" || gotActorID != actorID ||
		actorDisplay != actorID.String() || gotRationale != rationale {
		t.Fatalf("approval ledger event %q = subject:(%q,%q,%q) decision:%q actor:(%q,%q,%q) rationale:%q",
			idempotencyKey, gotSubjectType, gotSubjectID, gotDigest, gotDecision,
			actorType, gotActorID.String(), actorDisplay, gotRationale)
	}
}

func purgeApprovalEventsForProject(t *testing.T, projectID pgtype.UUID) {
	t.Helper()
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Errorf("begin approval event cleanup: %v", err)
		return
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `SELECT set_config('multica.approval_purge','on',true)`); err != nil {
		t.Errorf("enable approval event cleanup: %v", err)
		return
	}
	if _, err := tx.Exec(context.Background(), `DELETE FROM autonomous_project_approval_event WHERE project_id=$1`, projectID); err != nil {
		t.Errorf("delete approval events: %v", err)
		return
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Errorf("commit approval event cleanup: %v", err)
	}
}

func approveProjectSpecificationFixture(t *testing.T, projectID string, revision projectorchestration.SpecificationRevision) {
	t.Helper()
	ctx := context.Background()
	var digest string
	if err := testPool.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1::uuid)`, revision.ID).Scan(&digest); err != nil {
		t.Fatalf("calculate project specification fixture digest: %v", err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin project specification fixture approval: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID:     parseUUID(testWorkspaceID),
		ProjectID:       parseUUID(projectID),
		IdempotencyKey:  "handler-specification-fixture:" + uuid.NewString(),
		SubjectType:     "specification_revision",
		SubjectID:       revision.ID,
		SubjectRevision: pgtype.Int8{Int64: revision.Revision, Valid: true},
		SubjectDigest:   digest,
		Decision:        "approved",
		ActorType:       "human",
		ActorID:         parseUUID(testUserID),
		ActorDisplay:    testUserID,
		Rationale:       "Approved current specification for handler test fixture",
		SourceEndpoint:  "/test/handler/project-specification",
		Origin:          "local",
	}); err != nil {
		t.Fatalf("record local project specification approval: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit local project specification approval: %v", err)
	}
}

func createApprovedProjectSpecificationFixture(t *testing.T, projectID, sourceRef string) projectorchestration.SpecificationRevision {
	t.Helper()
	ctx := context.Background()
	spec := projectorchestration.ProjectSpecification{
		Goal:    "Deliver the requested project capability",
		Summary: "Locally approved scope for handler test coverage",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-HANDLER-APPROVAL-1", Text: "Implement the project capability.",
			Rationale: "The project needs this capability.", Source: "brief",
		}},
		DefinitionOfDone: []string{"The project capability is complete and reviewed."},
	}
	revision, err := projectorchestration.NewStore(testPool).CreateSpecificationRevision(ctx, parseUUID(testWorkspaceID), parseUUID(projectID),
		"project_bootstrap", sourceRef, spec, true)
	if err != nil {
		t.Fatalf("create approved project specification fixture: %v", err)
	}
	t.Cleanup(func() {
		projectUUID := parseUUID(projectID)
		purgeApprovalEventsForProject(t, projectUUID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_specification_head WHERE project_id=$1`, projectUUID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_specification_revision WHERE id=$1`, revision.ID)
	})
	approveProjectSpecificationFixture(t, projectID, revision)
	return revision
}

func approveProjectPlanFixture(t *testing.T, projectID string, plan projectorchestration.StoredPlan) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO autonomous_project_control (workspace_id, project_id, paused)
		VALUES ($1, $2, FALSE)
		ON CONFLICT DO NOTHING
	`, parseUUID(testWorkspaceID), parseUUID(projectID)); err != nil {
		t.Fatalf("create project control for plan approval fixture: %v", err)
	}
	req := withURLParam(newRequestAs(testUserID, http.MethodPost,
		"/api/projects/"+projectID+"/autonomous/plan/approve",
		map[string]string{"plan_digest": plan.PlanDigest}), "id", projectID)
	w := httptest.NewRecorder()
	testHandler.ApproveProjectAutonomousPlan(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve project plan fixture status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func seedApprovedProjectIssuePlanFixture(t *testing.T, projectID string) {
	t.Helper()
	ctx := context.Background()
	workspaceID, projectUUID := parseUUID(testWorkspaceID), parseUUID(projectID)
	store := projectorchestration.NewStore(testPool)
	spec := projectorchestration.ProjectSpecification{
		Goal:    "Deliver the requested project capability",
		Summary: "Approved project scope for quick-create claim coverage",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-QUICK-CREATE-1", Text: "Create project issues from the approved scope.",
			Rationale: "The project needs agent-created follow-up issues.", Source: "brief",
		}},
		DefinitionOfDone: []string{"Project work remains within the approved specification."},
	}
	revision, err := store.CreateSpecificationRevision(ctx, workspaceID, projectUUID,
		"project_bootstrap", "quick-create-handler-"+uuid.NewString(), spec, true)
	if err != nil {
		t.Fatalf("create quick-create project specification fixture: %v", err)
	}
	t.Cleanup(func() {
		purgeApprovalEventsForProject(t, projectUUID)
		if err := store.CleanupProject(context.Background(), workspaceID, projectUUID); err != nil {
			t.Errorf("clean quick-create project plan fixture: %v", err)
		}
		for _, statement := range []string{
			`DELETE FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`,
			`DELETE FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2`,
			`DELETE FROM autonomous_project_control WHERE workspace_id=$1 AND project_id=$2`,
		} {
			if _, err := testPool.Exec(context.Background(), statement, workspaceID, projectUUID); err != nil {
				t.Errorf("clean quick-create project specification fixture: %v", err)
			}
		}
	})
	approveProjectSpecificationFixture(t, projectID, revision)
	plan, err := store.PersistPlan(ctx, workspaceID, projectUUID, "quick-create-handler-plan-"+uuid.NewString(), "test planner", "test-model",
		projectorchestration.Plan{
			Version:                 projectorchestration.CurrentPlanVersion,
			Goal:                    spec.Goal,
			Specification:           spec.LegacySpecification(),
			SpecificationRevisionID: revision.ID,
			Policy:                  projectorchestration.DefaultPolicy(),
			Nodes: []projectorchestration.NodeSpec{
				{Key: "implement", Kind: projectorchestration.NodeImplementation, Title: "Create follow-up issues", RequirementIDs: []string{"REQ-QUICK-CREATE-1"}, AcceptanceCriteria: []string{"Project issue creation follows the approved scope."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
				{Key: "review", Kind: projectorchestration.NodeReview, Title: "Review project issue flow", RequirementIDs: []string{"REQ-QUICK-CREATE-1"}, AcceptanceCriteria: []string{"Project issue creation is reviewed."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			},
			Edges: []projectorchestration.EdgeSpec{{From: "implement", To: "review", Type: projectorchestration.DependencyHard}},
		})
	if err != nil {
		t.Fatalf("persist quick-create project plan fixture: %v", err)
	}
	approveProjectPlanFixture(t, projectID, plan)
}

func TestApproveProjectAutonomousPlanRequiresExactDigest(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	project := createProjectPermissionTestProject(t, "autonomous plan approval project")
	ctx := context.Background()
	projectSpec := projectorchestration.ProjectSpecification{
		Goal: "test goal", Summary: "Exact-digest plan approval fixture",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-APPROVAL-1", Text: "Implement the approved capability.",
			Rationale: "The project owner requested this capability.", Source: "brief",
		}},
		DefinitionOfDone: []string{"The requested capability is complete."},
	}
	specStore := projectorchestration.NewStore(testPool)
	specRevision, err := specStore.CreateSpecificationRevision(ctx, parseUUID(testWorkspaceID), parseUUID(project.ID),
		"project_bootstrap", "handler-plan-approval-"+uuid.NewString(), projectSpec, true)
	if err != nil {
		t.Fatalf("create current project specification fixture: %v", err)
	}
	approveProjectSpecificationFixture(t, project.ID, specRevision)
	specificationJSON, err := json.Marshal(projectSpec.LegacySpecification())
	if err != nil {
		t.Fatalf("encode plan specification fixture: %v", err)
	}
	specRevisionID := parseUUID(specRevision.ID)
	var planID pgtype.UUID
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_control (workspace_id, project_id, paused)
		VALUES ($1, $2, FALSE)
	`, testWorkspaceID, project.ID); err != nil {
		t.Fatalf("insert project control: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_plan (
			workspace_id, project_id, revision, source_revision, planner_name, goal,
			specification, policy, status, specification_revision_id, plan_digest
		) VALUES ($1, $2, 1, 'test', 'test-planner', $4, $5::jsonb, '{}'::jsonb,
		         'awaiting_approval', $3, repeat('0', 64))
		RETURNING id
	`, testWorkspaceID, project.ID, specRevisionID, projectSpec.Goal, specificationJSON).Scan(&planID); err != nil {
		t.Fatalf("insert awaiting plan: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_plan_node (
			plan_id, workspace_id, project_id, node_key, kind, title, status, risk_level,
			required_capabilities, acceptance_criteria, requirement_ids
		) VALUES ($1, $2, $3, 'implement', 'implementation', 'Implement', 'pending', 'high',
		         '[]'::jsonb, '[]'::jsonb, '[]'::jsonb)
	`, planID, testWorkspaceID, project.ID); err != nil {
		t.Fatalf("insert pending plan node: %v", err)
	}
	t.Cleanup(func() {
		purgeApprovalEventsForProject(t, parseUUID(project.ID))
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_plan_node WHERE project_id = $1`, project.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_plan WHERE project_id = $1`, project.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_specification_head WHERE project_id = $1`, project.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_specification_revision WHERE project_id = $1`, project.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	})
	var digest string
	if err := testPool.QueryRow(ctx, `
		UPDATE autonomous_project_plan
		SET plan_digest = autonomous_project_plan_digest(id)
		WHERE id = $1
		RETURNING plan_digest
	`, planID).Scan(&digest); err != nil {
		t.Fatalf("calculate plan digest: %v", err)
	}
	planStore := projectorchestration.NewStore(testPool)
	if err := planStore.RefreshReady(ctx, parseUUID(testWorkspaceID), parseUUID(project.ID)); err != nil {
		t.Fatalf("refresh unapproved plan: %v", err)
	}
	var nodeStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM autonomous_project_plan_node WHERE plan_id = $1`, planID).Scan(&nodeStatus); err != nil {
		t.Fatalf("read unapproved node readiness: %v", err)
	}
	if nodeStatus != "pending" {
		t.Fatalf("unapproved plan node status = %q, want pending", nodeStatus)
	}

	approve := func(value string) *httptest.ResponseRecorder {
		req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/plan/approve", map[string]string{"plan_digest": value}), "id", project.ID)
		w := httptest.NewRecorder()
		testHandler.ApproveProjectAutonomousPlan(w, req)
		return w
	}
	if w := approve(strings.Repeat("0", 64)); w.Code != http.StatusConflict {
		t.Fatalf("stale digest approval status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var status, approvedBy string
	var approvedAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(approved_by, ''), approved_at
		FROM autonomous_project_plan WHERE id = $1
	`, planID).Scan(&status, &approvedBy, &approvedAt); err != nil {
		t.Fatalf("read plan after stale approval: %v", err)
	}
	if status != "awaiting_approval" || approvedAt.Valid || approvedBy != "" {
		t.Fatalf("stale digest changed plan approval: status=%q approved_at=%v approved_by=%q", status, approvedAt.Valid, approvedBy)
	}
	if w := approve(digest); w.Code != http.StatusOK {
		t.Fatalf("exact digest approval status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(approved_by, ''), approved_at
		FROM autonomous_project_plan WHERE id = $1
	`, planID).Scan(&status, &approvedBy, &approvedAt); err != nil {
		t.Fatalf("read approved plan: %v", err)
	}
	if status != "active" || !approvedAt.Valid || approvedBy != testUserID {
		t.Fatalf("exact digest did not bind approval to admin: status=%q approved_at=%v approved_by=%q", status, approvedAt.Valid, approvedBy)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM autonomous_project_plan_node WHERE plan_id = $1`, planID).Scan(&nodeStatus); err != nil {
		t.Fatalf("read approved node readiness: %v", err)
	}
	if nodeStatus != "ready" {
		t.Fatalf("approved plan node status = %q, want ready", nodeStatus)
	}
	lateIssueID := pgtype.UUID{Valid: true, Bytes: [16]byte{9}}
	if err := projectorchestration.NewStore(testPool).AppendPlanDelta(
		ctx, parseUUID(testWorkspaceID), parseUUID(project.ID), planID,
		[]projectorchestration.NodeSpec{{Key: "late", Kind: projectorchestration.NodeImplementation, Title: "Late node", Risk: projectorchestration.RiskLow}},
		nil, "late", lateIssueID, "", pgtype.UUID{}, "", "", nil,
	); err == nil {
		t.Fatal("approved plan accepted an in-place late node")
	}

	var nextSpecificationRevisionID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_specification_revision (
			workspace_id, project_id, revision, source_kind, goal, specification
		) VALUES ($1, $2, 2, 'change_request', 'replanned goal', '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, project.ID).Scan(&nextSpecificationRevisionID); err != nil {
		t.Fatalf("insert next specification revision: %v", err)
	}
	replanned, err := projectorchestration.NewStore(testPool).PersistPlan(ctx,
		parseUUID(testWorkspaceID), parseUUID(project.ID), "change-request:2", "test-planner", "",
		projectorchestration.Plan{
			Version:                 projectorchestration.CurrentPlanVersion,
			Goal:                    "replanned goal",
			SpecificationRevisionID: nextSpecificationRevisionID.String(),
			Specification: projectorchestration.Specification{
				Summary:          "Replanned work",
				Requirements:     []string{"Implement the replanned work."},
				DefinitionOfDone: []string{"The implementation is independently reviewed."},
				RequirementDetails: []projectorchestration.SpecificationRequirement{{
					ID: "REQ-REPLAN-1", Text: "Implement the replanned work.",
				}},
			},
			Policy: projectorchestration.DefaultPolicy(),
			Nodes: []projectorchestration.NodeSpec{
				{Key: "arch", Kind: projectorchestration.NodeArchitecture, Title: "Architecture", Risk: projectorchestration.RiskLow, MaxAttempts: 2},
				{Key: "implement", Kind: projectorchestration.NodeImplementation, Title: "Implement", RequirementIDs: []string{"REQ-REPLAN-1"}, AcceptanceCriteria: []string{"Implement the replanned work."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
				{Key: "review", Kind: projectorchestration.NodeReview, Title: "Review", RequirementIDs: []string{"REQ-REPLAN-1"}, AcceptanceCriteria: []string{"Independently review the implementation."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			},
			Edges: []projectorchestration.EdgeSpec{
				{From: "arch", To: "implement", Type: projectorchestration.DependencyHard},
				{From: "implement", To: "review", Type: projectorchestration.DependencyHard},
			},
		})
	if err != nil {
		t.Fatalf("persist replan: %v", err)
	}
	if replanned.Status != "awaiting_approval" {
		t.Fatalf("change-request plan status = %q, want awaiting_approval", replanned.Status)
	}
	if w := approve(digest); w.Code != http.StatusConflict {
		t.Fatalf("prior revision digest approval status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM autonomous_project_plan WHERE id = $1`, planID).Scan(&status); err != nil {
		t.Fatalf("read prior plan after replanning: %v", err)
	}
	if status != "superseded" {
		t.Fatalf("prior approved plan status after replan = %q, want superseded", status)
	}
	var nextStatus string
	var nextApprovedAt pgtype.Timestamptz
	var nextApprovedBy string
	if err := testPool.QueryRow(ctx, `
		SELECT status, approved_at, COALESCE(approved_by, '')
		FROM autonomous_project_plan WHERE id = $1
	`, parseUUID(replanned.ID)).Scan(&nextStatus, &nextApprovedAt, &nextApprovedBy); err != nil {
		t.Fatalf("read replanned approval state: %v", err)
	}
	if nextStatus != "awaiting_approval" || nextApprovedAt.Valid || nextApprovedBy != "" {
		t.Fatalf("old digest changed replan approval: status=%q approved_at=%v approved_by=%q", nextStatus, nextApprovedAt.Valid, nextApprovedBy)
	}
}

func TestResolveProductionDeploymentApprovalRequiresCurrentManifestScope(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	exerciseProductionEscalationApprovalLedger(t)
}

func exerciseProductionEscalationApprovalLedger(t *testing.T) {
	t.Helper()
	project := createProjectPermissionTestProject(t, "production deployment approval scope project")
	ctx := context.Background()
	projectUUID, err := uuid.Parse(project.ID)
	if err != nil {
		t.Fatalf("parse test project UUID: %v", err)
	}
	t.Cleanup(func() { purgeApprovalEventsForProject(t, pgtype.UUID{Bytes: [16]byte(projectUUID), Valid: true}) })
	planID := uuid.New()
	nodeID := uuid.New()
	manifestID := uuid.New()
	manifestDigest := newHandlerTestDigest()
	artifactDigest := newHandlerTestDigest()
	planDigest := strings.Repeat("c", 64)
	changeSetID := uuid.New()
	firstRunID, secondRunID := uuid.New(), uuid.New()
	workspaceUUID, err := uuid.Parse(testWorkspaceID)
	if err != nil {
		t.Fatalf("parse test workspace UUID: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_plan (
			id,workspace_id,project_id,revision,source_revision,planner_name,goal,
			policy,plan_digest,status,release_manifest_id,release_manifest_digest
		) VALUES ($1,$2,$3,1,'test','test','production approval test','{}'::jsonb,$4,'active',$5,$6)
	`, planID, testWorkspaceID, project.ID, planDigest, manifestID, manifestDigest); err != nil {
		t.Fatalf("insert production approval plan: %v", err)
	}
	t.Cleanup(func() {
		if err := projectorchestration.NewStore(testPool).CleanupProject(context.Background(),
			pgtype.UUID{Bytes: [16]byte(workspaceUUID), Valid: true},
			pgtype.UUID{Bytes: [16]byte(projectUUID), Valid: true},
		); err != nil {
			t.Errorf("clean production approval test project: %v", err)
		}
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_plan_node (
			id,plan_id,workspace_id,project_id,node_key,kind,title,status
		) VALUES ($1,$2,$3,$4,'deploy','deploy','Production deployment','blocked')
	`, nodeID, planID, testWorkspaceID, project.ID); err != nil {
		t.Fatalf("insert blocked production deployment node: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_release_manifest (
			manifest_id,manifest_digest,workspace_id,project_id,plan_id,change_set_id,
			plan_revision,plan_digest,environment,commit_sha,tree_sha,builder,outputs,
			artifact_digest,artifact_storage_key,quality_gate_run_ids,manifest_json
		) VALUES ($1,$2,$3,$4,$5,$6,1,$7,'production',$8,$9,'go_binary',ARRAY['bin/server']::text[],$10,$11,$12,'{}'::jsonb)
	`, manifestID, manifestDigest, testWorkspaceID, project.ID, planID, changeSetID, planDigest,
		strings.Repeat("d", 40), strings.Repeat("e", 40), artifactDigest,
		"autonomous-project/releases/sha256/"+artifactDigest+".tar", []uuid.UUID{firstRunID, secondRunID}); err != nil {
		t.Fatalf("insert immutable production manifest: %v", err)
	}
	approvalContext, err := json.Marshal(map[string]any{
		"approval_scope": "production_release", "node_key": "deploy",
		"workspace_id": testWorkspaceID, "project_id": project.ID,
		"plan_id": planID.String(), "plan_revision": 1, "node_id": nodeID.String(),
		"target_environment": "production", "manifest_id": manifestID.String(),
		"manifest_digest": manifestDigest,
	})
	if err != nil {
		t.Fatalf("encode production approval context: %v", err)
	}
	var escalationID uuid.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id,project_id,plan_id,node_id,category,status,summary,context
		) VALUES ($1,$2,$3,$4,'approval_required','open','Approve exact production release',$5)
		RETURNING id
	`, testWorkspaceID, project.ID, planID, nodeID, approvalContext).Scan(&escalationID); err != nil {
		t.Fatalf("insert exact production approval escalation: %v", err)
	}
	resolve := func() *httptest.ResponseRecorder {
		req := withURLParams(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/escalations/"+escalationID.String()+"/resolve", map[string]string{"decision": "approved"}),
			"id", project.ID, "escalationId", escalationID.String())
		w := httptest.NewRecorder()
		testHandler.ResolveProjectAutonomousEscalation(w, req)
		return w
	}
	assertOpen := func(label string) {
		t.Helper()
		var status string
		if err := testPool.QueryRow(ctx, "SELECT status FROM autonomous_project_escalation WHERE id=$1", escalationID).Scan(&status); err != nil {
			t.Fatalf("read escalation after %s: %v", label, err)
		}
		if status != "open" {
			t.Fatalf("stale %s approval request changed escalation to %s", label, status)
		}
	}

	if _, err := testPool.Exec(ctx, "UPDATE autonomous_project_plan SET revision=2 WHERE id=$1", planID); err != nil {
		t.Fatalf("stale production approval plan revision: %v", err)
	}
	if w := resolve(); w.Code != http.StatusConflict {
		t.Fatalf("stale plan revision resolution status = %d, want 409: %s", w.Code, w.Body.String())
	}
	assertOpen("plan revision")
	if _, err := testPool.Exec(ctx, "UPDATE autonomous_project_plan SET revision=1 WHERE id=$1", planID); err != nil {
		t.Fatalf("restore production approval plan revision: %v", err)
	}
	changedDigest := strings.Repeat("f", 64)
	if _, err := testPool.Exec(ctx, "UPDATE autonomous_project_plan SET release_manifest_digest=$2 WHERE id=$1", planID, changedDigest); err != nil {
		t.Fatalf("change pinned production manifest digest: %v", err)
	}
	if w := resolve(); w.Code != http.StatusConflict {
		t.Fatalf("changed manifest resolution status = %d, want 409: %s", w.Code, w.Body.String())
	}
	assertOpen("manifest digest")
	var staleApprovalEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_approval_event
		WHERE project_id=$1 AND idempotency_key=$2
	`, parseUUID(project.ID), "escalation-resolution:"+escalationID.String()).Scan(&staleApprovalEvents); err != nil {
		t.Fatalf("count stale production approval ledger events: %v", err)
	}
	if staleApprovalEvents != 0 {
		t.Fatalf("stale production approvals created %d ledger events, want 0", staleApprovalEvents)
	}
	if _, err := testPool.Exec(ctx, "UPDATE autonomous_project_plan SET release_manifest_digest=$2 WHERE id=$1", planID, manifestDigest); err != nil {
		t.Fatalf("restore exact production manifest digest: %v", err)
	}
	if w := resolve(); w.Code != http.StatusOK {
		t.Fatalf("exact production approval resolution status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var status string
	var resolution map[string]any
	if err := testPool.QueryRow(ctx, `SELECT status,resolution FROM autonomous_project_escalation WHERE id=$1`, escalationID).Scan(&status, &resolution); err != nil {
		t.Fatalf("read accepted production approval: %v", err)
	}
	if status != "resolved" || resolution["decision"] != "approved" || resolution["approval_scope"] != "production_release" ||
		resolution["plan_id"] != planID.String() || resolution["manifest_id"] != manifestID.String() || resolution["manifest_digest"] != manifestDigest ||
		resolution["resolved_by"] != testUserID {
		t.Fatalf("accepted approval did not persist exact human scope: status=%s resolution=%#v", status, resolution)
	}
	assertApprovalLedgerEvent(t, pgtype.UUID{Bytes: [16]byte(projectUUID), Valid: true},
		"escalation-resolution:"+escalationID.String(), "escalation", escalationID.String(), manifestDigest,
		"approved", parseUUID(testUserID),
		"Approved the current escalation scope after review")
	var subjectRevision int64
	if err := testPool.QueryRow(ctx, `
		SELECT subject_revision FROM autonomous_project_approval_event
		WHERE project_id=$1 AND idempotency_key=$2
	`, parseUUID(project.ID), "escalation-resolution:"+escalationID.String()).Scan(&subjectRevision); err != nil {
		t.Fatalf("read production approval subject revision: %v", err)
	}
	if subjectRevision != 1 {
		t.Fatalf("production escalation subject revision = %d, want 1", subjectRevision)
	}
}

func newHandlerTestDigest() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "") + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func TestAutonomousRestartHandlerInvokesProjectScopedRecovery(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous restart project")

	previous := testHandler.AutonomousWorkflowRestart
	t.Cleanup(func() { testHandler.AutonomousWorkflowRestart = previous })

	called := false
	testHandler.AutonomousWorkflowRestart = func(ctx context.Context, workspaceID, projectID pgtype.UUID) error {
		called = true
		if uuidToString(workspaceID) != testWorkspaceID {
			t.Fatalf("restart workspace = %s, want %s", workspaceID.String(), testWorkspaceID)
		}
		if uuidToString(projectID) != project.ID {
			t.Fatalf("restart project = %s, want %s", projectID.String(), project.ID)
		}
		return nil
	}

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/workflow/restart", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.RestartProjectAutonomousWorkflow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("restart status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("restart endpoint did not invoke runtime recovery")
	}
}

func TestAutonomousRestartReportsPlanApprovalRequired(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous restart approval project")

	previous := testHandler.AutonomousWorkflowRestart
	t.Cleanup(func() { testHandler.AutonomousWorkflowRestart = previous })
	testHandler.AutonomousWorkflowRestart = func(context.Context, pgtype.UUID, pgtype.UUID) error {
		return projectorchestration.ErrPlanApprovalRequired
	}

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/workflow/restart", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.RestartProjectAutonomousWorkflow(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("restart status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode restart error: %v", err)
	}
	if body["code"] != "plan_approval_required" {
		t.Fatalf("restart error code = %q, want plan_approval_required", body["code"])
	}
}

func TestAutonomousPauseRepairsStaleControlWorkspaceOwnership(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous stale control ownership project")
	_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	})

	var staleWorkspaceID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT gen_random_uuid()`).Scan(&staleWorkspaceID); err != nil {
		t.Fatalf("generate stale workspace id: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO autonomous_project_control (project_id, workspace_id, paused)
		VALUES ($1, $2, FALSE)
	`, project.ID, staleWorkspaceID); err != nil {
		t.Fatalf("seed stale control row: %v", err)
	}

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/pause", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.PauseProjectAutonomous(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var workspaceID pgtype.UUID
	var paused bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT workspace_id, paused
		FROM autonomous_project_control
		WHERE project_id = $1
	`, project.ID).Scan(&workspaceID, &paused); err != nil {
		t.Fatalf("read repaired control state: %v", err)
	}
	if uuidToString(workspaceID) != testWorkspaceID {
		t.Fatalf("control workspace = %s, want %s", uuidToString(workspaceID), testWorkspaceID)
	}
	if !paused {
		t.Fatal("repaired control row did not persist paused=true")
	}
}

func TestAutonomousRestartReturnsStableRepairStageCode(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous restart diagnostic project")

	previous := testHandler.AutonomousWorkflowRestart
	t.Cleanup(func() { testHandler.AutonomousWorkflowRestart = previous })
	testHandler.AutonomousWorkflowRestart = func(context.Context, pgtype.UUID, pgtype.UUID) error {
		return errors.New("refresh project readiness after restart: injected failure")
	}

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/workflow/restart", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.RestartProjectAutonomousWorkflow(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("restart status = %d, want 500: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode restart error: %v", err)
	}
	if got := body["code"]; got != "autonomous_repair_readiness_failed" {
		t.Fatalf("repair error code = %q, want autonomous_repair_readiness_failed", got)
	}
}

func TestProjectPauseCancelsRunningProjectTask(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous pause cancellation project")
	issueID := dbfx.Issue(t, "running autonomous project task")
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET project_id=$1 WHERE id=$2`, project.ID, issueID); err != nil {
		t.Fatalf("attach issue to project: %v", err)
	}
	runtimeID := createHandlerTestRuntime(t, "autonomous pause cancellation runtime")
	agentID := createHandlerTestAgentForRuntime(t, "autonomous pause cancellation agent", []byte(`{}`), runtimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue SET issue_id=$1, status='running', started_at=now() WHERE id=$2
	`, issueID, taskID); err != nil {
		t.Fatalf("make project task active: %v", err)
	}

	var planID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO autonomous_project_plan (
			workspace_id, project_id, revision, source_revision, planner_name, goal, status, plan_digest
		) VALUES ($1,$2,1,'pause-test','test','pause test','active',repeat('0', 64)) RETURNING id
	`, testWorkspaceID, project.ID).Scan(&planID); err != nil {
		t.Fatalf("create project plan: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO autonomous_project_plan_node (
			plan_id, workspace_id, project_id, node_key, kind, title, status, materialized_issue_id
		) VALUES ($1,$2,$3,'implementation-1','implementation','Implementation','running',$4)
	`, planID, testWorkspaceID, project.ID, issueID); err != nil {
		t.Fatalf("create materialized plan node: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE autonomous_project_plan SET plan_digest = autonomous_project_plan_digest(id) WHERE id = $1
	`, planID); err != nil {
		t.Fatalf("calculate project plan digest: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_plan_node WHERE plan_id=$1`, planID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_plan WHERE id=$1`, planID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_control WHERE project_id=$1`, project.ID)
	})

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/pause", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.PauseProjectAutonomous(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_task_queue WHERE id=$1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read cancelled task state: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("task status after project pause = %q, want cancelled", status)
	}
}

func TestProjectCancelIsTerminalIdempotentAndCancelsRunningTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	project := createProjectPermissionTestProject(t, "autonomous terminal cancellation project")
	issueID := dbfx.Issue(t, "running autonomous project task")
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET project_id=$1 WHERE id=$2`, project.ID, issueID); err != nil {
		t.Fatalf("attach issue to project: %v", err)
	}
	runtimeID := createHandlerTestRuntime(t, "autonomous cancellation runtime")
	agentID := createHandlerTestAgentForRuntime(t, "autonomous cancellation agent", []byte(`{}`), runtimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue SET issue_id=$1, status='running', started_at=now() WHERE id=$2
	`, issueID, taskID); err != nil {
		t.Fatalf("make project task active: %v", err)
	}

	var planID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO autonomous_project_plan (
			workspace_id, project_id, revision, source_revision, planner_name, goal, status, plan_digest
		) VALUES ($1,$2,1,'cancel-test','test','cancel test','active',repeat('0', 64)) RETURNING id
	`, testWorkspaceID, project.ID).Scan(&planID); err != nil {
		t.Fatalf("create project plan: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO autonomous_project_plan_node (
			plan_id, workspace_id, project_id, node_key, kind, title, status, materialized_issue_id
		) VALUES ($1,$2,$3,'implementation-1','implementation','Implementation','running',$4)
	`, planID, testWorkspaceID, project.ID, issueID); err != nil {
		t.Fatalf("create materialized plan node: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE autonomous_project_plan SET plan_digest = autonomous_project_plan_digest(id) WHERE id = $1
	`, planID); err != nil {
		t.Fatalf("calculate project plan digest: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_plan_node WHERE plan_id=$1`, planID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_plan WHERE id=$1`, planID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_control WHERE project_id=$1`, project.ID)
	})

	var firstCancelledAt pgtype.Timestamptz
	for attempt := 0; attempt < 2; attempt++ {
		req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/cancel", nil), "id", project.ID)
		w := httptest.NewRecorder()
		testHandler.CancelProjectAutonomous(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("cancel attempt %d status = %d, want 200: %s", attempt+1, w.Code, w.Body.String())
		}
		var cancelledAt pgtype.Timestamptz
		if err := testPool.QueryRow(context.Background(), `SELECT cancelled_at FROM autonomous_project_control WHERE project_id=$1`, project.ID).Scan(&cancelledAt); err != nil {
			t.Fatalf("read terminal cancellation state: %v", err)
		}
		if attempt == 0 {
			firstCancelledAt = cancelledAt
		} else if !firstCancelledAt.Time.Equal(cancelledAt.Time) {
			t.Fatal("repeated cancellation changed the original cancellation time")
		}
	}

	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_task_queue WHERE id=$1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read cancelled task state: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("task status after project cancellation = %q, want cancelled", status)
	}

	resumeReq := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/resume", nil), "id", project.ID)
	resumeW := httptest.NewRecorder()
	testHandler.ResumeProjectAutonomous(resumeW, resumeReq)
	if resumeW.Code != http.StatusConflict {
		t.Fatalf("resume status after terminal cancellation = %d, want 409: %s", resumeW.Code, resumeW.Body.String())
	}
	replanReq := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/replan", nil), "id", project.ID)
	replanW := httptest.NewRecorder()
	testHandler.ReplanProjectAutonomous(replanW, replanReq)
	if replanW.Code != http.StatusConflict {
		t.Fatalf("replan status after terminal cancellation = %d, want 409: %s", replanW.Code, replanW.Body.String())
	}
	restartReq := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/workflow/restart", nil), "id", project.ID)
	restartW := httptest.NewRecorder()
	testHandler.RestartProjectAutonomousWorkflow(restartW, restartReq)
	if restartW.Code != http.StatusConflict {
		t.Fatalf("restart status after terminal cancellation = %d, want 409: %s", restartW.Code, restartW.Body.String())
	}
}
