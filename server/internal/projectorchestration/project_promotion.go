package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

const projectPromotionRetryDelay = 5 * time.Minute

type projectPromotionRepo struct {
	path        string
	verifiedSHA string
	repoRoot    string
	masterSHA   string
	masterRef   string
	masterPath  string
}

// PromoteCompletedProject advances master only after every task is terminal
// and every reviewed change set has passed the merge queue's integration gates.
func (q *MergeQueue) PromoteCompletedProject(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) (MergeResult, bool, error) {
	if q == nil || q.store == nil {
		return MergeResult{}, false, errors.New("project merge queue requires a store")
	}
	if !workspaceID.Valid || !projectID.Valid {
		return MergeResult{}, false, errors.New("workspace_id and project_id are required")
	}
	return q.store.promoteCompletedProject(ctx, workspaceID, projectID)
}

func (s *Store) promoteCompletedProject(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) (MergeResult, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MergeResult{}, false, fmt.Errorf("begin project promotion: %w", err)
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `
		SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))
	`, projectMergeLockKey(workspaceID, projectID)).Scan(&locked); err != nil {
		return MergeResult{}, false, fmt.Errorf("acquire project promotion lock: %w", err)
	}
	if !locked {
		return MergeResult{}, false, ErrMergeQueueBusy
	}

	var planID, finalChangeSetID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT p.id, final.id
		FROM autonomous_project_plan p
		JOIN LATERAL (
			SELECT c.id, c.merge_evidence
			FROM autonomous_project_change_set c
			WHERE c.plan_id=p.id AND c.merge_status='merged'
			ORDER BY c.merge_order DESC NULLS LAST, c.merged_at DESC, c.id DESC
			LIMIT 1
		) final ON TRUE
		WHERE p.workspace_id=$1 AND p.project_id=$2
		  AND p.revision=(
			SELECT MAX(latest.revision) FROM autonomous_project_plan latest
			WHERE latest.workspace_id=p.workspace_id AND latest.project_id=p.project_id
		  )
		  AND p.status IN ('active','blocked','completed')
		  AND NOT EXISTS (
			SELECT 1 FROM autonomous_project_plan_node n
			WHERE n.plan_id=p.id AND n.status NOT IN ('completed','cancelled')
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM autonomous_project_change_set c
			WHERE c.plan_id=p.id AND c.merge_status NOT IN ('merged','cancelled')
		  )
		  AND COALESCE(final.merge_evidence #>> '{project_promotion,status}','') <> 'promoted'
		  AND (
			COALESCE(final.merge_evidence #>> '{project_promotion,status}','') <> 'blocked'
			OR COALESCE((final.merge_evidence #>> '{project_promotion,retry_after}')::timestamptz,'-infinity'::timestamptz) <= now()
		  )
		FOR UPDATE OF p
	`, workspaceID, projectID).Scan(&planID, &finalChangeSetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MergeResult{}, false, ErrMergeQueueEmpty
	}
	if err != nil {
		return MergeResult{}, false, fmt.Errorf("load completed project for promotion: %w", err)
	}
	var unresolvedEscalation bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM autonomous_project_escalation
			WHERE plan_id=$1 AND status IN ('open','acknowledged')
		)
	`, planID).Scan(&unresolvedEscalation); err != nil {
		return MergeResult{}, false, fmt.Errorf("check project escalations before promotion: %w", err)
	}
	if unresolvedEscalation {
		return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID,
			errors.New("project has unresolved escalations; master promotion requires a clean final review"))
	}

	rows, err := tx.Query(ctx, `
		SELECT id, COALESCE(repo_path,''), COALESCE(integration_branch,''), COALESCE(merged_sha,'')
		FROM autonomous_project_change_set
		WHERE plan_id=$1 AND merge_status='merged'
		ORDER BY merge_order ASC NULLS LAST, merged_at ASC, id ASC
	`, planID)
	if err != nil {
		return MergeResult{}, false, fmt.Errorf("list completed project change sets: %w", err)
	}
	reposByPath := make(map[string]*projectPromotionRepo)
	for rows.Next() {
		var changeSetID pgtype.UUID
		var repoPath, branch, mergedSHA string
		if err := rows.Scan(&changeSetID, &repoPath, &branch, &mergedSHA); err != nil {
			rows.Close()
			return MergeResult{}, false, fmt.Errorf("read completed project change set: %w", err)
		}
		if strings.TrimSpace(repoPath) == "" || branch != "develop" || !gitCommitSHA.MatchString(mergedSHA) {
			rows.Close()
			return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID,
				fmt.Errorf("change set %s has no valid develop integration proof", util.UUIDToString(changeSetID)))
		}
		reposByPath[repoPath] = &projectPromotionRepo{path: repoPath, verifiedSHA: mergedSHA}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MergeResult{}, false, fmt.Errorf("iterate completed project change sets: %w", err)
	}
	rows.Close()
	if len(reposByPath) == 0 {
		return MergeResult{}, false, ErrMergeQueueEmpty
	}

	repoPaths := make([]string, 0, len(reposByPath))
	for path := range reposByPath {
		repoPaths = append(repoPaths, path)
	}
	sort.Strings(repoPaths)
	for _, path := range repoPaths {
		repo := reposByPath[path]
		repo.repoRoot, err = validateRecordedRepoPath(ctx, repo.path)
		if err != nil {
			return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID, err)
		}
		developSHA, exists, err := readRef(ctx, repo.repoRoot, "refs/heads/develop")
		if err != nil || !exists || developSHA != repo.verifiedSHA {
			if err == nil {
				err = fmt.Errorf("develop in %q no longer points at the last verified project change set", repo.repoRoot)
			}
			return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID, err)
		}
		repo.masterRef = "refs/heads/master"
		repo.masterSHA, exists, err = readRef(ctx, repo.repoRoot, repo.masterRef)
		if err != nil {
			return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID, err)
		}
		if !exists {
			repo.masterRef = "refs/remotes/origin/master"
			repo.masterSHA, exists, err = readRef(ctx, repo.repoRoot, repo.masterRef)
			if err != nil || !exists {
				if err == nil {
					err = fmt.Errorf("repository %q has no master or origin/master ref", repo.repoRoot)
				}
				return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID, err)
			}
		}
		ancestor, err := isAncestor(ctx, repo.repoRoot, repo.masterSHA, repo.verifiedSHA)
		if err != nil || !ancestor {
			if err == nil {
				err = fmt.Errorf("master in %q has diverged from develop; refusing a non-fast-forward promotion", repo.repoRoot)
			}
			return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID, err)
		}
		if repo.masterRef == "refs/heads/master" && repo.masterSHA != repo.verifiedSHA {
			repo.masterPath, err = cleanBranchWorktree(ctx, repo.repoRoot, repo.masterRef, repo.masterSHA)
			if err != nil {
				return s.blockProjectPromotion(ctx, tx, planID, finalChangeSetID, err)
			}
		}
	}

	promoted := make([]map[string]string, 0, len(reposByPath))
	for _, path := range repoPaths {
		repo := reposByPath[path]
		if repo.masterRef != "refs/heads/master" || repo.masterSHA != repo.verifiedSHA {
			if repo.masterPath != "" {
				currentSHA, exists, err := readRef(ctx, repo.repoRoot, "refs/heads/master")
				if err != nil || !exists || currentSHA != repo.masterSHA {
					if err == nil {
						err = errors.New("master changed after promotion preflight")
					}
					return s.blockProjectPromotionWithRepos(ctx, tx, planID, finalChangeSetID, err, promoted)
				}
				if _, err := runGit(ctx, repo.masterPath, "merge", "--ff-only", repo.verifiedSHA); err != nil {
					return s.blockProjectPromotionWithRepos(ctx, tx, planID, finalChangeSetID,
						fmt.Errorf("fast-forward master in %q: %w", repo.masterPath, err), promoted)
				}
			} else if repo.masterRef == "refs/heads/master" {
				if err := compareAndSwapRef(ctx, repo.repoRoot, "refs/heads/master", repo.verifiedSHA, repo.masterSHA, false); err != nil {
					return s.blockProjectPromotionWithRepos(ctx, tx, planID, finalChangeSetID, err, promoted)
				}
			} else if err := createLocalMasterFromRemote(ctx, repo.repoRoot, repo.masterSHA, repo.verifiedSHA); err != nil {
				return s.blockProjectPromotionWithRepos(ctx, tx, planID, finalChangeSetID, err, promoted)
			}
		}
		promoted = append(promoted, map[string]string{
			"repo_path": repo.repoRoot, "master_sha": repo.verifiedSHA, "develop_sha": repo.verifiedSHA,
		})
	}

	evidence, err := json.Marshal(map[string]any{
		"status": "promoted", "repositories": promoted, "promoted_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return MergeResult{}, false, fmt.Errorf("encode project promotion evidence: %w", err)
	}
	if err := persistProjectPromotion(ctx, tx, planID, finalChangeSetID, evidence, "completed"); err != nil {
		return MergeResult{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MergeResult{}, false, fmt.Errorf("commit project promotion: %w", err)
	}
	return MergeResult{ChangeSetID: finalChangeSetID, Status: "promoted"}, true, nil
}

func cleanBranchWorktree(ctx context.Context, repoRoot, branchRef, expectedSHA string) (string, error) {
	output, err := runGit(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("list repository worktrees: %w", err)
	}
	var path, branch string
	for _, line := range strings.Split(output+"\n", "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path, branch = strings.TrimPrefix(line, "worktree "), ""
		} else if strings.HasPrefix(line, "branch ") {
			branch = strings.TrimPrefix(line, "branch ")
			if branch == branchRef {
				currentSHA, exists, err := readRef(ctx, repoRoot, branch)
				if err != nil || !exists || currentSHA != expectedSHA {
					if err == nil {
						err = fmt.Errorf("checked-out branch %s changed during promotion preflight", branchRef)
					}
					return "", err
				}
				status, err := runGit(ctx, path, "status", "--porcelain", "--untracked-files=normal")
				if err != nil {
					return "", fmt.Errorf("inspect checked-out branch %s: %w", branchRef, err)
				}
				if strings.TrimSpace(status) != "" {
					return "", fmt.Errorf("checked-out branch worktree %q has uncommitted changes; refusing to promote", path)
				}
				return path, nil
			}
		}
	}
	return "", nil
}

func createLocalMasterFromRemote(ctx context.Context, repoRoot, remoteSHA, developSHA string) error {
	input := []byte(fmt.Sprintf("start\nverify refs/remotes/origin/master %s\ncreate refs/heads/master %s\nprepare\ncommit\n", remoteSHA, developSHA))
	_, exitCode, err := runGitInput(ctx, repoRoot, input, "update-ref", "--stdin")
	if err != nil {
		return fmt.Errorf("create local master from verified origin/master: %w", err)
	}
	if exitCode != 0 {
		return errors.New("create local master failed because origin/master changed")
	}
	return nil
}

func (s *Store) blockProjectPromotion(
	ctx context.Context,
	tx pgx.Tx,
	planID, changeSetID pgtype.UUID,
	reason error,
) (MergeResult, bool, error) {
	return s.blockProjectPromotionWithRepos(ctx, tx, planID, changeSetID, reason, nil)
}

func (s *Store) blockProjectPromotionWithRepos(
	ctx context.Context,
	tx pgx.Tx,
	planID, changeSetID pgtype.UUID,
	reason error,
	promoted []map[string]string,
) (MergeResult, bool, error) {
	if reason == nil {
		reason = errors.New("project promotion failed")
	}
	evidence, err := json.Marshal(map[string]any{
		"status": "blocked", "error": reason.Error(), "repositories_promoted": promoted,
		"blocked_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"retry_after": time.Now().UTC().Add(projectPromotionRetryDelay).Format(time.RFC3339Nano),
	})
	if err != nil {
		return MergeResult{}, false, fmt.Errorf("encode blocked project promotion evidence: %w", err)
	}
	if err := persistProjectPromotion(ctx, tx, planID, changeSetID, evidence, "blocked"); err != nil {
		return MergeResult{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MergeResult{}, false, fmt.Errorf("persist blocked project promotion: %w", err)
	}
	return MergeResult{ChangeSetID: changeSetID, Status: "promotion_blocked", ErrorDetails: reason.Error()}, true, nil
}

func persistProjectPromotion(
	ctx context.Context,
	tx pgx.Tx,
	planID, changeSetID pgtype.UUID,
	evidence []byte,
	planStatus string,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET merge_evidence=merge_evidence || jsonb_build_object('project_promotion',$3::jsonb), updated_at=now()
		WHERE id=$1 AND plan_id=$2 AND merge_status='merged'
	`, changeSetID, planID, evidence); err != nil {
		return fmt.Errorf("record project promotion result: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan SET status=$2, updated_at=now() WHERE id=$1
	`, planID, planStatus); err != nil {
		return fmt.Errorf("update project promotion status: %w", err)
	}
	return nil
}
