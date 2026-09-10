package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestIssueDependencyBlocksProgressUntilPredecessorDone(t *testing.T) {
	predecessor := dbfx.Issue(t, "dependency predecessor", testutil.Cols{"status": "todo"})
	blocked := dbfx.Issue(t, "dependency blocked", testutil.Cols{"status": "todo"})
	dbfx.Exec(t, `
		INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
		VALUES ($1, $2, 'blocked_by')
	`, blocked, predecessor)
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM issue_dependency WHERE issue_id = $1`, blocked)
		dbfx.Exec(t, `DELETE FROM issue WHERE id IN ($1, $2)`, predecessor, blocked)
	})

	get := httptest.NewRecorder()
	testHandler.GetIssue(get, testutil.WithURLParams(
		newRequest(http.MethodGet, "/api/issues/"+blocked, nil),
		"id", blocked,
	))
	if get.Code != http.StatusOK {
		t.Fatalf("get blocked issue: expected 200, got %d: %s", get.Code, get.Body.String())
	}
	var response IssueResponse
	if err := json.NewDecoder(get.Body).Decode(&response); err != nil {
		t.Fatalf("decode blocked issue: %v", err)
	}
	if !response.IsBlocked || response.UnresolvedBlockerCount != 1 {
		t.Fatalf("dependency state = blocked=%v count=%d, want true/1", response.IsBlocked, response.UnresolvedBlockerCount)
	}

	var conflict struct {
		Code string `json:"code"`
	}
	testutil.Call(t, testHandler.UpdateIssue, testutil.WithURLParams(
		newRequest(http.MethodPut, "/api/issues/"+blocked, map[string]any{"status": "in_progress"}),
		"id", blocked,
	)).Want(http.StatusConflict).JSON(&conflict)
	if conflict.Code != "dependency_blocked" {
		t.Fatalf("conflict code = %q, want dependency_blocked", conflict.Code)
	}

	dbfx.Exec(t, `UPDATE issue SET status = 'done' WHERE id = $1`, predecessor)
	testutil.Call(t, testHandler.UpdateIssue, testutil.WithURLParams(
		newRequest(http.MethodPut, "/api/issues/"+blocked, map[string]any{"status": "in_progress"}),
		"id", blocked,
	)).Want(http.StatusOK)
}
