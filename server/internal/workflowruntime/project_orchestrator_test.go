package workflowruntime

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/teamprovision"
)

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
