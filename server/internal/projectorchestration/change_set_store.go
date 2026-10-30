package projectorchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type TaskCommit struct {
	WorkspaceID  pgtype.UUID
	ProjectID    pgtype.UUID
	PlanID       pgtype.UUID
	NodeID       pgtype.UUID
	IssueID      pgtype.UUID
	TaskID       pgtype.UUID
	AgentID      pgtype.UUID
	BaseSHA      string
	CommitSHA    string
	BranchName   string
	Worktree     string
	RepoPath     string
	ChangedFiles []string
}

func RecordTaskCommit(ctx context.Context, tx pgx.Tx, commit TaskCommit) error {
	if tx == nil || !commit.WorkspaceID.Valid || !commit.ProjectID.Valid || !commit.PlanID.Valid ||
		!commit.NodeID.Valid || !commit.IssueID.Valid || !commit.TaskID.Valid || !commit.AgentID.Valid {
		return fmt.Errorf("project change set requires a transaction and complete task identity")
	}
	if strings.TrimSpace(commit.BaseSHA) == "" || strings.TrimSpace(commit.CommitSHA) == "" ||
		strings.TrimSpace(commit.BranchName) == "" || strings.TrimSpace(commit.Worktree) == "" ||
		strings.TrimSpace(commit.RepoPath) == "" || len(commit.ChangedFiles) == 0 {
		return fmt.Errorf("project change set requires daemon-observed commit, branch, paths, and changed files")
	}
	changedFiles, err := json.Marshal(commit.ChangedFiles)
	if err != nil {
		return fmt.Errorf("encode project change set files: %w", err)
	}
	lockKey := projectMergeLockKey(commit.WorkspaceID, commit.ProjectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return fmt.Errorf("lock project merge queue before recording a replacement change set: %w", err)
	}
	var alreadyRecorded bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM autonomous_project_change_set WHERE source_task_id=$1
		)
	`, commit.TaskID).Scan(&alreadyRecorded); err != nil {
		return fmt.Errorf("check existing project task change set: %w", err)
	}
	if alreadyRecorded {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_change_set c
		SET merge_status='cancelled', merge_order=NULL, approved_review_task_id=NULL,
		    approved_review_artifact_id=NULL,
		    last_error='superseded by a newer implementation task',
		    merge_evidence=COALESCE(c.merge_evidence,'{}'::jsonb) ||
		      jsonb_build_object('superseded_by_task_id',$3::text,'superseded_at',now()),
		    updated_at=now()
		WHERE c.plan_id=$1 AND c.node_id=$2
		  AND c.merge_status NOT IN ('merged','cancelled')
		  AND EXISTS (
			SELECT 1
			FROM agent_task_queue current_task
			JOIN agent_task_queue prior_task ON prior_task.id=c.source_task_id
			WHERE current_task.id=$3 AND current_task.issue_id=prior_task.issue_id
			  AND (prior_task.created_at,prior_task.id) < (current_task.created_at,current_task.id)
		  )
	`, commit.PlanID, commit.NodeID, commit.TaskID); err != nil {
		return fmt.Errorf("cancel superseded project change sets: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO autonomous_project_change_set (
			workspace_id, project_id, plan_id, node_id, issue_id, source_task_id,
			source_agent_id, base_sha, branch_name, worktree_path, repo_path,
			changed_files, commit_sha, merge_status, ready_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'changes_requested',NULL)
		ON CONFLICT (source_task_id) DO NOTHING
	`, commit.WorkspaceID, commit.ProjectID, commit.PlanID, commit.NodeID, commit.IssueID,
		commit.TaskID, commit.AgentID, strings.TrimSpace(commit.BaseSHA), strings.TrimSpace(commit.BranchName),
		strings.TrimSpace(commit.Worktree), strings.TrimSpace(commit.RepoPath), changedFiles,
		strings.TrimSpace(commit.CommitSHA))
	if err != nil {
		return fmt.Errorf("record project task change set: %w", err)
	}
	return nil
}
