package workflowruntime

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
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
