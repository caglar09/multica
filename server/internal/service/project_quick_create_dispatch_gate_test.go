package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/entitlement"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectQuickCreateClaimsRequireExecutablePlan(t *testing.T) {
	ctx := context.Background()
	fixture := newAutopilotQuotaFixture(t, entitlement.ActionEnforce, 5)
	projectID, revision := seedAutopilotProjectIssueGate(t, fixture)
	projectIDString := util.UUIDToString(projectID)
	workspaceIDString := util.UUIDToString(fixture.workspaceID)
	queries := db.New(fixture.pool)
	fx := testutil.New(fixture.pool, util.UUIDToString(fixture.workspaceID), util.UUIDToString(fixture.publisherID))

	parentIssueID := fx.Issue(t, "Quick-create project parent")
	if _, err := fixture.pool.Exec(ctx, `UPDATE issue SET project_id=$1 WHERE id=$2`, projectID, parentIssueID); err != nil {
		t.Fatalf("bind parent issue to project: %v", err)
	}
	var runtimeID pgtype.UUID
	if err := fixture.pool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id=$1`, fixture.agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load fixture agent runtime: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agent_runtime SET status='online', last_seen_at=now() WHERE id=$1`, runtimeID); err != nil {
		t.Fatalf("keep fixture runtime claimable: %v", err)
	}

	directTaskID := fx.Task(t, util.UUIDToString(fixture.agentID), testutil.Cols{
		"runtime_id": util.UUIDToString(runtimeID),
		"context":    fmt.Sprintf(`{"type":"quick_create","workspace_id":%q,"project_id":%q}`, workspaceIDString, projectIDString),
	})
	inheritedTaskID := fx.Task(t, util.UUIDToString(fixture.agentID), testutil.Cols{
		"runtime_id": util.UUIDToString(runtimeID),
		"context":    fmt.Sprintf(`{"type":"quick_create","workspace_id":%q,"parent_issue_id":%q}`, workspaceIDString, parentIssueID),
	})

	assertNoQuickCreateCandidates := func() {
		t.Helper()
		singular, err := queries.ListQueuedClaimCandidatesByRuntime(ctx, runtimeID)
		if err != nil {
			t.Fatalf("list singular claim candidates with closed gate: %v", err)
		}
		if len(singular) != 0 {
			t.Fatalf("singular candidates = %d, want 0 while project plan is closed", len(singular))
		}
		batch, err := queries.ListQueuedClaimCandidatesByRuntimes(ctx, []pgtype.UUID{runtimeID})
		if err != nil {
			t.Fatalf("list batch claim candidates with closed gate: %v", err)
		}
		if len(batch) != 0 {
			t.Fatalf("batch candidates = %d, want 0 while project plan is closed", len(batch))
		}
	}
	assertTasksQueued := func() {
		t.Helper()
		for _, taskID := range []string{directTaskID, inheritedTaskID} {
			var status string
			if err := fixture.pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1`, taskID).Scan(&status); err != nil {
				t.Fatalf("read task %s status: %v", taskID, err)
			}
			if status != "queued" {
				t.Fatalf("task %s status = %q, want queued", taskID, status)
			}
		}
	}

	assertNoQuickCreateCandidates()
	if _, err := queries.ClaimAgentTask(ctx, db.ClaimAgentTaskParams{
		AgentID: fixture.agentID, RuntimeID: runtimeID,
		PrepareLeaseSecs: 60, RuntimeStaleSecs: RuntimeClaimFreshnessSeconds,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("atomic claim with closed project plan error = %v, want no rows", err)
	}

	singularService := NewTaskService(queries, fixture.pool, nil, events.New())
	claimed, err := singularService.ClaimTaskForRuntime(ctx, runtimeID)
	if err != nil {
		t.Fatalf("singular claim with closed project plan: %v", err)
	}
	if claimed != nil {
		t.Fatalf("singular claim dispatched task %s while project plan is closed", util.UUIDToString(claimed.ID))
	}

	batchService := NewTaskService(queries, fixture.pool, nil, events.New())
	batchClaimed, err := batchService.ClaimTasksForRuntimes(ctx, []pgtype.UUID{runtimeID}, 1)
	if err != nil {
		t.Fatalf("batch claim with closed project plan: %v", err)
	}
	if len(batchClaimed) != 0 {
		t.Fatalf("batch claim dispatched %d tasks while project plan is closed", len(batchClaimed))
	}
	assertTasksQueued()

	approveAutopilotProjectSpecification(t, fixture, projectID, revision)
	var executable bool
	if err := fixture.pool.QueryRow(ctx, `SELECT autonomous_project_plan_executable(id) FROM autonomous_project_plan WHERE project_id=$1`, projectID).Scan(&executable); err != nil {
		t.Fatalf("recheck project gate after local approval: %v", err)
	}
	if !executable {
		t.Fatal("local project approval did not make the persisted plan executable")
	}
	openCandidates, err := queries.ListQueuedClaimCandidatesByRuntime(ctx, runtimeID)
	if err != nil {
		t.Fatalf("list singular candidates after local approval: %v", err)
	}
	if len(openCandidates) != 2 {
		t.Fatalf("singular candidates after local approval = %d, want 2", len(openCandidates))
	}
	singularService = NewTaskService(queries, fixture.pool, nil, events.New())
	claimed, err = singularService.ClaimTaskForRuntime(ctx, runtimeID)
	if err != nil {
		t.Fatalf("singular claim after local project approval: %v", err)
	}
	if claimed == nil {
		t.Fatal("singular claim did not dispatch a project quick-create task after local approval")
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', completed_at=now(), prepare_lease_expires_at=NULL WHERE id=$1`, claimed.ID); err != nil {
		t.Fatalf("complete singular positive-control task: %v", err)
	}

	batchService = NewTaskService(queries, fixture.pool, nil, events.New())
	batchClaimed, err = batchService.ClaimTasksForRuntimes(ctx, []pgtype.UUID{runtimeID}, 1)
	if err != nil {
		t.Fatalf("batch claim after local project approval: %v", err)
	}
	if len(batchClaimed) != 1 {
		t.Fatalf("batch claim after local approval returned %d tasks, want 1", len(batchClaimed))
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed', completed_at=now(), prepare_lease_expires_at=NULL WHERE id=$1`, batchClaimed[0].ID); err != nil {
		t.Fatalf("complete batch positive-control task: %v", err)
	}

	nonProjectTaskID := fx.Task(t, util.UUIDToString(fixture.agentID), testutil.Cols{
		"runtime_id": util.UUIDToString(runtimeID),
		"context":    fmt.Sprintf(`{"type":"quick_create","workspace_id":%q}`, workspaceIDString),
	})
	batchService = NewTaskService(queries, fixture.pool, nil, events.New())
	batchClaimed, err = batchService.ClaimTasksForRuntimes(ctx, []pgtype.UUID{runtimeID}, 1)
	if err != nil {
		t.Fatalf("batch claim for non-project quick-create: %v", err)
	}
	if len(batchClaimed) != 1 || util.UUIDToString(batchClaimed[0].ID) != nonProjectTaskID {
		t.Fatalf("non-project quick-create claim = %v, want task %s", batchClaimed, nonProjectTaskID)
	}

	t.Log("PROJECT_QUICK_CREATE_DISPATCH_GATE_PASS")
}
