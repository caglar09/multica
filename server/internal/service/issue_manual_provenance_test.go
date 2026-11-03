package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectIssueCreationPathsRequireLocalPlanAndRecordManualProvenance(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	f, ownerID := newPrincipalFixture(t)
	ctx := context.Background()
	workspaceID := parseTestUUID(t, f.WorkspaceID)
	projectID := parseTestUUID(t, f.Project(t, "manual issue provenance project"))
	if err := f.q.SeedIssueStatusEntries(ctx, workspaceID); err != nil {
		t.Fatalf("seed issue statuses: %v", err)
	}
	t.Cleanup(func() {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Errorf("begin manual issue ledger cleanup: %v", err)
			return
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(context.Background(), `SELECT set_config('multica.approval_purge','on',true)`); err != nil {
			t.Errorf("enable manual issue ledger cleanup: %v", err)
			return
		}
		if _, err := tx.Exec(context.Background(), `DELETE FROM autonomous_project_approval_event WHERE project_id=$1`, projectID); err != nil {
			t.Errorf("delete manual issue ledger rows: %v", err)
			return
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Errorf("commit manual issue ledger cleanup: %v", err)
		}
	})

	issueService := NewIssueService(f.q, pool, events.New(), nil, nil)
	provenance := &ManualIssueProvenance{
		ActorID: parseTestUUID(t, ownerID), ActorDisplay: "Project owner", SourceEndpoint: "/api/issues",
	}
	created, err := issueService.Create(ctx, IssueCreateParams{
		WorkspaceID: workspaceID, Title: "manual provenance subject", Status: "todo", Priority: "medium",
		CreatorType: "member", CreatorID: parseTestUUID(t, ownerID), ProjectID: projectID,
	}, IssueCreateOpts{ManualIssueProvenance: provenance})
	if err != nil {
		t.Fatalf("create manual project issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, created.Issue.ID)
	})
	var count int
	var digest, endpoint string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(subject_digest), max(source_endpoint)
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND subject_type='manual_issue' AND subject_id=$3
	`, workspaceID, projectID, created.Issue.ID).Scan(&count, &digest, &endpoint); err != nil {
		t.Fatalf("read committed manual issue event: %v", err)
	}
	if count != 1 || len(digest) != 64 || endpoint != "/api/issues" {
		t.Fatalf("committed manual issue provenance = count:%d digest:%q endpoint:%q", count, digest, endpoint)
	}
	if executable, err := ProjectIssuePlanExecutable(ctx, pool, workspaceID, projectID); err != nil {
		t.Fatalf("check execution predicate after manual issue event: %v", err)
	} else if executable {
		t.Fatal("manual issue provenance made a project executable")
	}

	rejected := errors.New("reject before commit")
	var rollbackIssueID string
	var callbackSawEvent bool
	_, err = issueService.Create(ctx, IssueCreateParams{
		WorkspaceID: workspaceID, Title: "manual provenance rollback", Status: "todo", Priority: "medium",
		CreatorType: "member", CreatorID: parseTestUUID(t, ownerID), ProjectID: projectID,
	}, IssueCreateOpts{
		ManualIssueProvenance: provenance,
		BeforeCommit: func(ctx context.Context, tx pgx.Tx, issue db.Issue) error {
			rollbackIssueID = util.UUIDToString(issue.ID)
			var events int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM autonomous_project_approval_event
				WHERE project_id=$1 AND subject_type='manual_issue' AND subject_id=$2
			`, projectID, rollbackIssueID).Scan(&events); err != nil {
				return err
			}
			callbackSawEvent = events == 1
			return rejected
		},
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("rollback issue create error = %v, want callback rejection", err)
	}
	if !callbackSawEvent || rollbackIssueID == "" {
		t.Fatal("before-commit hook did not see manual issue provenance in the issue transaction")
	}
	var rolledBackIssues, rolledBackEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE workspace_id=$1 AND title='manual provenance rollback'`, workspaceID).Scan(&rolledBackIssues); err != nil {
		t.Fatalf("count rolled-back issue rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM autonomous_project_approval_event
		WHERE project_id=$1 AND subject_type='manual_issue' AND subject_id=$2
	`, projectID, rollbackIssueID).Scan(&rolledBackEvents); err != nil {
		t.Fatalf("count rolled-back manual issue events: %v", err)
	}
	if rolledBackIssues != 0 || rolledBackEvents != 0 {
		t.Fatalf("rollback left issue/event rows: issue=%d event=%d", rolledBackIssues, rolledBackEvents)
	}
	t.Log("PROJECT_ISSUE_PATH_GATE_PASS")
}
