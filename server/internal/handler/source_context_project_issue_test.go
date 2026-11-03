package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestSourceContextProjectIssueRecordsManualProvenance(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	project := createProjectPermissionTestProject(t, "source-context manual project issue")
	projectUUID := parseUUID(project.ID)
	t.Cleanup(func() { purgeApprovalEventsForProject(t, projectUUID) })

	number := nextWorkspaceIssueNumber(t)
	var sourceIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,description,creator_type,creator_id,number,project_id)
		VALUES ($1,'source-context project parent','source body','member',$2,$3,$4) RETURNING id
	`, testWorkspaceID, testUserID, number, project.ID).Scan(&sourceIssueID); err != nil {
		t.Fatalf("create source project issue: %v", err)
	}
	var commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id,workspace_id,author_type,author_id,content)
		VALUES ($1,$2,'member',$3,'source context anchor') RETURNING id
	`, sourceIssueID, testWorkspaceID, testUserID).Scan(&commentID); err != nil {
		t.Fatalf("create source context anchor: %v", err)
	}

	originalStorage := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	t.Cleanup(func() { testHandler.Storage = originalStorage })
	targetIssueID := ""
	t.Cleanup(func() {
		if targetIssueID != "" {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM attachment WHERE source_context_id IN (SELECT id FROM issue_source_context WHERE issue_id=$1)`, targetIssueID)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM issue_source_context WHERE issue_id=$1`, targetIssueID)
			_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, targetIssueID)
		}
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, sourceIssueID)
	})

	build, err := service.BuildSourceContext(ctx, testHandler.Queries, parseUUID(testWorkspaceID), parseUUID(commentID))
	if err != nil {
		t.Fatalf("build source context: %v", err)
	}
	request := withURLParam(newRequest(http.MethodPost, "/api/comments/"+commentID+"/sub-issues", map[string]any{
		"mode":          "manual",
		"capture_token": build.Token,
		"issue":         map[string]any{"title": "Manual project sub-issue", "status": "todo", "priority": "none"},
	}), "commentId", commentID)
	recorder := httptest.NewRecorder()
	testHandler.CreateCommentSubIssue(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("manual source-context sub-issue status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(recorder.Body).Decode(&created); err != nil {
		t.Fatalf("decode manual source-context sub-issue: %v", err)
	}
	targetIssueID = created.ID

	var issueProjectID string
	if err := testPool.QueryRow(ctx, `SELECT project_id::text FROM issue WHERE id=$1`, targetIssueID).Scan(&issueProjectID); err != nil {
		t.Fatalf("read manual sub-issue project: %v", err)
	}
	if issueProjectID != project.ID {
		t.Fatalf("manual sub-issue project = %q, want inherited project %q", issueProjectID, project.ID)
	}
	var eventCount int
	var subjectType, decision, actorType, actorID, sourceEndpoint, origin, digest string
	if err := testPool.QueryRow(ctx, `
		SELECT count(*),max(subject_type),max(decision),max(actor_type),max(actor_id::text),
		       max(source_endpoint),max(origin),max(subject_digest)
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND subject_id=$3
	`, testWorkspaceID, project.ID, targetIssueID).Scan(
		&eventCount, &subjectType, &decision, &actorType, &actorID, &sourceEndpoint, &origin, &digest,
	); err != nil {
		t.Fatalf("read source-context manual issue provenance: %v", err)
	}
	if eventCount != 1 || subjectType != "manual_issue" || decision != "approved" || actorType != "human" ||
		actorID != testUserID || sourceEndpoint != "/api/comments/:commentId/sub-issues" || origin != "local" || len(digest) != 64 {
		t.Fatalf("source-context provenance = count:%d subject:%q decision:%q actor:%q/%q endpoint:%q origin:%q digest:%q",
			eventCount, subjectType, decision, actorType, actorID, sourceEndpoint, origin, digest)
	}
	t.Log("SOURCE_CONTEXT_PROJECT_ISSUE_GATE_PASS")
}
