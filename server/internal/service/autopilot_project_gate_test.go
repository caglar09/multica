package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/entitlement"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAutopilotProjectIssueCreationRequiresExecutablePlan(t *testing.T) {
	ctx := context.Background()
	fixture := newAutopilotQuotaFixture(t, entitlement.ActionEnforce, 1)
	projectID, specification := seedAutopilotProjectIssueGate(t, fixture)

	ap := db.Autopilot{
		ID:            fixture.autopilotID,
		WorkspaceID:   fixture.workspaceID,
		ProjectID:     projectID,
		ExecutionMode: "create_issue",
		AssigneeType:  "agent",
		AssigneeID:    fixture.agentID,
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autopilot
		SET execution_mode='create_issue',project_id=$1
		WHERE id=$2
	`, projectID, fixture.autopilotID); err != nil {
		t.Fatalf("bind project to issue-creating autopilot: %v", err)
	}

	executable, err := fixture.service.projectIssueCreationPlanExecutable(ctx, ap)
	if err != nil {
		t.Fatalf("check unapproved project plan: %v", err)
	}
	if executable {
		t.Fatal("project issue creation was allowed before local specification approval")
	}

	run, err := fixture.service.DispatchAutopilot(ctx, ap, pgtype.UUID{}, "api", []byte(`{"test":true}`))
	if err != nil {
		t.Fatalf("dispatch closed project autopilot: %v", err)
	}
	if run.Status != "skipped" {
		t.Fatalf("closed project run status = %q, want skipped", run.Status)
	}
	if run.IssueID.Valid || run.TaskID.Valid {
		t.Fatalf("closed project run linked issue=%v task=%v", run.IssueID.Valid, run.TaskID.Valid)
	}
	if run.FailureReason.String != "project specification and plan are not approved for execution" {
		t.Fatalf("closed project skip reason = %q", run.FailureReason.String)
	}

	var projectIssueCount, projectTaskCount, quotaPeriodCount, quotaReservationCount int64
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE project_id=$1`, projectID).Scan(&projectIssueCount); err != nil {
		t.Fatalf("count project issues: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue tq
		JOIN issue i ON i.id=tq.issue_id
		WHERE i.project_id=$1
	`, projectID).Scan(&projectTaskCount); err != nil {
		t.Fatalf("count project task dispatches: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM autopilot_quota_period WHERE workspace_id=$1`, fixture.workspaceID).Scan(&quotaPeriodCount); err != nil {
		t.Fatalf("count autopilot quota periods: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM autopilot_quota_reservation WHERE workspace_id=$1`, fixture.workspaceID).Scan(&quotaReservationCount); err != nil {
		t.Fatalf("count autopilot quota reservations: %v", err)
	}
	if projectIssueCount != 0 || projectTaskCount != 0 || quotaPeriodCount != 0 || quotaReservationCount != 0 {
		t.Fatalf("closed project side effects: issues=%d tasks=%d quota_periods=%d quota_reservations=%d; want all zero", projectIssueCount, projectTaskCount, quotaPeriodCount, quotaReservationCount)
	}

	approveAutopilotProjectSpecification(t, fixture, projectID, specification)
	executable, err = fixture.service.projectIssueCreationPlanExecutable(ctx, ap)
	if err != nil {
		t.Fatalf("check locally approved project plan: %v", err)
	}
	if !executable {
		t.Fatal("local human approval did not open the project execution gate")
	}

	retargetID, _ := seedAutopilotProjectIssueGate(t, fixture)
	lateRun, err := fixture.service.Queries.CreateAutopilotRun(ctx, db.CreateAutopilotRunParams{
		AutopilotID:    fixture.autopilotID,
		Source:         "api",
		Status:         "running",
		TriggerPayload: []byte(`{"test":true}`),
		ID:             util.MustParseUUID(uuid.NewString()),
	})
	if err != nil {
		t.Fatalf("create run for late project-gate check: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE autopilot SET project_id=$1 WHERE id=$2`, retargetID, fixture.autopilotID); err != nil {
		t.Fatalf("retarget autopilot after admission: %v", err)
	}
	err = fixture.service.dispatchCreateIssue(ctx, ap, &lateRun, "UTC", pgtype.UUID{})
	var skipped *errDispatchSkipped
	if !errors.As(err, &skipped) || skipped.reason != "project specification and plan are not approved for execution" {
		t.Fatalf("dispatch after unapproved retarget = %v, want project-gate skip", err)
	}
	var retargetIssueCount int64
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE project_id=$1`, retargetID).Scan(&retargetIssueCount); err != nil {
		t.Fatalf("count issues for unapproved retarget: %v", err)
	}
	if retargetIssueCount != 0 {
		t.Fatalf("created %d issue(s) for an unapproved retarget", retargetIssueCount)
	}

	t.Log("AUTOPILOT_PROJECT_ISSUE_GATE_PASS")
}

func seedAutopilotProjectIssueGate(t *testing.T, fixture *autopilotQuotaFixture) (pgtype.UUID, projectorchestration.SpecificationRevision) {
	t.Helper()
	ctx := context.Background()
	projectIDString := testutil.New(fixture.pool, util.UUIDToString(fixture.workspaceID), util.UUIDToString(fixture.publisherID)).Project(t, "Autopilot issue gate")
	projectID := util.MustParseUUID(projectIDString)
	store := projectorchestration.NewStore(fixture.pool)
	spec := projectorchestration.ProjectSpecification{
		Goal:    "Deliver the requested project",
		Summary: "Project scope requiring local approval",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-1", Text: "Implement the capability", Rationale: "The project needs this capability", Source: "brief",
		}},
		DefinitionOfDone: []string{"The capability works and is reviewed"},
	}
	revision, err := store.CreateSpecificationRevision(ctx, fixture.workspaceID, projectID, "project_bootstrap", "autopilot-gate-"+uuid.NewString(), spec, true)
	if err != nil {
		t.Fatalf("create project specification: %v", err)
	}
	plan := projectorchestration.Plan{
		Version:                 projectorchestration.CurrentPlanVersion,
		Goal:                    spec.Goal,
		Specification:           spec.LegacySpecification(),
		SpecificationRevisionID: revision.ID,
		Policy:                  projectorchestration.DefaultPolicy(),
		Nodes: []projectorchestration.NodeSpec{
			{
				Key: "implementation", Kind: projectorchestration.NodeImplementation, Title: "Implement capability",
				Description: "Implement the requested capability", RequirementIDs: []string{"REQ-1"},
				AcceptanceCriteria: []string{"The capability works"}, Risk: projectorchestration.RiskLow, MaxAttempts: 3,
			},
			{
				Key: "review", Kind: projectorchestration.NodeReview, Title: "Review capability",
				Description: "Review the implemented capability", RequirementIDs: []string{"REQ-1"},
				AcceptanceCriteria: []string{"The capability has been reviewed"}, Risk: projectorchestration.RiskLow, MaxAttempts: 3,
			},
		},
		Edges: []projectorchestration.EdgeSpec{{From: "implementation", To: "review", Type: projectorchestration.DependencyHard}},
	}
	stored, err := store.PersistPlan(ctx, fixture.workspaceID, projectID, "autopilot-gate-plan-"+uuid.NewString(), "Project Manager", "test-model", plan)
	if err != nil {
		t.Fatalf("persist project plan: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status='active',plan_digest=autonomous_project_plan_digest(id),
		    approved_at=now(),approved_by='fixture human'
		WHERE id=$1::uuid
	`, stored.ID); err != nil {
		t.Fatalf("activate fixture project plan: %v", err)
	}
	registerAutopilotProjectIssueGateCleanup(t, fixture, projectID)
	return projectID, revision
}

func approveAutopilotProjectSpecification(t *testing.T, fixture *autopilotQuotaFixture, projectID pgtype.UUID, revision projectorchestration.SpecificationRevision) {
	t.Helper()
	ctx := context.Background()
	var digest string
	if err := fixture.pool.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1::uuid)`, revision.ID).Scan(&digest); err != nil {
		t.Fatalf("load project specification digest: %v", err)
	}
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin project specification approval: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID:     fixture.workspaceID,
		ProjectID:       projectID,
		IdempotencyKey:  "autopilot-gate-approval-" + uuid.NewString(),
		SubjectType:     "specification_revision",
		SubjectID:       revision.ID,
		SubjectRevision: pgtype.Int8{Int64: revision.Revision, Valid: true},
		SubjectDigest:   digest,
		Decision:        "approved",
		ActorType:       "human",
		ActorID:         fixture.publisherID,
		ActorDisplay:    "Autopilot project gate test",
		Rationale:       "Positive control for local human project approval",
		SourceEndpoint:  "/test/autopilot/project-issue-gate",
		Origin:          "local",
	}); err != nil {
		t.Fatalf("record project specification approval: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit project specification approval: %v", err)
	}
}

func registerAutopilotProjectIssueGateCleanup(t *testing.T, fixture *autopilotQuotaFixture, projectID pgtype.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := fixture.pool.Exec(ctx, `UPDATE autopilot SET project_id=NULL WHERE project_id=$1`, projectID); err != nil {
			t.Errorf("unlink autopilot project fixture: %v", err)
			return
		}
		tx, err := fixture.pool.Begin(ctx)
		if err != nil {
			t.Errorf("begin project approval fixture cleanup: %v", err)
			return
		}
		if _, err := tx.Exec(ctx, `SET LOCAL multica.approval_purge='on'`); err != nil {
			_ = tx.Rollback(ctx)
			t.Errorf("enable project approval fixture cleanup: %v", err)
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_approval_event WHERE workspace_id=$1 AND project_id=$2`, fixture.workspaceID, projectID); err != nil {
			_ = tx.Rollback(ctx)
			t.Errorf("delete project approval fixture events: %v", err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit project approval fixture cleanup: %v", err)
			return
		}
		for _, table := range []string{
			"autonomous_project_plan_node",
			"autonomous_project_plan",
			"autonomous_project_specification_head",
			"autonomous_project_specification_revision",
		} {
			if _, err := fixture.pool.Exec(ctx, "DELETE FROM "+table+" WHERE workspace_id=$1 AND project_id=$2", fixture.workspaceID, projectID); err != nil {
				t.Errorf("delete %s fixture rows: %v", table, err)
				return
			}
		}
		if _, err := fixture.pool.Exec(ctx, `DELETE FROM project WHERE id=$1`, projectID); err != nil {
			t.Errorf("delete project fixture: %v", err)
		}
	})
}
