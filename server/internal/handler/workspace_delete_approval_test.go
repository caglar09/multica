package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDeleteWorkspacePurgesApprovalLedger(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	insertWorkspace := func(name string) string {
		t.Helper()
		var workspaceID string
		slug := name + "-" + uuid.NewString()
		if err := testPool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`, name, slug).Scan(&workspaceID); err != nil {
			t.Fatalf("insert workspace %q: %v", name, err)
		}
		if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, testUserID); err != nil {
			t.Fatalf("add workspace owner: %v", err)
		}
		return workspaceID
	}
	insertApproval := func(workspaceID string) {
		t.Helper()
		_, err := testPool.Exec(ctx, `INSERT INTO autonomous_project_approval_event
			(workspace_id, project_id, idempotency_key, subject_type, subject_id, subject_revision,
			 subject_digest, decision, actor_type, actor_id, actor_display, policy_ref, rationale,
			 source_endpoint, origin, origin_ref)
			VALUES ($1,$2,$3,'plan',$4,1,$5,'approved','system',NULL,'Policy test',
			 'workspace-delete-test','Workspace deletion purge test','/test/workspace-delete','local','{}'::jsonb)`,
			workspaceID, uuid.NewString(), uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64))
		if err != nil {
			t.Fatalf("insert approval event: %v", err)
		}
	}
	countApprovals := func(workspaceID string) int {
		t.Helper()
		var count int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_approval_event WHERE workspace_id=$1`, workspaceID).Scan(&count); err != nil {
			t.Fatalf("count approval events for workspace %s: %v", workspaceID, err)
		}
		return count
	}
	deleteWorkspace := func(workspaceID string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodDelete, "/api/workspaces/"+workspaceID, nil), "id", workspaceID)
		testHandler.DeleteWorkspace(w, req)
		return w
	}

	deletedWorkspaceID := insertWorkspace("approval-purge-deleted")
	keptWorkspaceID := insertWorkspace("approval-purge-kept")
	insertApproval(deletedWorkspaceID)
	insertApproval(keptWorkspaceID)

	if response := deleteWorkspace(deletedWorkspaceID); response.Code != http.StatusNoContent {
		t.Fatalf("DeleteWorkspace status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if count := countApprovals(deletedWorkspaceID); count != 0 {
		t.Fatalf("deleted workspace approval events = %d, want 0", count)
	}
	if count := countApprovals(keptWorkspaceID); count != 1 {
		t.Fatalf("other workspace approval events = %d, want 1", count)
	}

	rollbackWorkspaceID := insertWorkspace("approval-purge-rollback")
	insertApproval(rollbackWorkspaceID)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "test_workspace_delete_failure_" + suffix
	triggerName := "test_workspace_delete_failure_trigger_" + suffix
	rollbackSlug := "approval-purge-rollback-" + suffix
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON workspace", triggerName))
		_, _ = testPool.Exec(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})
	functionSQL := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
BEGIN
	IF OLD.slug = '%s' THEN
		RAISE EXCEPTION 'injected workspace deletion failure';
	END IF;
	RETURN OLD;
END;
$body$`, functionName, rollbackSlug)
	if _, err := testPool.Exec(ctx, functionSQL); err != nil {
		t.Fatalf("create workspace deletion failure trigger function: %v", err)
	}
	if _, err := testPool.Exec(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE DELETE ON workspace FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName)); err != nil {
		t.Fatalf("create workspace deletion failure trigger: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET slug=$2 WHERE id=$1`, rollbackWorkspaceID, rollbackSlug); err != nil {
		t.Fatalf("mark rollback workspace: %v", err)
	}

	if response := deleteWorkspace(rollbackWorkspaceID); response.Code != http.StatusInternalServerError {
		t.Fatalf("DeleteWorkspace with injected late failure status = %d, want %d: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	var workspaceExists bool
	if err := testPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workspace WHERE id=$1)`, rollbackWorkspaceID).Scan(&workspaceExists); err != nil {
		t.Fatalf("check rollback workspace: %v", err)
	}
	if !workspaceExists {
		t.Fatal("workspace deletion was not rolled back after late failure")
	}
	if count := countApprovals(rollbackWorkspaceID); count != 1 {
		t.Fatalf("approval events after late delete failure = %d, want 1", count)
	}
	if count := countApprovals(keptWorkspaceID); count != 1 {
		t.Fatalf("other workspace approval events after rollback = %d, want 1", count)
	}
	t.Log("WORKSPACE_APPROVAL_PURGE_GATE_PASS")
}
