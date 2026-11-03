package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

func TestProjectPlanApprovalRequiresApprovedSpecification(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, decision := range []string{"pending", "rejected"} {
		t.Run(decision, func(t *testing.T) {
			ctx := context.Background()
			project := createProjectPermissionTestProject(t, "plan approval specification gate")
			workspaceID := parseUUID(testWorkspaceID)
			projectID := parseUUID(project.ID)
			store := projectorchestration.NewStore(testPool)
			spec := projectorchestration.ProjectSpecification{
				Goal: "Deliver an approved project", Summary: "Current project scope",
				Requirements: []projectorchestration.SpecificationRequirement{{
					ID: "REQ-1", Text: "Deliver the capability", Rationale: "Required by the project owner", Source: "brief",
				}},
				DefinitionOfDone: []string{"The requested capability is delivered"},
			}
			revision, err := store.CreateSpecificationRevision(ctx, workspaceID, projectID, "project_bootstrap", "gate-"+uuid.NewString(), spec, true)
			if err != nil {
				t.Fatalf("create current specification: %v", err)
			}
			plan := projectorchestration.Plan{
				Version: projectorchestration.CurrentPlanVersion, Goal: spec.Goal,
				Specification: spec.LegacySpecification(), SpecificationRevisionID: revision.ID,
				Policy: projectorchestration.DefaultPolicy(),
				Nodes: []projectorchestration.NodeSpec{{
					Key: "implementation", Kind: projectorchestration.NodeImplementation, Title: "Implement capability",
					Description: "Deliver the approved capability", RequirementIDs: []string{"REQ-1"},
					AcceptanceCriteria: []string{"Capability works"}, Risk: projectorchestration.RiskLow, MaxAttempts: 3,
				}, {
					Key: "review", Kind: projectorchestration.NodeReview, Title: "Review capability",
					Description: "Verify the delivered capability", RequirementIDs: []string{"REQ-1"},
					AcceptanceCriteria: []string{"Capability is verified"}, Risk: projectorchestration.RiskLow, MaxAttempts: 3,
				}},
				Edges: []projectorchestration.EdgeSpec{{From: "implementation", To: "review", Type: projectorchestration.DependencyHard}},
			}
			stored, err := store.PersistPlan(ctx, workspaceID, projectID, "spec-gate-"+uuid.NewString(), "test planner", "test-model", plan)
			if err != nil {
				t.Fatalf("persist pending plan: %v", err)
			}
			if decision == "rejected" {
				var revisionNumber int64
				var digest string
				if err := testPool.QueryRow(ctx, `SELECT revision,autonomous_project_specification_digest($1::uuid) FROM autonomous_project_specification_revision WHERE id=$1::uuid`, revision.ID).Scan(&revisionNumber, &digest); err != nil {
					t.Fatalf("read specification decision subject: %v", err)
				}
				if _, err := testPool.Exec(ctx, `
					INSERT INTO autonomous_project_approval_event (
						workspace_id,project_id,idempotency_key,subject_type,subject_id,subject_revision,subject_digest,
						decision,actor_type,actor_id,actor_display,rationale,source_endpoint,origin
					) VALUES ($1,$2,$3,'specification_revision',$4,$5,$6,'rejected','human',$7,'test owner','Rejected current scope','/test/specification/reject','local')
				`, workspaceID, projectID, "plan-spec-gate:"+uuid.NewString(), revision.ID, revisionNumber, digest,
					pgtype.UUID{Bytes: uuid.MustParse(testUserID), Valid: true}); err != nil {
					t.Fatalf("record current specification rejection: %v", err)
				}
			}
			t.Cleanup(func() {
				if _, err := testPool.Exec(ctx, `DELETE FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID); err != nil {
					t.Errorf("delete specification-gated test plan: %v", err)
				}
				purgeApprovalEventsForProject(t, projectID)
			})

			req := withURLParam(newRequestAs(testUserID, http.MethodPost, "/api/projects/"+project.ID+"/autonomous/plan/approve", map[string]string{"plan_digest": stored.PlanDigest}), "id", project.ID)
			w := httptest.NewRecorder()
			testHandler.ApproveProjectAutonomousPlan(w, req)
			if w.Code != http.StatusConflict {
				t.Fatalf("plan approval status = %d, want 409: %s", w.Code, w.Body.String())
			}
			var status string
			var approvedAt pgtype.Timestamptz
			if err := testPool.QueryRow(ctx, `SELECT status,approved_at FROM autonomous_project_plan WHERE id=$1::uuid`, stored.ID).Scan(&status, &approvedAt); err != nil {
				t.Fatalf("read plan after rejected approval: %v", err)
			}
			if status != "awaiting_approval" || approvedAt.Valid {
				t.Fatalf("plan after rejected approval = (%q, approved_at valid %v), want pending and inactive", status, approvedAt.Valid)
			}
		})
	}
	t.Log("PROJECT_PLAN_APPROVAL_SPECIFICATION_GATE_PASS")
}

func TestStartProjectPlanningRequiresApprovedSpecification(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	project := createProjectPermissionTestProject(t, "planning start specification gate")
	workspaceID := parseUUID(testWorkspaceID)
	projectID := parseUUID(project.ID)
	store := projectorchestration.NewStore(testPool)
	spec := projectorchestration.ProjectSpecification{
		Goal: "Deliver an approved project", Summary: "Current project scope",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-1", Text: "Deliver the capability", Rationale: "Required by the project owner", Source: "brief",
		}},
		DefinitionOfDone: []string{"The requested capability is delivered"},
	}
	if _, err := store.CreateSpecificationRevision(ctx, workspaceID, projectID, "project_bootstrap", "planning-start-"+uuid.NewString(), spec, true); err != nil {
		t.Fatalf("create current specification: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_team_draft (
			project_id,workspace_id,plan,planner_name,status,confirmed_at,confirmed_by
		) VALUES ($1,$2,'{}'::jsonb,'test planner','applied',now(),$3)
	`, projectID, workspaceID, pgtype.UUID{Bytes: uuid.MustParse(testUserID), Valid: true}); err != nil {
		t.Fatalf("seed applied team draft: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_team_draft WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID)
		purgeApprovalEventsForProject(t, projectID)
	})

	req := withURLParam(newRequestAs(testUserID, http.MethodPost, "/api/projects/"+project.ID+"/autonomous/planning/start", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.StartProjectAutonomousPlanning(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("planning start status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var startedAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `SELECT continuation_started_at FROM autonomous_project_team_draft WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID).Scan(&startedAt); err != nil {
		t.Fatalf("read planning continuation state: %v", err)
	}
	if startedAt.Valid {
		t.Fatal("planning continuation started without current specification approval")
	}
	var startEvents int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_approval_event WHERE workspace_id=$1 AND project_id=$2 AND subject_type='planning_start'`, workspaceID, projectID).Scan(&startEvents); err != nil {
		t.Fatalf("count planning-start approvals: %v", err)
	}
	if startEvents != 0 {
		t.Fatalf("planning-start approvals = %d, want 0", startEvents)
	}
	t.Log("PROJECT_PLANNING_START_SPECIFICATION_GATE_PASS")
}
