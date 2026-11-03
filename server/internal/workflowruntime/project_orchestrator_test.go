package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/entitlement"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/teamprovision"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/prometheus/client_golang/prometheus"
)

type projectPlanMutationTxStarter struct {
	pool   *pgxpool.Pool
	planID pgtype.UUID
}

type countingProjectPlanExecutor struct{ calls int }

type issueCreateEntryCounter struct{ calls int }

func (c *issueCreateEntryCounter) Gate(context.Context, uuid.UUID, entitlement.GateName) entitlement.Decision {
	c.calls++
	return entitlement.Decision{}
}

func (e *countingProjectPlanExecutor) ExecuteProjectPlan(context.Context, projectorchestration.PlanningInput, string, string) (projectorchestration.RuntimeExecution, error) {
	e.calls++
	return projectorchestration.RuntimeExecution{}, errors.New("planner must not be invoked")
}

func (s *projectPlanMutationTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE autonomous_project_plan SET status='blocked' WHERE id=$1`, s.planID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func recordLocalSpecificationApprovalForRuntimeFixture(t *testing.T, fixture *autonomousDeploymentBridgeFixture, revisionID pgtype.UUID, revision int64) string {
	t.Helper()
	return recordLocalProjectSpecificationApproval(
		t, fixture.pool, fixture.ctx, fixture.workspaceID, fixture.projectID,
		d2ParseUUID(t, fixture.ownerID), revisionID, revision, fixture.ownerID,
	)
}

func recordLocalProjectSpecificationApproval(t *testing.T, pool *pgxpool.Pool, ctx context.Context, workspaceID, projectID, actorID, revisionID pgtype.UUID, revision int64, actorDisplay string) string {
	t.Helper()
	var digest string
	if err := pool.QueryRow(ctx, `SELECT autonomous_project_specification_digest($1)`, revisionID).Scan(&digest); err != nil {
		t.Fatalf("read specification digest for local approval: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin local specification approval: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID:     workspaceID,
		ProjectID:       projectID,
		IdempotencyKey:  "workflowruntime-spec-approval:" + uuid.NewString(),
		SubjectType:     "specification_revision",
		SubjectID:       d2UUIDText(revisionID),
		SubjectRevision: pgtype.Int8{Int64: revision, Valid: true},
		SubjectDigest:   digest,
		Decision:        "approved",
		ActorType:       "human",
		ActorID:         actorID,
		ActorDisplay:    actorDisplay,
		Rationale:       "Approve the exact current project specification for the runtime regression fixture.",
		SourceEndpoint:  "/test/workflowruntime/specification/approve",
		Origin:          "local",
	}); err != nil {
		t.Fatalf("record local specification approval: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit local specification approval: %v", err)
	}
	return digest
}

func approveCurrentRuntimePlan(t *testing.T, fixture *autonomousDeploymentBridgeFixture, digest string) {
	t.Helper()
	approveCurrentProjectPlan(t, fixture.pool, fixture.ctx, fixture.workspaceID, fixture.projectID, d2ParseUUID(t, fixture.ownerID), fixture.ownerID, digest)
}

func approveCurrentProjectPlan(t *testing.T, pool *pgxpool.Pool, ctx context.Context, workspaceID, projectID, actorID pgtype.UUID, actorDisplay, digest string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_control (project_id,workspace_id,paused,updated_at)
		VALUES ($1,$2,FALSE,now())
		ON CONFLICT (project_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id,updated_at=now()
	`, projectID, workspaceID); err != nil {
		t.Fatalf("ensure project control row before exact plan approval: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin exact plan approval: %v", err)
	}
	defer tx.Rollback(ctx)
	approved, err := projectorchestration.ApprovePlanTx(ctx, tx, workspaceID, projectID, digest, actorDisplay)
	if err != nil {
		t.Fatalf("approve exact current plan: %v", err)
	}
	if !approved {
		t.Fatal("exact current plan digest was not approved")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit exact plan approval: %v", err)
	}
}

func TestChangeSetTaskMetadata(t *testing.T) {
	ids := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	result := []byte(`{"branch_name":"task/issue-1","worktree_base_sha":"base","worktree_commit_sha":"commit","worktree_changed_files":["server/main.go"],"worktree_path":"/tmp/worktree","worktree_repo_path":"/repo"}`)
	commit, err := taskCommitFromResult(result, ids, ids, ids, ids, ids, ids, ids)
	if err != nil {
		t.Fatalf("decode valid task commit: %v", err)
	}
	if commit.BranchName != "task/issue-1" || commit.BaseSHA != "base" || commit.CommitSHA != "commit" ||
		commit.Worktree != "/tmp/worktree" || commit.RepoPath != "/repo" || len(commit.ChangedFiles) != 1 ||
		commit.ChangedFiles[0] != "server/main.go" {
		t.Fatalf("decoded task commit = %+v", commit)
	}

	for name, input := range map[string]string{
		"missing": `{"output":"done"}`,
		"partial": `{"branch_name":"task/issue-1","worktree_commit_sha":"commit"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := taskCommitFromResult([]byte(input), ids, ids, ids, ids, ids, ids, ids); err == nil {
				t.Fatal("incomplete task commit metadata was accepted")
			}
		})
	}
}

func TestPlanningReconcilerRetryDelay(t *testing.T) {
	var previousUpper time.Duration
	for attempts := int32(1); attempts <= 3; attempts++ {
		lower := time.Duration(30*(1<<attempts)) * time.Second
		upper := lower + 30*time.Second
		if previousUpper != 0 && lower < previousUpper {
			t.Fatalf("attempt %d lower bound %s overlaps prior upper bound %s", attempts, lower, previousUpper)
		}
		for i := 0; i < 20; i++ {
			delay := projectPlanningRetryDelay(attempts)
			if delay < lower || delay >= upper {
				t.Fatalf("attempt %d retry delay = %s, want [%s, %s)", attempts, delay, lower, upper)
			}
		}
		previousUpper = upper
	}
}

func TestPlanningReconcilerClaimBoundAndLeaseGrace(t *testing.T) {
	if projectPlanningClaimLimit != 1 {
		t.Fatalf("planning claim limit = %d, want one project per reconciler call", projectPlanningClaimLimit)
	}
	if projectPlanningDeadline != 5*time.Minute {
		t.Fatalf("planning deadline = %s, want 5m", projectPlanningDeadline)
	}
	if projectPlanningLease <= projectPlanningDeadline {
		t.Fatalf("planning lease = %s, want cleanup grace after deadline %s", projectPlanningLease, projectPlanningDeadline)
	}
}

func TestPlanProjectRevisionRequiresApprovedSpecification(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	store := projectorchestration.NewStore(fixture.pool)
	projectFixture := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID)
	projectID := d2ParseUUID(t, projectFixture.Project(t, "Plan requires approved specification"))
	t.Cleanup(func() {
		if err := store.CleanupProject(context.Background(), fixture.workspaceID, projectID); err != nil {
			t.Errorf("clean approved specification gate project: %v", err)
		}
	})
	proposal := projectorchestration.ProjectLeaderSpecificationProposal{
		ProposalID: "plan-gate-" + uuid.NewString(),
		Rationale:  "The project request needs explicit owner review before planning.",
		Specification: projectorchestration.ProjectSpecification{
			Goal:    "Deliver a reviewed project plan",
			Summary: "The PM proposal must remain pending until a human approves it.",
			Requirements: []projectorchestration.SpecificationRequirement{{
				ID: "REQ-001", Text: "Planning waits for an approved specification.",
				Rationale: "The plan must follow owner-approved scope.", Source: "project brief",
			}},
			DefinitionOfDone: []string{"The owner can approve the specification before planning."},
		},
	}
	change, err := store.RecordProjectLeaderSpecificationProposal(ctx, fixture.workspaceID, projectID, proposal)
	if err != nil {
		t.Fatalf("record pending project manager specification proposal: %v", err)
	}
	if change.State != projectorchestration.ChangeApprovalRequired {
		t.Fatalf("proposal state = %q, want approval_required", change.State)
	}
	specificationRevisionID := d2ParseUUID(t, change.ProposedSpecificationRevisionID)
	var specificationRevision int64
	if err := fixture.pool.QueryRow(ctx, `SELECT revision FROM autonomous_project_specification_revision WHERE id=$1`, specificationRevisionID).Scan(&specificationRevision); err != nil {
		t.Fatalf("read pending specification revision: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_specification_head(workspace_id,project_id,specification_revision_id,revision)
		VALUES($1,$2,$3,$4)
	`, fixture.workspaceID, projectID, specificationRevisionID, specificationRevision); err != nil {
		t.Fatalf("set specification as current head: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_approval_event(
			workspace_id,project_id,idempotency_key,subject_type,subject_id,subject_revision,
			subject_digest,decision,actor_type,actor_display,source_endpoint,origin
		) VALUES($1,$2,$3,'specification_revision',$4,$5,
			autonomous_project_specification_digest($6),'approved','human','Imported approver','bundle import','imported')
	`, fixture.workspaceID, projectID, "imported-spec-approval-"+uuid.NewString(), change.ProposedSpecificationRevisionID, specificationRevision, specificationRevisionID); err != nil {
		t.Fatalf("record imported approval for current specification head: %v", err)
	}
	executor := &countingProjectPlanExecutor{}
	runtime := &Runtime{
		pool:           fixture.pool,
		projectStore:   store,
		projectPlanner: projectorchestration.NewPlanner(executor, projectorchestration.DefaultMaxNodes),
	}
	if err := runtime.planProjectRevision(ctx, fixture.workspaceID, projectID, "pending-spec-test"); err != nil {
		t.Fatalf("planning while specification awaits approval: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("planner calls = %d, want 0 while specification awaits approval", executor.calls)
	}
	var planCount, revisionCount, escalationCount int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2`, fixture.workspaceID, projectID).Scan(&planCount); err != nil {
		t.Fatalf("count plans after waiting for specification approval: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2`, fixture.workspaceID, projectID).Scan(&revisionCount); err != nil {
		t.Fatalf("count specification revisions after waiting for approval: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_escalation WHERE workspace_id=$1 AND project_id=$2`, fixture.workspaceID, projectID).Scan(&escalationCount); err != nil {
		t.Fatalf("count scheduler escalations after waiting for approval: %v", err)
	}
	if planCount != 0 || revisionCount != 1 || escalationCount != 0 {
		t.Fatalf("plan/revision/escalation counts = %d/%d/%d, want 0/1/0", planCount, revisionCount, escalationCount)
	}
	var state string
	var escalationID pgtype.UUID
	if err := fixture.pool.QueryRow(ctx, `SELECT state,approval_escalation_id FROM autonomous_project_change_request WHERE id=$1`, d2ParseUUID(t, change.ID)).Scan(&state, &escalationID); err != nil {
		t.Fatalf("read pending specification proposal after planning wait: %v", err)
	}
	if state != string(projectorchestration.ChangeApprovalRequired) || escalationID.Valid {
		t.Fatalf("pending proposal state/escalation = %q/%v, want approval_required/no escalation", state, escalationID.Valid)
	}
	t.Log("PROJECT_PLAN_REQUIRES_APPROVED_SPECIFICATION_GATE_PASS")
}

func TestPlanningStartWithoutSpecification(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	store := projectorchestration.NewStore(fixture.pool)
	projectFixture := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID)
	projectID := d2ParseUUID(t, projectFixture.Project(t, "Planning start without specification"))
	t.Cleanup(func() {
		if err := store.CleanupProject(context.Background(), fixture.workspaceID, projectID); err != nil {
			t.Errorf("clean no-specification planning project: %v", err)
		}
	})
	var revisionsBefore int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM autonomous_project_specification_revision
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, projectID).Scan(&revisionsBefore); err != nil {
		t.Fatalf("count specification revisions before planning: %v", err)
	}
	if revisionsBefore != 0 {
		t.Fatalf("specification revisions before planning = %d, want no specification", revisionsBefore)
	}

	executor := &countingProjectPlanExecutor{}
	runtime := &Runtime{
		pool:           fixture.pool,
		projectStore:   store,
		projectPlanner: projectorchestration.NewPlanner(executor, projectorchestration.DefaultMaxNodes),
	}
	if err := runtime.planProjectRevision(ctx, fixture.workspaceID, projectID, "no-spec-planning-start"); err != nil {
		t.Fatalf("planning without a specification: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("planner calls without a specification = %d, want 0", executor.calls)
	}
	var revisionsAfter int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM autonomous_project_specification_revision
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, projectID).Scan(&revisionsAfter); err != nil {
		t.Fatalf("count specification revisions after planning: %v", err)
	}
	if revisionsAfter != 0 {
		t.Fatalf("planning created %d specification revisions without a specification", revisionsAfter)
	}
	t.Log("T1_NO_SPEC_PLANNING_START_GATE_PASS")
}

func TestPlanningLeasePreservesExistingPlanStatuses(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status='awaiting_approval', approved_at=NULL, approved_by=NULL
		WHERE id=$1
	`, fixture.planID); err != nil {
		t.Fatalf("set plan awaiting approval: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_team_draft (
			project_id, workspace_id, plan, planner_name, status, confirmed_at,
			continuation_started_at, planning_attempts, planning_lease_owner, planning_lease_until
		) VALUES ($1, $2, '{}'::jsonb, 'test', 'applied', now(), now(), 1, 'expired-lease', now()-interval '1 second')
	`, fixture.projectID, fixture.workspaceID); err != nil {
		t.Fatalf("insert expired planning lease: %v", err)
	}
	store := projectorchestration.NewStore(fixture.pool)
	runtime := &Runtime{
		pool:           fixture.pool,
		projectStore:   store,
		projectPlanner: projectorchestration.NewPlanner(nil, projectorchestration.DefaultMaxNodes),
		team:           &teamprovision.Provisioner{},
	}
	if err := runtime.processProjectPlanning(ctx); err != nil {
		t.Fatalf("reconcile planning lease without replacing approval plan: %v", err)
	}
	var status string
	if err := fixture.pool.QueryRow(ctx, `SELECT status FROM autonomous_project_plan WHERE id=$1`, fixture.planID).Scan(&status); err != nil {
		t.Fatalf("read existing plan status: %v", err)
	}
	if status != "awaiting_approval" {
		t.Fatalf("existing plan status = %q, want awaiting_approval", status)
	}
	var completedAt pgtype.Timestamptz
	var owner pgtype.Text
	if err := fixture.pool.QueryRow(ctx, `
		SELECT continuation_completed_at, planning_lease_owner
		FROM autonomous_project_team_draft WHERE project_id=$1 AND workspace_id=$2
	`, fixture.projectID, fixture.workspaceID).Scan(&completedAt, &owner); err != nil {
		t.Fatalf("read expired planning lease recovery: %v", err)
	}
	if !completedAt.Valid || owner.Valid {
		t.Fatalf("expired planning lease recovery = completed:%v owner:%v, want completed and cleared owner", completedAt.Valid, owner.Valid)
	}
}

func TestEnsureMaterializedProjectNodeAssignmentRepairsImportedIssue(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	projectFixture := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID)
	runtimeID := projectFixture.Runtime(t, "assignment repair runtime")
	agentIDText := projectFixture.Agent(t, "Mobile Engineer", runtimeID)
	agentID := d2ParseUUID(t, agentIDText)
	issueIDText := projectFixture.Issue(t, "Imported task", testutil.Cols{"project_id": fixture.projectIDText})
	issueID := d2ParseUUID(t, issueIDText)

	teamID := pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
	squadID := pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
	teamPlan, err := json.Marshal(teamprovision.Plan{Roles: []teamprovision.RoleSpec{{Role: "mobile_engineer", Family: "backend"}}})
	if err != nil {
		t.Fatalf("encode project team plan: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_team (
			id, workspace_id, project_id, squad_id, intent, plan,
			created_by_agent_id, owner_user_id, status
		) VALUES ($1,$2,$3,$4,'test', $5, $6, $7, 'active')
	`, teamID, fixture.workspaceID, fixture.projectID, squadID, teamPlan, agentID, d2ParseUUID(t, fixture.ownerID)); err != nil {
		t.Fatalf("insert project team: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_team_member (team_id, role, agent_id, role_family)
		VALUES ($1,'mobile_engineer',$2,'backend')
	`, teamID, agentID); err != nil {
		t.Fatalf("insert project team member: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET kind='implementation', title='Imported task', status='ready', materialized_issue_id=$2
		WHERE id=$1
	`, fixture.nodeID, issueID); err != nil {
		t.Fatalf("materialize imported project node: %v", err)
	}
	approveAutonomousDeploymentBridgeFixturePlan(t, fixture)

	runtime := &Runtime{
		pool: fixture.pool, projectStore: projectorchestration.NewStore(fixture.pool),
		team: teamprovision.New(fixture.pool, db.New(fixture.pool)), taskSvc: &service.TaskService{Queries: db.New(fixture.pool)},
	}
	node := projectorchestration.PlannedNode{
		ReadyNode: projectorchestration.ReadyNode{ID: d2UUIDText(fixture.nodeID), Key: "deploy", Kind: projectorchestration.NodeImplementation},
		Status:    "ready", MaterializedIssueID: issueIDText,
	}
	changed, err := runtime.ensureMaterializedProjectNodeAssignment(ctx, fixture.workspaceID, fixture.projectID, node)
	if err != nil {
		t.Fatalf("repair imported task assignment: %v", err)
	}
	if !changed {
		t.Fatal("assignment repair reported no change")
	}
	var issueAssignee, planAssignee pgtype.UUID
	var issueAssigneeType, planRole string
	if err := fixture.pool.QueryRow(ctx, `
		SELECT i.assignee_id, i.assignee_type, n.assigned_agent_id, n.assigned_role
		FROM issue i JOIN autonomous_project_plan_node n ON n.materialized_issue_id=i.id
		WHERE i.id=$1 AND n.id=$2
	`, issueID, fixture.nodeID).Scan(&issueAssignee, &issueAssigneeType, &planAssignee, &planRole); err != nil {
		t.Fatalf("read repaired issue and plan assignment: %v", err)
	}
	if issueAssignee != agentID || planAssignee != agentID || issueAssigneeType != "agent" || planRole != "mobile_engineer" {
		t.Fatalf("issue assignment=(%q,%v), plan assignment=(%q,%q), want the project agent", issueAssigneeType, issueAssignee, planAssignee, planRole)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE issue SET assignee_type='member', assignee_id=$2, revision=revision+1 WHERE id=$1
	`, issueID, d2ParseUUID(t, fixture.ownerID)); err != nil {
		t.Fatalf("simulate a later human assignment: %v", err)
	}
	if _, err := runtime.ensureMaterializedProjectNodeAssignment(ctx, fixture.workspaceID, fixture.projectID, node); err == nil {
		t.Fatal("human assignment was silently replaced by the project team")
	}
	var preservedAssigneeType string
	var preservedAssigneeID pgtype.UUID
	if err := fixture.pool.QueryRow(ctx, `SELECT assignee_type, assignee_id FROM issue WHERE id=$1`, issueID).Scan(&preservedAssigneeType, &preservedAssigneeID); err != nil {
		t.Fatalf("read preserved human assignment: %v", err)
	}
	if preservedAssigneeType != "member" || preservedAssigneeID != d2ParseUUID(t, fixture.ownerID) {
		t.Fatalf("assignment after repair attempt = (%q,%v), want the human assignment", preservedAssigneeType, preservedAssigneeID)
	}
}

type recordingAutonomousIssueAnalytics struct {
	events []analytics.Event
}

func (a *recordingAutonomousIssueAnalytics) Capture(event analytics.Event) {
	a.events = append(a.events, event)
}

func (*recordingAutonomousIssueAnalytics) Close() {}

func TestAutonomousProjectIssueCreatorProvenance(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	projectFixture := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID)
	runtimeID := projectFixture.Runtime(t, "autonomous issue creator runtime")
	agentID := d2ParseUUID(t, projectFixture.Agent(t, "Mobile Engineer", runtimeID))

	teamID := pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
	squadID := pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
	teamPlan, err := json.Marshal(teamprovision.Plan{Roles: []teamprovision.RoleSpec{{Role: "mobile_engineer", Family: "backend"}}})
	if err != nil {
		t.Fatalf("encode project team plan: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_team (
			id,workspace_id,project_id,squad_id,intent,plan,
			created_by_agent_id,owner_user_id,status
		) VALUES ($1,$2,$3,$4,'test',$5,$6,$7,'active')
	`, teamID, fixture.workspaceID, fixture.projectID, squadID, teamPlan, agentID, d2ParseUUID(t, fixture.ownerID)); err != nil {
		t.Fatalf("insert autonomous project team: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_team_member (team_id,role,agent_id,role_family)
		VALUES ($1,'mobile_engineer',$2,'backend')
	`, teamID, agentID); err != nil {
		t.Fatalf("insert autonomous project team member: %v", err)
	}

	specification := projectorchestration.ProjectSpecification{
		Goal:    "Create an autonomous project node issue.",
		Summary: "Create an autonomous project node issue.",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-AUTONOMOUS-ISSUE-CREATOR", Text: "Use the selected autonomous agent as issue creator.",
			Rationale: "Issue history must identify the agent responsible for autonomous work.", Source: "project brief",
		}},
		DefinitionOfDone: []string{"The created issue records the autonomous agent as its creator."},
	}
	encodedSpecification, err := json.Marshal(specification)
	if err != nil {
		t.Fatalf("encode project specification: %v", err)
	}
	encodedPlanSpecification, err := json.Marshal(specification.LegacySpecification())
	if err != nil {
		t.Fatalf("encode plan specification: %v", err)
	}
	var specificationRevisionID, planID, nodeID pgtype.UUID
	var specificationRevision int64
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(revision),0)+1
		FROM autonomous_project_specification_revision
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID).Scan(&specificationRevision); err != nil {
		t.Fatalf("allocate project specification revision: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_specification_revision (
			workspace_id,project_id,revision,source_kind,goal,specification
		) VALUES ($1,$2,$3,'planner','Create an autonomous project node issue.',$4)
		RETURNING id
	`, fixture.workspaceID, fixture.projectID, specificationRevision, encodedSpecification).Scan(&specificationRevisionID); err != nil {
		t.Fatalf("insert project specification revision: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_specification_head (
			workspace_id,project_id,specification_revision_id,revision
		) VALUES ($1,$2,$3,$4)
		ON CONFLICT (workspace_id,project_id) DO UPDATE
		SET specification_revision_id=EXCLUDED.specification_revision_id,revision=EXCLUDED.revision,updated_at=now()
	`, fixture.workspaceID, fixture.projectID, specificationRevisionID, specificationRevision); err != nil {
		t.Fatalf("insert project specification head: %v", err)
	}
	recordLocalSpecificationApprovalForRuntimeFixture(t, fixture, specificationRevisionID, specificationRevision)
	policy, err := json.Marshal(projectorchestration.DefaultPolicy())
	if err != nil {
		t.Fatalf("encode project policy: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_plan (
			workspace_id,project_id,revision,source_revision,planner_name,goal,
			specification,policy,status,specification_revision_id,plan_digest
		) VALUES ($1,$2,2,$3,'test-planner','Create an autonomous project node issue.',$4,$5,'awaiting_approval',$6,repeat('0',64))
		RETURNING id
	`, fixture.workspaceID, fixture.projectID, "creator-provenance-"+uuid.NewString(),
		encodedPlanSpecification, policy, specificationRevisionID).Scan(&planID); err != nil {
		t.Fatalf("insert executable project plan: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_plan_node (
			plan_id,workspace_id,project_id,node_key,kind,title,description,status,
			required_role_family,required_capabilities,acceptance_criteria,requirement_ids,risk_level,ready_at
		) VALUES ($1,$2,$3,'implement','implementation','Implement autonomous work',
		         'Complete the approved implementation scope.','ready','backend','[]'::jsonb,
		         '["The implementation is independently reviewable."]'::jsonb,
		         '["REQ-AUTONOMOUS-ISSUE-CREATOR"]'::jsonb,'medium',now())
		RETURNING id
	`, planID, fixture.workspaceID, fixture.projectID).Scan(&nodeID); err != nil {
		t.Fatalf("insert ready project node: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET plan_digest=autonomous_project_plan_digest(id)
		WHERE id=$1
	`, planID); err != nil {
		t.Fatalf("refresh executable project plan digest: %v", err)
	}
	var planDigest string
	if err := fixture.pool.QueryRow(ctx, `SELECT plan_digest FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&planDigest); err != nil {
		t.Fatalf("read executable project plan digest: %v", err)
	}
	approveCurrentRuntimePlan(t, fixture, planDigest)

	bus := events.New()
	var createdEvent events.Event
	bus.Subscribe(protocol.EventIssueCreated, func(event events.Event) { createdEvent = event })
	queries := db.New(fixture.pool)
	taskSvc := &service.TaskService{Queries: queries, TxStarter: fixture.pool, Bus: bus}
	businessMetrics := metrics.NewBusinessMetrics()
	metricRegistry := prometheus.NewRegistry()
	metricRegistry.MustRegister(businessMetrics.Collectors()...)
	analyticsRecorder := &recordingAutonomousIssueAnalytics{}
	issueSvc := service.NewIssueService(queries, fixture.pool, bus, analyticsRecorder, taskSvc)
	issueSvc.Metrics = businessMetrics
	runtime := &Runtime{
		pool:         fixture.pool,
		projectStore: projectorchestration.NewStore(fixture.pool),
		team:         teamprovision.New(fixture.pool, queries),
		taskSvc:      taskSvc,
		issueSvc:     issueSvc,
	}
	node := projectorchestration.PlannedNode{
		ReadyNode: projectorchestration.ReadyNode{
			ID: d2UUIDText(nodeID), Key: "implement", Kind: projectorchestration.NodeImplementation,
			Title: "Implement autonomous work", Description: "Complete the approved implementation scope.",
			Risk: projectorchestration.RiskMedium, RequiredRoleFamily: "backend",
			AcceptanceCriteria: []string{"The implementation is independently reviewable."},
		},
		Status: "ready",
	}
	if err := runtime.ensureProjectNodeIssue(ctx, fixture.workspaceID, fixture.projectID, node); err != nil {
		t.Fatalf("materialize autonomous project node issue: %v", err)
	}

	var issueID, creatorID, assigneeID, nodeIssueID, nodeAgentID pgtype.UUID
	var creatorType, assigneeType, issueStatus, assignedRole string
	if err := fixture.pool.QueryRow(ctx, `
		SELECT i.id,i.creator_type,i.creator_id,i.assignee_type,i.assignee_id,i.status,
		       n.materialized_issue_id,n.assigned_agent_id,n.assigned_role
		FROM autonomous_project_plan_node n
	JOIN issue i ON i.id=n.materialized_issue_id AND i.workspace_id=n.workspace_id
		WHERE n.id=$1 AND n.plan_id=$2
	`, nodeID, planID).Scan(&issueID, &creatorType, &creatorID, &assigneeType, &assigneeID,
		&issueStatus, &nodeIssueID, &nodeAgentID, &assignedRole); err != nil {
		t.Fatalf("read materialized project issue provenance: %v", err)
	}
	if creatorType != "agent" || creatorID != agentID {
		t.Fatalf("issue creator = (%q,%s), want autonomous agent %s", creatorType, creatorID.String(), agentID.String())
	}
	if assigneeType != "agent" || assigneeID != agentID || nodeAgentID != agentID || assignedRole != "mobile_engineer" {
		t.Fatalf("issue/node assignment = (%q,%s,%s,%q), want project agent %s", assigneeType, assigneeID.String(), nodeAgentID.String(), assignedRole, agentID.String())
	}
	if issueStatus != "todo" || nodeIssueID != issueID {
		t.Fatalf("materialized issue state = status:%q bound:%s, want todo and bound issue %s", issueStatus, nodeIssueID.String(), issueID.String())
	}
	if createdEvent.Type != protocol.EventIssueCreated || createdEvent.WorkspaceID != fixture.workspaceIDText ||
		createdEvent.ActorType != "agent" || createdEvent.ActorID != agentID.String() {
		t.Fatalf("issue-created event provenance = %+v, want agent %s", createdEvent, agentID.String())
	}
	families, err := metricRegistry.Gather()
	if err != nil {
		t.Fatalf("gather issue-created metrics: %v", err)
	}
	var issueCreated float64
	for _, family := range families {
		if family.GetName() != "multica_issue_created_total" {
			continue
		}
		for _, sample := range family.GetMetric() {
			issueCreated += sample.GetCounter().GetValue()
		}
	}
	if issueCreated != 1 {
		t.Fatalf("issue-created metric = %v, want exactly one", issueCreated)
	}
	if len(analyticsRecorder.events) != 0 {
		t.Fatalf("PostHog issue-created events = %d, want none for the metrics-only event", len(analyticsRecorder.events))
	}
	t.Log("AUTONOMOUS_ISSUE_CREATOR_PROVENANCE_GATE_PASS")
}

func TestEnsureProjectNodeIssueRejectsNonExecutablePlansBeforeCreate(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate string
	}{
		{
			name:   "blocked plan",
			mutate: `UPDATE autonomous_project_plan SET status='blocked' WHERE id=$1`,
		},
		{
			name:   "stale digest",
			mutate: `UPDATE autonomous_project_plan_node SET description=COALESCE(description,'') || ' changed' WHERE id=$1`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newAutonomousDeploymentBridgeFixture(t)
			approveAutonomousDeploymentBridgeFixturePlan(t, fixture)
			mutationID := fixture.planID
			if testCase.name == "stale digest" {
				mutationID = fixture.nodeID
			}
			if _, err := fixture.pool.Exec(fixture.ctx, testCase.mutate, mutationID); err != nil {
				t.Fatalf("make plan non-executable: %v", err)
			}
			var before int
			if err := fixture.pool.QueryRow(fixture.ctx, `
				SELECT count(*) FROM issue WHERE workspace_id=$1 AND project_id=$2
			`, fixture.workspaceID, fixture.projectID).Scan(&before); err != nil {
				t.Fatalf("count project issues before materialization: %v", err)
			}

			runtime := &Runtime{
				pool: fixture.pool, projectStore: projectorchestration.NewStore(fixture.pool),
			}
			node := projectorchestration.PlannedNode{
				ReadyNode: projectorchestration.ReadyNode{ID: d2UUIDText(fixture.nodeID), Key: "deploy"},
				Status:    "ready",
			}
			err := runtime.ensureProjectNodeIssue(fixture.ctx, fixture.workspaceID, fixture.projectID, node)
			if err == nil || err.Error() != `project plan for node "deploy" is not executable` {
				t.Fatalf("materialize non-executable plan error = %v, want executable-plan rejection before service access", err)
			}
			var after int
			if err := fixture.pool.QueryRow(fixture.ctx, `
				SELECT count(*) FROM issue WHERE workspace_id=$1 AND project_id=$2
			`, fixture.workspaceID, fixture.projectID).Scan(&after); err != nil {
				t.Fatalf("count project issues after rejected materialization: %v", err)
			}
			if after != before {
				t.Fatalf("project issue count changed from %d to %d for non-executable plan", before, after)
			}
		})
	}
}

func TestRejectedSpecificationPreventsNodeIssueCreate(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	store := projectorchestration.NewStore(fixture.pool)
	projectFixture := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID)
	runtimeID := projectFixture.Runtime(t, "rejected specification issue gate runtime")
	agentID := d2ParseUUID(t, projectFixture.Agent(t, "Mobile Engineer", runtimeID))

	teamID := pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
	squadID := pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
	teamPlan, err := json.Marshal(teamprovision.Plan{Roles: []teamprovision.RoleSpec{{Role: "mobile_engineer", Family: "backend"}}})
	if err != nil {
		t.Fatalf("encode project team plan: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_team (
			id,workspace_id,project_id,squad_id,intent,plan,
			created_by_agent_id,owner_user_id,status
		) VALUES ($1,$2,$3,$4,'test',$5,$6,$7,'active')
	`, teamID, fixture.workspaceID, fixture.projectID, squadID, teamPlan, agentID, d2ParseUUID(t, fixture.ownerID)); err != nil {
		t.Fatalf("insert autonomous project team: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO autonomous_project_team_member (team_id,role,agent_id,role_family)
		VALUES ($1,'mobile_engineer',$2,'backend')
	`, teamID, agentID); err != nil {
		t.Fatalf("insert autonomous project team member: %v", err)
	}

	specification := projectorchestration.ProjectSpecification{
		Goal:    "Create an issue only under an approved project specification.",
		Summary: "A rejected current specification must stop project node issue creation.",
		Requirements: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-REJECTED-ISSUE-GATE", Text: "Reject project node issue creation.",
			Rationale: "A rejected specification cannot authorize autonomous execution.", Source: "project brief",
		}},
		DefinitionOfDone: []string{"The conductor does not call IssueService.Create."},
	}
	revision, err := store.CreateSpecificationRevision(
		ctx, fixture.workspaceID, fixture.projectID, "planner", "rejected-issue-gate-"+uuid.NewString(), specification, true,
	)
	if err != nil {
		t.Fatalf("create current project specification: %v", err)
	}
	specificationRevisionID := d2ParseUUID(t, revision.ID)
	planSpecification, err := json.Marshal(projectorchestration.Specification{
		Summary:                   specification.Summary,
		Requirements:              []string{specification.Requirements[0].Text},
		NonFunctionalRequirements: specification.NonFunctionalRequirements,
		Constraints:               specification.Constraints,
		DefinitionOfDone:          specification.DefinitionOfDone,
		RequirementDetails:        specification.Requirements,
		NonGoals:                  specification.NonGoals,
		AcceptanceExpectations:    specification.AcceptanceExpectations,
	})
	if err != nil {
		t.Fatalf("encode plan specification: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status='awaiting_approval', approved_at=NULL, approved_by=NULL,
		    goal=$4, specification=$2::jsonb, specification_revision_id=$3
		WHERE id=$1
	`, fixture.planID, planSpecification, specificationRevisionID, specification.Goal); err != nil {
		t.Fatalf("link approved plan fixture to current specification: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET kind='implementation', title='Implement rejected scope',
		    description='Create an issue only under an approved project specification.',
		    status='ready', required_role_family='backend', required_capabilities='[]'::jsonb,
		    acceptance_criteria='["The rejected specification blocks issue creation."]'::jsonb,
		    requirement_ids='["REQ-REJECTED-ISSUE-GATE"]'::jsonb, risk_level='medium', ready_at=now()
		WHERE id=$1
	`, fixture.nodeID); err != nil {
		t.Fatalf("prepare ready project node: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan SET plan_digest=autonomous_project_plan_digest(id) WHERE id=$1
	`, fixture.planID); err != nil {
		t.Fatalf("refresh linked plan digest: %v", err)
	}
	var specificationDigest string
	if err := fixture.pool.QueryRow(ctx, `
		SELECT autonomous_project_specification_digest($1)
	`, specificationRevisionID).Scan(&specificationDigest); err != nil {
		t.Fatalf("read current specification digest: %v", err)
	}
	insertDecision := func(decision string, actedAt time.Time) {
		t.Helper()
		if _, err := fixture.pool.Exec(ctx, `
			INSERT INTO autonomous_project_approval_event (
				workspace_id,project_id,idempotency_key,subject_type,subject_id,subject_revision,
				subject_digest,decision,actor_type,actor_id,actor_display,rationale,
				source_endpoint,origin,origin_ref,acted_at
			) VALUES ($1,$2,$3,'specification_revision',$4,$5,$6,$7,'human',$8,
			         'Project owner','Specification execution gate regression',
			         '/test/specification/decision','local','{}'::jsonb,$9)
		`, fixture.workspaceID, fixture.projectID, "rejected-issue-gate:"+decision+":"+uuid.NewString(),
			revision.ID, revision.Revision, specificationDigest, decision, d2ParseUUID(t, fixture.ownerID), actedAt); err != nil {
			t.Fatalf("insert %s specification decision: %v", decision, err)
		}
	}
	decisionTime := time.Now().UTC()
	insertDecision("approved", decisionTime)
	var planDigest string
	if err := fixture.pool.QueryRow(ctx, `SELECT plan_digest FROM autonomous_project_plan WHERE id=$1`, fixture.planID).Scan(&planDigest); err != nil {
		t.Fatalf("read plan digest before local plan approval: %v", err)
	}
	approveCurrentRuntimePlan(t, fixture, planDigest)
	var executable bool
	if err := fixture.pool.QueryRow(ctx, `SELECT autonomous_project_plan_executable($1)`, fixture.planID).Scan(&executable); err != nil {
		t.Fatalf("check executable plan before rejection: %v", err)
	}
	if !executable {
		t.Fatal("test fixture plan is not executable under the local specification approval")
	}
	insertDecision("rejected", decisionTime.Add(time.Second))
	if err := fixture.pool.QueryRow(ctx, `SELECT autonomous_project_plan_executable($1)`, fixture.planID).Scan(&executable); err != nil {
		t.Fatalf("check plan executable state after rejection: %v", err)
	}
	if executable {
		t.Fatal("rejected current specification left the project plan executable")
	}

	bus := events.New()
	queries := db.New(fixture.pool)
	taskSvc := &service.TaskService{Queries: queries, TxStarter: fixture.pool, Bus: bus}
	issueSvc := service.NewIssueService(queries, fixture.pool, bus, nil, taskSvc)
	createCounter := &issueCreateEntryCounter{}
	issueSvc.Entitlements = createCounter
	runtime := &Runtime{
		pool:         fixture.pool,
		projectStore: store,
		team:         teamprovision.New(fixture.pool, queries),
		taskSvc:      taskSvc,
		issueSvc:     issueSvc,
	}
	node := projectorchestration.PlannedNode{
		ReadyNode: projectorchestration.ReadyNode{
			ID: d2UUIDText(fixture.nodeID), Key: "deploy", Kind: projectorchestration.NodeImplementation,
			Title: "Implement rejected scope", Description: "Create an issue only under an approved project specification.",
			Risk: projectorchestration.RiskMedium, RequiredRoleFamily: "backend",
			AcceptanceCriteria: []string{"The rejected specification blocks issue creation."},
		},
		Status: "ready",
	}
	if err := runtime.ensureProjectNodeIssue(ctx, fixture.workspaceID, fixture.projectID, node); err == nil || err.Error() != `project plan for node "deploy" is not executable` {
		t.Fatalf("materialize rejected-specification node error = %v, want executable-plan rejection before IssueService.Create", err)
	}
	if createCounter.calls != 0 {
		t.Fatalf("IssueService.Create entry calls = %d, want 0 for rejected specification", createCounter.calls)
	}
	var issueCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM issue WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID).Scan(&issueCount); err != nil {
		t.Fatalf("count project issues after rejected materialization: %v", err)
	}
	if issueCount != 0 {
		t.Fatalf("project issue count after rejected materialization = %d, want 0", issueCount)
	}
	t.Log("T3_REJECTED_SPEC_ISSUE_GATE_PASS")
}

func TestResumeNodeForWorkflowRetryReactivatesOnlyApprovedCurrentPlans(t *testing.T) {
	t.Run("approved blocked plan resumes", func(t *testing.T) {
		fixture := newAutonomousDeploymentBridgeFixture(t)
		approveAutonomousDeploymentBridgeFixturePlan(t, fixture)
		issueIDText := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID).Issue(
			t, "Retryable project node", testutil.Cols{"project_id": fixture.projectIDText},
		)
		issueID := d2ParseUUID(t, issueIDText)
		if _, err := fixture.pool.Exec(fixture.ctx, `
			UPDATE autonomous_project_plan_node
			SET status='blocked', blocked_category='technical_failure', materialized_issue_id=$2
			WHERE id=$1
		`, fixture.nodeID, issueID); err != nil {
			t.Fatalf("block materialized project node: %v", err)
		}
		if _, err := fixture.pool.Exec(fixture.ctx, `
			UPDATE autonomous_project_plan SET status='blocked', plan_digest=autonomous_project_plan_digest(id),
			       approved_at=now(), approved_by='retry regression'
			WHERE id=$1
		`, fixture.planID); err != nil {
			t.Fatalf("block approved plan: %v", err)
		}

		resumed, err := projectorchestration.NewStore(fixture.pool).ResumeNodeForWorkflowRetry(
			fixture.ctx, fixture.workspaceID, issueID,
		)
		if err != nil || !resumed {
			t.Fatalf("resume node on current approved plan = %v, %v; want resumed", resumed, err)
		}
		var planStatus, nodeStatus string
		if err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT p.status, n.status FROM autonomous_project_plan p
			JOIN autonomous_project_plan_node n ON n.plan_id=p.id
			WHERE p.id=$1 AND n.id=$2
		`, fixture.planID, fixture.nodeID).Scan(&planStatus, &nodeStatus); err != nil {
			t.Fatalf("read resumed node and plan: %v", err)
		}
		if planStatus != "active" || nodeStatus != "running" {
			t.Fatalf("retry state = plan %q node %q, want active/running", planStatus, nodeStatus)
		}
	})

	t.Run("stale blocked plan remains blocked", func(t *testing.T) {
		fixture := newAutonomousDeploymentBridgeFixture(t)
		approveAutonomousDeploymentBridgeFixturePlan(t, fixture)
		issueIDText := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID).Issue(
			t, "Stale retry project node", testutil.Cols{"project_id": fixture.projectIDText},
		)
		issueID := d2ParseUUID(t, issueIDText)
		if _, err := fixture.pool.Exec(fixture.ctx, `
			UPDATE autonomous_project_plan_node
			SET status='blocked', blocked_category='technical_failure', materialized_issue_id=$2,
			    description=COALESCE(description,'') || ' changed after approval'
			WHERE id=$1
		`, fixture.nodeID, issueID); err != nil {
			t.Fatalf("make retry plan digest stale: %v", err)
		}
		if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan SET status='blocked' WHERE id=$1`, fixture.planID); err != nil {
			t.Fatalf("block stale plan: %v", err)
		}

		resumed, err := projectorchestration.NewStore(fixture.pool).ResumeNodeForWorkflowRetry(
			fixture.ctx, fixture.workspaceID, issueID,
		)
		if err != nil {
			t.Fatalf("check stale retry plan: %v", err)
		}
		if resumed {
			t.Fatal("stale plan retry was resumed")
		}
		var planStatus, nodeStatus string
		if err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT p.status, n.status FROM autonomous_project_plan p
			JOIN autonomous_project_plan_node n ON n.plan_id=p.id
			WHERE p.id=$1 AND n.id=$2
		`, fixture.planID, fixture.nodeID).Scan(&planStatus, &nodeStatus); err != nil {
			t.Fatalf("read stale retry plan state: %v", err)
		}
		if planStatus != "blocked" || nodeStatus != "blocked" {
			t.Fatalf("stale retry state = plan %q node %q, want blocked/blocked", planStatus, nodeStatus)
		}
	})
}

func TestIssueCreateRollsBackWhenPlanBecomesBlockedBeforeTransactionalBind(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	approveAutonomousDeploymentBridgeFixturePlan(t, fixture)
	issueTitle := "Plan-race materialization rollback"
	issueService := service.NewIssueService(
		db.New(fixture.pool),
		&projectPlanMutationTxStarter{pool: fixture.pool, planID: fixture.planID},
		nil, nil, nil,
	)
	var before int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM issue WHERE workspace_id=$1 AND title=$2`, fixture.workspaceID, issueTitle).Scan(&before); err != nil {
		t.Fatalf("count issues before race test: %v", err)
	}
	_, err := issueService.Create(fixture.ctx, service.IssueCreateParams{
		WorkspaceID: fixture.workspaceID,
		ProjectID:   fixture.projectID,
		Title:       issueTitle,
		Status:      "backlog",
		Priority:    "medium",
		CreatorType: "member",
		CreatorID:   d2ParseUUID(t, fixture.ownerID),
	}, service.IssueCreateOpts{
		BeforeCommit: func(ctx context.Context, tx pgx.Tx, issue db.Issue) error {
			return projectorchestration.NewStore(fixture.pool).BindNodeIssueTx(
				ctx, tx, fixture.workspaceID, fixture.projectID, fixture.nodeID, "deploy",
				issue.ID, "", pgtype.UUID{},
			)
		},
	})
	if err == nil {
		t.Fatal("issue create committed after the plan became blocked before binding")
	}
	if !strings.Contains(err.Error(), "plan is not executable") {
		t.Fatalf("issue create error = %v, want executable-plan binding rejection", err)
	}
	var after int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM issue WHERE workspace_id=$1 AND title=$2`, fixture.workspaceID, issueTitle).Scan(&after); err != nil {
		t.Fatalf("count issues after race test: %v", err)
	}
	if after != before {
		t.Fatalf("issue count changed from %d to %d after failed transactional bind", before, after)
	}
	var boundIssueID pgtype.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT materialized_issue_id FROM autonomous_project_plan_node WHERE id=$1`, fixture.nodeID).Scan(&boundIssueID); err != nil {
		t.Fatalf("read node binding after failed create: %v", err)
	}
	if boundIssueID.Valid {
		t.Fatalf("node received rolled-back issue id %s", d2UUIDText(boundIssueID))
	}
	if errors.Is(err, service.ErrActiveDuplicate) {
		t.Fatal("race test unexpectedly used an existing duplicate")
	}
	t.Log("PROJECT_PLAN_EXECUTABLE_GATE_PASS")
}

func TestDiscoveredIssueInheritsEpicAndRequirements(t *testing.T) {
	plan := projectorchestration.Plan{Nodes: []projectorchestration.NodeSpec{{Key: "epic", IsEpic: true}}}
	node := projectorchestration.NodeSpec{}
	if err := inheritDiscoveredProjectNodeTraceability(&node, plan, "task", "epic", false, true, []string{"REQ-1", "REQ-2"}); err != nil {
		t.Fatalf("inherit source task traceability: %v", err)
	}
	if node.ParentEpicKey != "epic" || len(node.RequirementIDs) != 2 || node.RequirementIDs[0] != "REQ-1" || node.RequirementIDs[1] != "REQ-2" {
		t.Fatalf("inherited traceability = parent %q, requirements %v", node.ParentEpicKey, node.RequirementIDs)
	}

	epicChild := projectorchestration.NodeSpec{}
	if err := inheritDiscoveredProjectNodeTraceability(&epicChild, plan, "new-epic", "", true, true, []string{"REQ-3"}); err != nil {
		t.Fatalf("inherit source epic traceability: %v", err)
	}
	if epicChild.ParentEpicKey != "new-epic" || len(epicChild.RequirementIDs) != 1 || epicChild.RequirementIDs[0] != "REQ-3" {
		t.Fatalf("source epic traceability = parent %q, requirements %v", epicChild.ParentEpicKey, epicChild.RequirementIDs)
	}
	if err := inheritDiscoveredProjectNodeTraceability(&node, plan, "", "", false, false, nil); err == nil {
		t.Fatal("missing source was accepted despite an epic hierarchy")
	}
	if err := inheritDiscoveredProjectNodeTraceability(&node, plan, "task", "", false, true, nil); err == nil {
		t.Fatal("source without epic membership was accepted in an epic plan")
	}
}

func TestNonDeploymentApprovalRequiresCurrentPlanRevision(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	approveAutonomousDeploymentBridgeFixturePlan(t, fixture)
	ctx := fixture.ctx
	var revision int64
	if err := fixture.pool.QueryRow(ctx, `SELECT revision FROM autonomous_project_plan WHERE id=$1`, fixture.planID).Scan(&revision); err != nil {
		t.Fatalf("read current plan revision: %v", err)
	}
	var escalationID pgtype.UUID
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, plan_id, node_id, category, severity, summary,
			context, status, resolution
		) VALUES ($1,$2,$3,$4,'approval_required','medium','stale approval',
		         jsonb_build_object('plan_revision', ($5::bigint - 1)::text),
		         'resolved','{"decision":"approved"}'::jsonb)
		RETURNING id
	`, fixture.workspaceID, fixture.projectID, fixture.planID, fixture.nodeID, revision).Scan(&escalationID); err != nil {
		t.Fatalf("insert prior-revision approval: %v", err)
	}
	runtime := &Runtime{pool: fixture.pool}
	approved, err := runtime.hasExactPlanApproval(ctx, fixture.workspaceID, fixture.projectID, fixture.planID, revision, fixture.nodeID)
	if err != nil {
		t.Fatalf("check prior-revision approval: %v", err)
	}
	if approved {
		t.Fatal("approval from an older plan revision was accepted")
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_escalation
		SET context=jsonb_build_object('plan_revision', $2::bigint::text)
		WHERE id=$1
	`, escalationID, revision); err != nil {
		t.Fatalf("bind approval to current plan revision: %v", err)
	}
	approved, err = runtime.hasExactPlanApproval(ctx, fixture.workspaceID, fixture.projectID, fixture.planID, revision, fixture.nodeID)
	if err != nil || !approved {
		t.Fatalf("current-revision approval = %v, error = %v; want accepted", approved, err)
	}
}
