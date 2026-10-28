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
