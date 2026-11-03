package projectorchestration

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAutonomousProjectPlanExecutableRequiresApprovedSpecification(t *testing.T) {
	fixture := newWave3ChangeFixture(t)
	registerPlanApprovalLedgerCleanup(t, fixture)
	var executable bool
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
		t.Fatalf("read pending specification execution gate: %v", err)
	}
	if executable {
		t.Fatal("plan executed without a specification approval")
	}
	ready, err := fixture.store.ListReadyNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 10)
	if err != nil {
		t.Fatalf("list ready nodes for pending specification: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("ready nodes for pending specification = %d, want 0", len(ready))
	}
	if err := fixture.store.BindNodeIssue(fixture.ctx, fixture.workspaceID, fixture.projectID, "implementation", wave3UUID(t, fixture.issueID), "", pgtype.UUID{}); err == nil {
		t.Fatal("binding succeeded for a plan with a pending specification")
	}

	insertExecutionSpecificationApproval(t, fixture, "imported", "approved")
	planned, err := fixture.store.ListPlanNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 10)
	if err != nil {
		t.Fatalf("list plan projection for imported specification approval: %v", err)
	}
	if len(planned) != 0 {
		t.Fatalf("plan projection for imported specification approval = %d, want 0", len(planned))
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
		t.Fatalf("read imported specification execution gate: %v", err)
	}
	if executable {
		t.Fatal("imported specification approval opened plan execution")
	}

	insertExecutionSpecificationApproval(t, fixture, "local", "approved")
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
		t.Fatalf("read approved specification execution gate: %v", err)
	}
	if !executable {
		t.Fatal("latest local human approval for the current specification did not open plan execution")
	}

	insertExecutionSpecificationApproval(t, fixture, "local", "rejected")
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
		t.Fatalf("read rejected specification execution gate: %v", err)
	}
	if executable {
		t.Fatal("latest specification rejection did not close plan execution")
	}

	var newerRevisionID pgtype.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO autonomous_project_specification_revision (
			workspace_id,project_id,revision,source_kind,source_ref,goal,specification
		) SELECT $1,$2,COALESCE(MAX(revision),0)+1,'change_request',$3,'Revised project scope',
		         '{"summary":"Revised scope","requirements":[{"id":"REQ-1","text":"Implement the requested capability","rationale":"The project needs this capability","source":"brief"}]}'::jsonb
		  FROM autonomous_project_specification_revision
		 WHERE workspace_id=$1 AND project_id=$2
		 RETURNING id
	`, fixture.workspaceID, fixture.projectID, "execution-gate-revision-"+uuid.NewString()).Scan(&newerRevisionID); err != nil {
		t.Fatalf("insert newer current specification: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_specification_head
		SET specification_revision_id=$3,revision=(SELECT revision FROM autonomous_project_specification_revision WHERE id=$3),updated_at=now()
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID, newerRevisionID); err != nil {
		t.Fatalf("advance current specification head: %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
		t.Fatalf("read stale specification execution gate: %v", err)
	}
	if executable {
		t.Fatal("revising the specification left the old plan approval executable")
	}
	t.Log("AUTONOMOUS_SPECIFICATION_EXECUTABLE_GATE_PASS")
}

func TestApprovedPlanInvalidatedByNewSpecificationHead(t *testing.T) {
	fixture := newWave3ChangeFixture(t)
	registerPlanApprovalLedgerCleanup(t, fixture)
	insertHumanSpecificationApproval(t, fixture)

	ready, err := fixture.store.ListReadyNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 10)
	if err != nil {
		t.Fatalf("list ready nodes for approved plan: %v", err)
	}
	if len(ready) != 1 || ready[0].Key != "implementation" {
		t.Fatalf("ready nodes for approved plan = %v, want implementation as a positive control", ready)
	}

	newRevision, err := fixture.store.CreateSpecificationRevision(
		fixture.ctx,
		fixture.workspaceID,
		fixture.projectID,
		"change_request",
		"t5-ready-node-head-advance",
		ProjectSpecification{
			Goal:    "Deliver the revised project",
			Summary: "Revised project scope with unchanged requirements",
			Requirements: []SpecificationRequirement{{
				ID: "REQ-1", Text: "Implement the requested capability", Rationale: "The project needs this capability", Source: "brief",
			}},
			DefinitionOfDone: []string{"Implementation and review coverage are complete"},
		},
		true,
	)
	if err != nil {
		t.Fatalf("advance project specification head: %v", err)
	}
	if newRevision.ID == fixture.initialSpecificationID {
		t.Fatal("specification head did not advance to a new revision")
	}

	ready, err = fixture.store.ListReadyNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 10)
	if err != nil {
		t.Fatalf("list ready nodes after specification head advanced: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("ready nodes after specification head advanced = %v, want none", ready)
	}
	t.Log("T5_SPEC_REVISION_INVALIDATES_READY_NODES_GATE_PASS")
}

func TestAutonomousPlanExecutableRejectsChangedSpecificationFields(t *testing.T) {
	mutations := []struct {
		name  string
		path  []string
		value string
		goal  bool
	}{
		{name: "goal", goal: true, value: "Changed project goal"},
		{name: "summary", path: []string{"summary"}, value: `"Changed project summary"`},
		{name: "requirements", path: []string{"requirement_details", "0", "rationale"}, value: `"Changed requirement rationale"`},
		{name: "legacy requirements projection", path: []string{"requirements"}, value: `["Changed legacy requirement text"]`},
		{name: "non-functional requirements", path: []string{"non_functional_requirements"}, value: `["Changed reliability requirement"]`},
		{name: "constraints", path: []string{"constraints"}, value: `["Changed project constraint"]`},
		{name: "non-goals", path: []string{"non_goals"}, value: `["Changed project non-goal"]`},
		{name: "definition of done", path: []string{"definition_of_done"}, value: `["Changed completion condition"]`},
		{name: "acceptance expectations", path: []string{"acceptance_expectations"}, value: `["Changed acceptance expectation"]`},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := newWave3ChangeFixture(t)
			registerPlanApprovalLedgerCleanup(t, fixture)
			insertExecutionSpecificationApproval(t, fixture, "local", "approved")

			var executable bool
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
				t.Fatalf("read unchanged approved plan execution gate: %v", err)
			}
			if !executable {
				t.Fatal("unchanged approved plan is not an executable positive control")
			}

			if mutation.goal {
				if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan SET goal=$2 WHERE id=$1::uuid`, fixture.initialPlan.ID, mutation.value); err != nil {
					t.Fatalf("change plan goal: %v", err)
				}
			} else if _, err := fixture.pool.Exec(fixture.ctx, `
				UPDATE autonomous_project_plan
				SET specification=jsonb_set(specification,$2::text[],$3::jsonb,true)
				WHERE id=$1::uuid
			`, fixture.initialPlan.ID, mutation.path, mutation.value); err != nil {
				t.Fatalf("change plan %s: %v", mutation.name, err)
			}
			if _, err := fixture.pool.Exec(fixture.ctx, `
				UPDATE autonomous_project_plan SET plan_digest=autonomous_project_plan_digest(id) WHERE id=$1::uuid
			`, fixture.initialPlan.ID); err != nil {
				t.Fatalf("recompute changed plan digest: %v", err)
			}
			var digestValid bool
			if err := fixture.pool.QueryRow(fixture.ctx, `
				SELECT plan_digest=autonomous_project_plan_digest(id)
				FROM autonomous_project_plan WHERE id=$1::uuid
			`, fixture.initialPlan.ID).Scan(&digestValid); err != nil {
				t.Fatalf("verify changed plan digest: %v", err)
			}
			if !digestValid {
				t.Fatal("changed plan digest is not current; executable check would not isolate specification matching")
			}
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
				t.Fatalf("read changed %s execution gate: %v", mutation.name, err)
			}
			if executable {
				t.Fatalf("plan with changed %s remained executable despite a current digest", mutation.name)
			}
		})
	}
	t.Log("FULL_SPEC_EXECUTABLE_GATE_PASS")
}

func insertExecutionSpecificationApproval(t *testing.T, fixture wave3ChangeFixture, origin, decision string) {
	t.Helper()
	var revision int64
	var digest string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT revision, autonomous_project_specification_digest(id)
		FROM autonomous_project_specification_revision WHERE id=$1::uuid
	`, fixture.initialSpecificationID).Scan(&revision, &digest); err != nil {
		t.Fatalf("load specification approval subject: %v", err)
	}
	actorID := pgtype.UUID{}
	actorType := "human"
	if origin == "local" {
		actorID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	}
	_, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_approval_event (
			workspace_id,project_id,idempotency_key,subject_type,subject_id,subject_revision,
			subject_digest,decision,actor_type,actor_id,actor_display,rationale,source_endpoint,origin,acted_at
		) VALUES ($1,$2,$3,'specification_revision',$4,$5,$6,$7,$8,$9,$10,$11,'/test/specification/decision',$12,now()+interval '1 second')
	`, fixture.workspaceID, fixture.projectID, "test:execution-gate:"+uuid.NewString(), fixture.initialSpecificationID,
		revision, digest, decision, actorType, actorID, "test approver", "execution gate regression", origin)
	if err != nil {
		t.Fatalf("insert %s %s specification event: %v", origin, decision, err)
	}
}
