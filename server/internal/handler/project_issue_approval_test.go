package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestProjectIssueCreationPathsRequireLocalPlanAndRecordManualProvenance(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	project := createProjectPermissionTestProject(t, "project issue creation gate")
	projectUUID := parseUUID(project.ID)
	t.Cleanup(func() { purgeApprovalEventsForProject(t, projectUUID) })

	manualRecorder := httptest.NewRecorder()
	manualRequest := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Human-created project issue",
		"project_id": project.ID,
	})
	testHandler.CreateIssue(manualRecorder, manualRequest)
	if manualRecorder.Code != http.StatusCreated {
		t.Fatalf("manual project issue status = %d, want 201: %s", manualRecorder.Code, manualRecorder.Body.String())
	}
	var manualIssue IssueResponse
	if err := json.NewDecoder(manualRecorder.Body).Decode(&manualIssue); err != nil {
		t.Fatalf("decode manual project issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, manualIssue.ID)
	})

	var provenanceCount int
	var subjectType, decision, actorType, actorID, sourceEndpoint, origin, digest, rationale string
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), max(subject_type), max(decision), max(actor_type), max(actor_id::text),
		       max(source_endpoint), max(origin), max(subject_digest), max(rationale)
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND subject_id=$3
	`, testWorkspaceID, project.ID, manualIssue.ID).Scan(
		&provenanceCount, &subjectType, &decision, &actorType, &actorID,
		&sourceEndpoint, &origin, &digest, &rationale,
	); err != nil {
		t.Fatalf("read manual issue provenance: %v", err)
	}
	if provenanceCount != 1 || subjectType != "manual_issue" || decision != "approved" || actorType != "human" ||
		actorID != testUserID || sourceEndpoint != "/api/issues" || origin != "local" || len(digest) != 64 || strings.TrimSpace(rationale) == "" {
		t.Fatalf("manual issue provenance = count:%d subject:%q decision:%q actor:%q/%q endpoint:%q origin:%q digest:%q rationale:%q",
			provenanceCount, subjectType, decision, actorType, actorID, sourceEndpoint, origin, digest, rationale)
	}
	if executable, err := service.ProjectIssuePlanExecutable(ctx, testHandler.DB, parseUUID(testWorkspaceID), projectUUID); err != nil {
		t.Fatalf("check plan after manual provenance: %v", err)
	} else if executable {
		t.Fatal("manual issue provenance opened autonomous project execution without an executable plan")
	}

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT id, runtime_id FROM agent WHERE workspace_id=$1 AND name='Handler Test Agent'
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("load project issue test agent: %v", err)
	}
	var originalRuntimeMetadata string
	if err := testPool.QueryRow(ctx, `SELECT metadata::text FROM agent_runtime WHERE id=$1`, runtimeID).Scan(&originalRuntimeMetadata); err != nil {
		t.Fatalf("read agent runtime metadata: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object('cli_version',$2::text) WHERE id=$1
	`, runtimeID, agent.MinQuickCreateFieldsCLIVersion); err != nil {
		t.Fatalf("enable quick-create CLI version: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET metadata=$2::jsonb WHERE id=$1`, runtimeID, originalRuntimeMetadata)
	})

	number := nextWorkspaceIssueNumber(t)
	var parentIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,priority,creator_type,creator_id,position,number,project_id)
		VALUES ($1,'project issue gate parent','todo','none','member',$2,0,$3,$4) RETURNING id
	`, testWorkspaceID, testUserID, number, project.ID).Scan(&parentIssueID); err != nil {
		t.Fatalf("create project issue parent: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, parentIssueID) })

	var actingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id,runtime_id,status,priority,originator_user_id,accountable_user_id)
		VALUES ($1,$2,'running',0,$3,$3) RETURNING id
	`, agentID, runtimeID, testUserID).Scan(&actingTaskID); err != nil {
		t.Fatalf("create agent issue request task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id=$1`, actingTaskID)
	})

	var issueCounterBefore, issueCounterAfter int32
	if err := testPool.QueryRow(ctx, `SELECT issue_counter FROM workspace WHERE id=$1`, testWorkspaceID).Scan(&issueCounterBefore); err != nil {
		t.Fatalf("read issue counter before agent create: %v", err)
	}
	var queuedTasksBefore int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'type'='quick_create'
	`, agentID).Scan(&queuedTasksBefore); err != nil {
		t.Fatalf("count queued quick-create tasks before agent create: %v", err)
	}
	agentRecorder := httptest.NewRecorder()
	agentRequest := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "Unauthorized agent project issue",
		"parent_issue_id": parentIssueID,
	})
	agentRequest.Header.Set("X-Agent-ID", agentID)
	agentRequest.Header.Set("X-Task-ID", actingTaskID)
	testHandler.CreateIssue(agentRecorder, agentRequest)
	assertProjectPlanRequired(t, agentRecorder)
	assertIssueTitleCount(t, "Unauthorized agent project issue", 0)
	if err := testPool.QueryRow(ctx, `SELECT issue_counter FROM workspace WHERE id=$1`, testWorkspaceID).Scan(&issueCounterAfter); err != nil {
		t.Fatalf("read issue counter after rejected agent create: %v", err)
	}
	if issueCounterAfter != issueCounterBefore {
		t.Fatalf("rejected agent create changed issue counter from %d to %d", issueCounterBefore, issueCounterAfter)
	}
	var queuedTasksAfter int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'type'='quick_create'
	`, agentID).Scan(&queuedTasksAfter); err != nil {
		t.Fatalf("count queued quick-create tasks after agent create: %v", err)
	}
	if queuedTasksAfter != queuedTasksBefore {
		t.Fatalf("rejected agent issue created %d quick-create tasks", queuedTasksAfter-queuedTasksBefore)
	}

	quickRecorder := httptest.NewRecorder()
	quickRequest := newRequest("POST", "/api/issues/quick-create?workspace_id="+testWorkspaceID, map[string]any{
		"agent_id":        agentID,
		"prompt":          "Create a project sub-issue",
		"parent_issue_id": parentIssueID,
	})
	testHandler.QuickCreateIssue(quickRecorder, quickRequest)
	assertProjectPlanRequired(t, quickRecorder)
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND context->>'type'='quick_create'
	`, agentID).Scan(&queuedTasksAfter); err != nil {
		t.Fatalf("count queued quick-create tasks after rejected admission: %v", err)
	}
	if queuedTasksAfter != queuedTasksBefore {
		t.Fatalf("rejected quick-create admission enqueued a task: before=%d after=%d", queuedTasksBefore, queuedTasksAfter)
	}

	memberRecorder := httptest.NewRecorder()
	memberRequest := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Non-project issue remains available",
	})
	testHandler.CreateIssue(memberRecorder, memberRequest)
	if memberRecorder.Code != http.StatusCreated {
		t.Fatalf("non-project member issue status = %d, want 201: %s", memberRecorder.Code, memberRecorder.Body.String())
	}
	var memberIssue IssueResponse
	if err := json.NewDecoder(memberRecorder.Body).Decode(&memberIssue); err != nil {
		t.Fatalf("decode non-project member issue: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, memberIssue.ID) })
	var manualEventsAfterNonProject int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_approval_event WHERE project_id=$1 AND subject_type='manual_issue'
	`, project.ID).Scan(&manualEventsAfterNonProject); err != nil {
		t.Fatalf("count project provenance after non-project issue: %v", err)
	}
	if manualEventsAfterNonProject != 1 {
		t.Fatalf("non-project issue changed project provenance count to %d", manualEventsAfterNonProject)
	}
	t.Log("PROJECT_ISSUE_PATH_GATE_PASS")
}

func assertProjectPlanRequired(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("project issue gate status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["code"] != "project_plan_required" {
		t.Fatalf("project issue gate response = %#v, err=%v", response, err)
	}
}

func assertIssueTitleCount(t *testing.T, title string, want int) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id=$1 AND title=$2`, testWorkspaceID, title).Scan(&count); err != nil {
		t.Fatalf("count issue title %q: %v", title, err)
	}
	if count != want {
		t.Fatalf("issue count for %q = %d, want %d", title, count, want)
	}
}
