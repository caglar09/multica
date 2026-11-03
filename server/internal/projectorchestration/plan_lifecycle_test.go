package projectorchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRequiresHumanPlanApproval(t *testing.T) {
	base := Plan{Nodes: []NodeSpec{{Risk: RiskLow}}}
	if !requiresHumanPlanApproval(base, "planner", "plan:1", 1) {
		t.Fatal("first plan must require human approval even when every node is low risk")
	}
	if requiresHumanPlanApproval(base, "planner", "plan:2", 2) {
		t.Fatal("low-risk replan under the existing policy unexpectedly requires human approval")
	}
	if !requiresHumanPlanApproval(base, "change_request", "plan:2", 2) {
		t.Fatal("scope-change plan must require human approval")
	}
	if !requiresHumanPlanApproval(base, "planner", "change-request:abc", 2) {
		t.Fatal("change-request-derived plan must require human approval")
	}
	highRisk := base
	highRisk.Nodes = []NodeSpec{{Risk: RiskHigh}}
	if !requiresHumanPlanApproval(highRisk, "planner", "plan:2", 2) {
		t.Fatal("high-risk plan must require human approval")
	}
	migration := base
	migration.Nodes = []NodeSpec{{Risk: RiskLow, Kind: NodeMigration}}
	if !requiresHumanPlanApproval(migration, "planner", "plan:2", 2) {
		t.Fatal("database migration must require human approval even if a plan weakens policy")
	}
	deploy := base
	deploy.Nodes = []NodeSpec{{Risk: RiskLow, Kind: NodeDeploy}}
	if !requiresHumanPlanApproval(deploy, "planner", "plan:2", 2) {
		t.Fatal("production deployment must require human approval even if a plan weakens policy")
	}
}

func TestFirstLowRiskPlanRemainsAwaitingApproval(t *testing.T) {
	fixture := newWave3ChangeFixture(t)
	if fixture.initialPlan.Revision != 1 {
		t.Fatalf("fixture plan revision = %d, want first revision 1", fixture.initialPlan.Revision)
	}
	if len(fixture.initialPlan.Plan.Nodes) == 0 {
		t.Fatal("fixture first plan has no nodes")
	}
	for _, node := range fixture.initialPlan.Plan.Nodes {
		if node.Risk != RiskLow || node.Kind == NodeMigration || node.Kind == NodeDeploy {
			t.Fatalf("fixture first plan node %q is not low-risk: kind=%q risk=%q", node.Key, node.Kind, node.Risk)
		}
	}

	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin first plan lifecycle finalization: %v", err)
	}
	defer tx.Rollback(fixture.ctx)
	lifecycle, err := FinalizePlanLifecycleTx(fixture.ctx, tx, wave3UUID(t, fixture.initialPlan.ID), fixture.initialPlan.Plan)
	if err != nil {
		t.Fatalf("finalize first low-risk plan lifecycle: %v", err)
	}
	if lifecycle.Status != "awaiting_approval" {
		t.Fatalf("first low-risk plan lifecycle status = %q, want awaiting_approval", lifecycle.Status)
	}
	if lifecycle.ApprovedAt != nil || lifecycle.ApprovedBy != "" {
		t.Fatalf("pending first plan has approval metadata: approved_at=%v approved_by=%q", lifecycle.ApprovedAt, lifecycle.ApprovedBy)
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Fatalf("commit first plan lifecycle finalization: %v", err)
	}

	var status string
	var approvedAt pgtype.Timestamptz
	var approvedBy pgtype.Text
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT status,approved_at,approved_by
		FROM autonomous_project_plan WHERE id=$1::uuid
	`, fixture.initialPlan.ID).Scan(&status, &approvedAt, &approvedBy); err != nil {
		t.Fatalf("read persisted first plan lifecycle: %v", err)
	}
	if status != "awaiting_approval" || approvedAt.Valid || approvedBy.Valid {
		t.Fatalf("persisted first plan lifecycle = status %q approved_at_valid %v approved_by_valid %v, want awaiting_approval with no approval metadata", status, approvedAt.Valid, approvedBy.Valid)
	}
	t.Log("T4_FIRST_LOW_RISK_PLAN_AWAITING_APPROVAL_GATE_PASS")
}

func TestApprovePlanTxWritesApprovalLedger(t *testing.T) {
	fixture := newWave3ChangeFixtureWithApprovedSpecification(t)
	plan := fixture.initialPlan.Plan
	plan.Nodes = append([]NodeSpec(nil), plan.Nodes...)
	plan.Nodes[0].Risk = RiskHigh
	stored, err := fixture.store.PersistPlan(fixture.ctx, fixture.workspaceID, fixture.projectID, "plan-approval-ledger-"+uuid.NewString(), "planner", "test-model", plan)
	if err != nil {
		t.Fatalf("persist pending replan: %v", err)
	}
	if stored.Status != "awaiting_approval" {
		t.Fatalf("high-risk replan status = %q, want awaiting_approval", stored.Status)
	}

	actorID := uuid.New()
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin plan approval: %v", err)
	}
	defer tx.Rollback(fixture.ctx)
	approved, err := ApprovePlanTx(fixture.ctx, tx, fixture.workspaceID, fixture.projectID, stored.PlanDigest, actorID.String())
	if err != nil {
		t.Fatalf("approve current plan: %v", err)
	}
	if !approved {
		t.Fatal("current pending plan was not approved")
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Fatalf("commit plan approval: %v", err)
	}

	var count int64
	err = fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND subject_type='plan' AND subject_id=$3
		  AND actor_type='human'
	`, fixture.workspaceID, fixture.projectID, stored.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count plan approval events: %v", err)
	}
	if count != 1 {
		t.Fatalf("plan approval event count = %d, want 1", count)
	}
	var actor, actorDisplay, subjectID, subjectDigest, decision, rationale, origin string
	var subjectRevision int64
	err = fixture.pool.QueryRow(fixture.ctx, `
		SELECT actor_id::text, actor_display, subject_id, subject_digest, decision, rationale, origin, subject_revision
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND subject_type='plan' AND subject_id=$3
		  AND actor_type='human'
	`, fixture.workspaceID, fixture.projectID, stored.ID).Scan(&actor, &actorDisplay, &subjectID, &subjectDigest, &decision, &rationale, &origin, &subjectRevision)
	if err != nil {
		t.Fatalf("read plan approval event: %v", err)
	}
	if actor != actorID.String() || actorDisplay != actor || subjectID != stored.ID || subjectDigest != stored.PlanDigest || decision != "approved" || rationale == "" || origin != "local" || subjectRevision != stored.Revision {
		t.Fatalf("plan approval ledger = count %d, actor %q, actor display %q, subject %q, digest %q, decision %q, rationale %q, origin %q, revision %d", count, actor, actorDisplay, subjectID, subjectDigest, decision, rationale, origin, subjectRevision)
	}
	t.Log("PLAN_APPROVAL_LEDGER_GATE_PASS")
}

func TestFinalizePlanLifecycleWritesApprovalLedger(t *testing.T) {
	passed := true
	passed = t.Run("approved unchanged head auto-activates once", func(t *testing.T) {
		fixture := newWave3ChangeFixture(t)
		registerPlanApprovalLedgerCleanup(t, fixture)
		insertHumanSpecificationApproval(t, fixture)
		stored := persistLowRiskReplan(t, fixture, false)
		if stored.Status != "active" {
			t.Fatalf("approved unchanged replan status = %q, want active", stored.Status)
		}
		var count int64
		if err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT count(*)
			FROM autonomous_project_approval_event
			WHERE workspace_id=$1 AND project_id=$2 AND subject_type='plan' AND subject_id=$3
		`, fixture.workspaceID, fixture.projectID, stored.ID).Scan(&count); err != nil {
			t.Fatalf("read system plan approval event: %v", err)
		}
		if count != 1 {
			t.Fatalf("system plan approval event count = %d, want 1", count)
		}
		var actorType, actorDisplay, policyRef, subjectDigest string
		if err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT actor_type, actor_display, policy_ref, subject_digest
			FROM autonomous_project_approval_event
			WHERE workspace_id=$1 AND project_id=$2 AND subject_type='plan' AND subject_id=$3
		`, fixture.workspaceID, fixture.projectID, stored.ID).Scan(&actorType, &actorDisplay, &policyRef, &subjectDigest); err != nil {
			t.Fatalf("read system plan approval event fields: %v", err)
		}
		if actorType != "system" || actorDisplay != "system_policy_engine" || policyRef != "low_risk_replan_under_approved_spec" || subjectDigest != stored.PlanDigest {
			t.Fatalf("system approval ledger = count %d, actor %q, actor display %q, policy %q, digest %q", count, actorType, actorDisplay, policyRef, subjectDigest)
		}
	}) && passed

	passed = t.Run("unapproved spec remains pending", func(t *testing.T) {
		fixture := newWave3ChangeFixture(t)
		registerPlanApprovalLedgerCleanup(t, fixture)
		stored := persistLowRiskReplan(t, fixture, false)
		assertReplanRemainsPendingWithoutSystemEvent(t, fixture, stored)
	}) && passed

	passed = t.Run("changed rich requirements remain pending", func(t *testing.T) {
		fixture := newWave3ChangeFixture(t)
		registerPlanApprovalLedgerCleanup(t, fixture)
		insertHumanSpecificationApproval(t, fixture)
		stored := persistLowRiskReplan(t, fixture, true)
		assertReplanRemainsPendingWithoutSystemEvent(t, fixture, stored)
	}) && passed

	if !passed {
		return
	}
	t.Log("PLAN_APPROVAL_LEDGER_GATE_PASS")
}

func TestLowRiskReplanCannotAutoApproveChangedSpecificationFields(t *testing.T) {
	fields := []string{
		"goal",
		"summary",
		"requirements",
		"non-functional requirements",
		"constraints",
		"non-goals",
		"definition of done",
		"acceptance expectations",
		"legacy requirements projection",
	}
	passed := true
	for _, field := range fields {
		passed = t.Run(field, func(t *testing.T) {
			fixture := newWave3ChangeFixture(t)
			registerPlanApprovalLedgerCleanup(t, fixture)
			insertHumanSpecificationApproval(t, fixture)

			plan := fixture.initialPlan.Plan
			switch field {
			case "goal":
				plan.Goal = "Changed project goal"
			case "summary":
				plan.Specification.Summary = "Changed project summary"
			case "requirements":
				plan.Specification.RequirementDetails = append([]SpecificationRequirement(nil), plan.Specification.RequirementDetails...)
				plan.Specification.RequirementDetails[0].Rationale = "Changed requirement rationale"
			case "non-functional requirements":
				plan.Specification.NonFunctionalRequirements = []string{"Changed reliability requirement"}
			case "constraints":
				plan.Specification.Constraints = []string{"Changed project constraint"}
			case "non-goals":
				plan.Specification.NonGoals = []string{"Changed project non-goal"}
			case "definition of done":
				plan.Specification.DefinitionOfDone = []string{"Changed completion condition"}
			case "acceptance expectations":
				plan.Specification.AcceptanceExpectations = []string{"Changed acceptance expectation"}
			case "legacy requirements projection":
				plan.Specification.Requirements = []string{"Changed legacy requirement text"}
			}

			stored, err := fixture.store.PersistPlan(
				fixture.ctx,
				fixture.workspaceID,
				fixture.projectID,
				"full-specification-replan-"+uuid.NewString(),
				"planner",
				"test-model",
				plan,
			)
			if field == "legacy requirements projection" {
				if !errors.Is(err, ErrInvalidRequirementCoverage) {
					t.Fatalf("persist changed legacy requirement projection error = %v, want invalid requirement coverage", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("persist low-risk replan with changed %s: %v", field, err)
			}
			assertReplanRemainsPendingWithoutSystemEvent(t, fixture, stored)
		}) && passed
	}
	if !passed {
		return
	}
	t.Log("FULL_SPEC_AUTO_APPROVAL_GATE_PASS")
}

func persistLowRiskReplan(t *testing.T, fixture wave3ChangeFixture, changedRequirements bool) StoredPlan {
	t.Helper()
	plan := fixture.initialPlan.Plan
	if changedRequirements {
		plan.Specification.RequirementDetails = append([]SpecificationRequirement(nil), plan.Specification.RequirementDetails...)
		plan.Specification.RequirementDetails[0].Rationale += " changed"
	}
	stored, err := fixture.store.PersistPlan(fixture.ctx, fixture.workspaceID, fixture.projectID, "approved-spec-replan-"+uuid.NewString(), "planner", "test-model", plan)
	if err != nil {
		t.Fatalf("persist low-risk replan: %v", err)
	}
	return stored
}

func insertHumanSpecificationApproval(t *testing.T, fixture wave3ChangeFixture) {
	t.Helper()
	var revision int64
	var digest string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT revision, autonomous_project_specification_digest(id)
		FROM autonomous_project_specification_revision WHERE id=$1::uuid
	`, fixture.initialSpecificationID).Scan(&revision, &digest); err != nil {
		t.Fatalf("load specification approval subject: %v", err)
	}
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin human specification approval: %v", err)
	}
	defer tx.Rollback(fixture.ctx)
	if err := RecordApprovalTx(fixture.ctx, tx, ApprovalEvent{
		WorkspaceID:     fixture.workspaceID,
		ProjectID:       fixture.projectID,
		IdempotencyKey:  "test:specification-approval:" + uuid.NewString(),
		SubjectType:     "specification_revision",
		SubjectID:       fixture.initialSpecificationID,
		SubjectRevision: pgtype.Int8{Int64: revision, Valid: true},
		SubjectDigest:   digest,
		Decision:        "approved",
		ActorType:       "human",
		ActorID:         pgtype.UUID{Bytes: uuid.New(), Valid: true},
		ActorDisplay:    "test approver",
		Rationale:       "Test approval for unchanged specification requirements",
		SourceEndpoint:  "/test/specification/approve",
		Origin:          "local",
	}); err != nil {
		t.Fatalf("record human specification approval: %v", err)
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Fatalf("commit human specification approval: %v", err)
	}
}

func assertReplanRemainsPendingWithoutSystemEvent(t *testing.T, fixture wave3ChangeFixture, stored StoredPlan) {
	t.Helper()
	if stored.Status != "awaiting_approval" {
		t.Fatalf("ineligible replan status = %q, want awaiting_approval", stored.Status)
	}
	var count int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND subject_type='plan' AND subject_id=$3 AND actor_type='system'
	`, fixture.workspaceID, fixture.projectID, stored.ID).Scan(&count); err != nil {
		t.Fatalf("count ineligible system plan approvals: %v", err)
	}
	if count != 0 {
		t.Fatalf("ineligible replan has %d system approvals, want none", count)
	}
}

func registerPlanApprovalLedgerCleanup(t *testing.T, fixture wave3ChangeFixture) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		tx, err := fixture.pool.Begin(ctx)
		if err != nil {
			t.Errorf("begin approval-ledger fixture cleanup: %v", err)
			return
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SET LOCAL multica.approval_purge='on'`); err != nil {
			t.Errorf("enable approval-ledger fixture cleanup: %v", err)
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM autonomous_project_approval_event WHERE workspace_id=$1`, fixture.workspaceID); err != nil {
			t.Errorf("delete approval-ledger fixture events: %v", err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit approval-ledger fixture cleanup: %v", err)
		}
	})
}
