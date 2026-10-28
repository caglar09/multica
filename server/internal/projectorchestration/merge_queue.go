package projectorchestration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

const (
	mergeGitOutputLimit = 64 * 1024
	mergeGitLockTimeout = 2 * time.Second
)

const maxMergeAttempts = 3

func mergeAttemptExhausted(attempt int32) bool { return attempt >= maxMergeAttempts }

var ErrMergeQueueBusy = errors.New("project merge queue is being processed")

// MergeQueue serializes integration per project. It prepares a candidate tree,
// verifies that exact commit through the configured Q2 runner, then advances
// the integration ref with compare-and-swap. The integration ref never points
// at an unverified candidate.
type MergeQueue struct {
	store  *Store
	runner QualityGateRunner
}

func NewMergeQueue(store *Store, runner QualityGateRunner) *MergeQueue {
	return &MergeQueue{store: store, runner: runner}
}

func (q *MergeQueue) ProcessNext(ctx context.Context, workspaceID, projectID pgtype.UUID) (result MergeResult, retErr error) {
	if q == nil || q.store == nil || q.runner == nil {
		return MergeResult{}, errors.New("project merge queue requires a store and quality gate runner")
	}
	if !workspaceID.Valid || !projectID.Valid {
		return MergeResult{}, errors.New("workspace_id and project_id are required")
	}
	conn, err := q.store.mergeQueueConnection(ctx)
	if err != nil {
		return MergeResult{}, err
	}
	defer conn.Release()
	lockKey := projectMergeLockKey(workspaceID, projectID)
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", lockKey).Scan(&locked); err != nil {
		return MergeResult{}, fmt.Errorf("acquire project merge lock: %w", err)
	}
	if !locked {
		return MergeResult{}, ErrMergeQueueBusy
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), mergeGitLockTimeout)
		defer cancel()
		_, _ = conn.Exec(cleanupCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey)
	}()

	claim, found, err := q.store.claimNextMerge(ctx, conn, workspaceID, projectID)
	if err != nil {
		return MergeResult{}, err
	}
	if !found {
		return MergeResult{}, ErrMergeQueueEmpty
	}
	defer func() {
		if retErr != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), mergeGitLockTimeout)
			defer cancel()
			if recordErr := q.store.recordMergeError(cleanupCtx, claim, retErr); recordErr != nil {
				retErr = errors.Join(retErr, recordErr)
			}
		}
	}()
	if mergeEvidenceString(normalizeMergeEvidence(claim.MergeEvidence), "phase") == "quality_verified" {
		return q.reconcileVerifiedClaim(ctx, claim)
	}
	proofCurrent, err := q.store.mergeQueueProofCurrent(ctx, claim)
	if err != nil {
		return MergeResult{ChangeSetID: claim.ChangeSetID}, err
	}
	if !proofCurrent {
		return q.returnForCurrentProof(ctx, claim, normalizeMergeEvidence(claim.MergeEvidence))
	}

	previousEvidence := normalizeMergeEvidence(claim.MergeEvidence)
	if mergeEvidenceString(previousEvidence, "phase") != "claimed" {
		if err := cleanupMergeWorktree(ctx, claim, previousEvidence); err != nil {
			return MergeResult{}, fmt.Errorf("clean up interrupted merge attempt: %w", err)
		}
		if mergeAttemptExhausted(claim.MergeAttempt) {
			message := "merge retry limit exhausted after an interrupted worker attempt"
			previousEvidence["phase"] = "integration_failed"
			previousEvidence["last_error"] = message
			return q.finishTerminal(ctx, claim, "integration_failed", message, previousEvidence)
		}
		claim, err = q.store.restartMergeAttempt(ctx, claim)
		if err != nil {
			return MergeResult{}, err
		}
	}
	return q.prepareVerifyPromote(ctx, claim)
}

func (q *MergeQueue) prepareVerifyPromote(ctx context.Context, claim MergeQueueClaim) (MergeResult, error) {
	result := MergeResult{ChangeSetID: claim.ChangeSetID}
	repoRoot, err := validateMergeSource(ctx, claim)
	if err != nil {
		status := "stale_base"
		if !errors.Is(err, errStaleMergeSource) {
			return result, err
		}
		evidence := normalizeMergeEvidence(claim.MergeEvidence)
		evidence["phase"] = status
		if finishErr := q.store.finishMerge(ctx, claim, status, "", err.Error(), evidence); finishErr != nil {
			return result, errors.Join(err, finishErr)
		}
		result.Status, result.ErrorDetails = status, err.Error()
		return result, nil
	}
	integrationRef := "refs/heads/" + claim.IntegrationBranch
	if err := validateBranchName(ctx, repoRoot, claim.IntegrationBranch); err != nil {
		return q.finishTerminal(ctx, claim, "integration_failed", "invalid project integration branch", map[string]any{
			"phase": "integration_failed", "error": err.Error(),
		})
	}
	baseSHA, refExists, err := readRef(ctx, repoRoot, integrationRef)
	if err != nil {
		return result, err
	}
	if !refExists {
		baseSHA = claim.BaseSHA
	}
	ancestor, err := isAncestor(ctx, repoRoot, claim.BaseSHA, baseSHA)
	if err != nil {
		return result, err
	}
	if !ancestor {
		return q.finishTerminal(ctx, claim, "stale_base", "integration branch no longer contains the change set base", map[string]any{
			"phase": "stale_base", "integration_branch": claim.IntegrationBranch,
			"integration_base_sha": baseSHA, "change_set_base_sha": claim.BaseSHA,
		})
	}

	parent, worktreePath, err := newMergeWorktreePath(claim.ChangeSetID)
	if err != nil {
		return result, err
	}
	if err := os.Mkdir(filepath.Join(parent, "empty-hooks"), 0o700); err != nil {
		_ = os.RemoveAll(parent)
		return result, fmt.Errorf("prepare isolated Git hooks directory: %w", err)
	}
	evidence := map[string]any{
		"attempt": claim.MergeAttempt, "phase": "preparing",
		"integration_branch": claim.IntegrationBranch, "integration_base_sha": baseSHA,
		"integration_ref_missing": !refExists, "worktree_parent": parent,
		"integration_worktree": worktreePath,
		"source_task_id":       util.UUIDToString(claim.SourceTaskID),
		"source_commit_sha":    claim.CommitSHA,
	}
	if err := q.store.persistMergeEvidence(ctx, claim, evidence); err != nil {
		_ = os.RemoveAll(parent)
		return result, err
	}
	if _, err := runGit(ctx, repoRoot, "worktree", "add", "--detach", worktreePath, baseSHA); err != nil {
		return result, err
	}
	if err := persistMergePhase(ctx, q.store, claim, evidence, "merging"); err != nil {
		return result, err
	}
	mergeOutput, mergeErr := mergeCommitIntoWorktree(ctx, worktreePath, parent, claim.CommitSHA)
	if mergeErr != nil {
		conflicts, diffErr := runGit(ctx, worktreePath, "diff", "--name-only", "--diff-filter=U")
		if diffErr == nil && strings.TrimSpace(conflicts) != "" {
			_, _ = runGit(ctx, worktreePath, "merge", "--abort")
			evidence["phase"] = "conflict"
			evidence["conflicts"] = strings.Split(strings.TrimSpace(conflicts), "\n")
			evidence["git_output"] = mergeOutput
			return q.finishTerminal(ctx, claim, "conflict", "merge has unresolved conflicts", evidence)
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if diffErr != nil {
			return result, errors.Join(mergeErr, diffErr)
		}
		return result, mergeErr
	}
	candidateSHA, err := runGit(ctx, worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	if !gitCommitSHA.MatchString(candidateSHA) {
		return result, errors.New("git produced an invalid integration commit SHA")
	}
	evidence["candidate_sha"] = candidateSHA
	evidence["phase"] = "verifying"
	if err := q.store.persistMergeEvidence(ctx, claim, evidence); err != nil {
		return result, err
	}
	gates := make([]map[string]any, 0, 2)
	for _, gateType := range []string{"build", "integration_test"} {
		gateEvidence, gateErr := q.verifyCandidate(ctx, claim, repoRoot, worktreePath, candidateSHA, gateType)
		gates = append(gates, gateEvidence)
		evidence["gates"] = gates
		if gateErr != nil {
			evidence["phase"] = "integration_failed"
			if err := q.store.finishMerge(ctx, claim, "integration_failed", "", gateErr.Error(), evidence); err != nil {
				return result, errors.Join(gateErr, err)
			}
			_ = cleanupMergeWorktree(context.Background(), claim, evidence)
			result.Status, result.ErrorDetails = "integration_failed", gateErr.Error()
			return result, nil
		}
		if err := q.store.persistMergeEvidence(ctx, claim, evidence); err != nil {
			return result, err
		}
	}
	evidence["phase"] = "quality_verified"
	evidence["candidate_sha"] = candidateSHA
	if err := q.store.persistMergeEvidence(ctx, claim, evidence); err != nil {
		return result, err
	}
	return q.promoteVerified(ctx, claim, evidence)
}

func (q *MergeQueue) verifyCandidate(
	ctx context.Context,
	claim MergeQueueClaim,
	repoRoot, worktreePath, commitSHA, gateType string,
) (map[string]any, error) {
	request := QualityGateRequest{
		WorkspaceID: claim.WorkspaceID, ProjectID: claim.ProjectID, PlanID: claim.PlanID,
		NodeID: claim.NodeID, IssueID: claim.IssueID, TaskID: claim.SourceTaskID,
		GateType: gateType, WorktreePath: worktreePath, RepoPath: repoRoot,
		CommitSHA: commitSHA,
		Artifact: map[string]any{
			"purpose":           "project_merge_integration",
			"source_task_id":    util.UUIDToString(claim.SourceTaskID),
			"source_commit_sha": claim.CommitSHA,
		},
	}
	result, err := q.runner.Run(ctx, request)
	evidence := map[string]any{
		"gate_type": gateType, "task_id": util.UUIDToString(claim.SourceTaskID),
		"commit_sha": commitSHA, "worktree_path": worktreePath, "repo_path": repoRoot,
	}
	if err != nil {
		evidence["error"] = err.Error()
		return evidence, fmt.Errorf("run merged-tree %s gate: %w", gateType, err)
	}
	evidence["passed"] = result.Passed
	evidence["runner_type"] = result.RunnerType
	evidence["executed_command"] = result.ExecutedCommand
	evidence["exit_code"] = result.ExitCode
	evidence["duration_ms"] = result.DurationMs
	evidence["artifact_digest"] = result.ArtifactDigest
	evidence["attestation"] = result.Attestation
	evidence["runner_evidence"] = result.Evidence
	if !result.Passed {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = "deterministic quality gate failed"
		}
		evidence["error"] = message
		return evidence, fmt.Errorf("merged-tree %s gate failed: %s", gateType, message)
	}
	if err := ValidateQualityGatePass(request, result); err != nil {
		evidence["passed"] = false
		evidence["error"] = err.Error()
		return evidence, fmt.Errorf("merged-tree %s gate returned unverified evidence: %w", gateType, err)
	}
	return evidence, nil
}

func (q *MergeQueue) promoteVerified(ctx context.Context, claim MergeQueueClaim, evidence map[string]any) (MergeResult, error) {
	result := MergeResult{ChangeSetID: claim.ChangeSetID}
	repoRoot, err := validateRecordedRepoPath(ctx, claim.RepoPath)
	if err != nil {
		return result, err
	}
	ref := "refs/heads/" + claim.IntegrationBranch
	candidateSHA := mergeEvidenceString(evidence, "candidate_sha")
	baseSHA := mergeEvidenceString(evidence, "integration_base_sha")
	if !gitCommitSHA.MatchString(candidateSHA) || !gitCommitSHA.MatchString(baseSHA) {
		return result, errors.New("verified merge attempt is missing exact base and candidate commits")
	}
	proofCurrent, err := q.store.mergeQueueProofCurrent(ctx, claim)
	if err != nil {
		return result, err
	}
	if !proofCurrent {
		return q.returnForCurrentProof(ctx, claim, evidence)
	}
	if err := compareAndSwapRef(ctx, repoRoot, ref, candidateSHA, baseSHA, evidence["integration_ref_missing"] == true); err != nil {
		currentSHA, exists, readErr := readRef(ctx, repoRoot, ref)
		if readErr != nil {
			return result, errors.Join(err, readErr)
		}
		if !exists || currentSHA != candidateSHA {
			evidence["phase"] = "stale_base"
			if finishErr := q.store.finishMerge(ctx, claim, "stale_base", "", err.Error(), evidence); finishErr != nil {
				return result, errors.Join(err, finishErr)
			}
			_ = cleanupMergeWorktree(context.Background(), claim, evidence)
			result.Status, result.ErrorDetails = "stale_base", err.Error()
			return result, nil
		}
	}
	return q.finalizePromoted(ctx, claim, evidence)
}

func (q *MergeQueue) returnForCurrentProof(ctx context.Context, claim MergeQueueClaim, evidence map[string]any) (MergeResult, error) {
	message := "review or unit-test evidence is no longer current"
	evidence["phase"] = "evidence_stale"
	if err := q.store.returnMergeToReview(ctx, claim, evidence); err != nil {
		return MergeResult{ChangeSetID: claim.ChangeSetID}, err
	}
	_ = cleanupMergeWorktree(context.Background(), claim, evidence)
	return MergeResult{ChangeSetID: claim.ChangeSetID, Status: "changes_requested", ErrorDetails: message}, nil
}

func (q *MergeQueue) reconcileVerifiedClaim(ctx context.Context, claim MergeQueueClaim) (MergeResult, error) {
	evidence := normalizeMergeEvidence(claim.MergeEvidence)
	repoRoot, err := validateRecordedRepoPath(ctx, claim.RepoPath)
	if err != nil {
		return MergeResult{ChangeSetID: claim.ChangeSetID}, err
	}
	ref := "refs/heads/" + claim.IntegrationBranch
	candidateSHA := mergeEvidenceString(evidence, "candidate_sha")
	baseSHA := mergeEvidenceString(evidence, "integration_base_sha")
	if !gitCommitSHA.MatchString(candidateSHA) || !gitCommitSHA.MatchString(baseSHA) || !verifiedGateEvidenceMatches(evidence, candidateSHA) {
		return MergeResult{ChangeSetID: claim.ChangeSetID}, errors.New("interrupted merge has incomplete exact-commit quality evidence")
	}
	currentSHA, exists, err := readRef(ctx, repoRoot, ref)
	if err != nil {
		return MergeResult{ChangeSetID: claim.ChangeSetID}, err
	}
	if exists && currentSHA == candidateSHA {
		return q.finalizePromoted(ctx, claim, evidence)
	}
	if evidence["integration_ref_missing"] == true && !exists || exists && currentSHA == baseSHA {
		return q.promoteVerified(ctx, claim, evidence)
	}
	message := "integration branch changed while recovering a verified merge attempt"
	evidence["phase"] = "stale_base"
	if err := q.store.finishMerge(ctx, claim, "stale_base", "", message, evidence); err != nil {
		return MergeResult{ChangeSetID: claim.ChangeSetID}, err
	}
	_ = cleanupMergeWorktree(context.Background(), claim, evidence)
	return MergeResult{ChangeSetID: claim.ChangeSetID, Status: "stale_base", ErrorDetails: message}, nil
}

func (q *MergeQueue) finalizePromoted(ctx context.Context, claim MergeQueueClaim, evidence map[string]any) (MergeResult, error) {
	sha := mergeEvidenceString(evidence, "candidate_sha")
	evidence["phase"] = "merged"
	evidence["promoted_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := q.store.finishMerge(ctx, claim, "merged", sha, "", evidence); err != nil {
		return MergeResult{ChangeSetID: claim.ChangeSetID, MergedSHA: sha}, err
	}
	_ = cleanupMergeWorktree(context.Background(), claim, evidence)
	return MergeResult{ChangeSetID: claim.ChangeSetID, MergedSHA: sha, Status: "merged"}, nil
}

func (q *MergeQueue) finishTerminal(ctx context.Context, claim MergeQueueClaim, status, message string, evidence map[string]any) (MergeResult, error) {
	if err := q.store.finishMerge(ctx, claim, status, "", message, evidence); err != nil {
		return MergeResult{ChangeSetID: claim.ChangeSetID}, errors.Join(errors.New(message), err)
	}
	_ = cleanupMergeWorktree(context.Background(), claim, evidence)
	return MergeResult{ChangeSetID: claim.ChangeSetID, Status: status, ErrorDetails: message}, nil
}

var errStaleMergeSource = errors.New("stale project change set")

func validateMergeSource(ctx context.Context, claim MergeQueueClaim) (string, error) {
	if !gitCommitSHA.MatchString(strings.TrimSpace(claim.BaseSHA)) || !gitCommitSHA.MatchString(strings.TrimSpace(claim.CommitSHA)) {
		return "", fmt.Errorf("%w: recorded base and source must be full commit SHAs", errStaleMergeSource)
	}
	_, repoRoot, err := validateQualityWorktree(ctx, claim.WorktreePath, claim.RepoPath, claim.CommitSHA)
	if err != nil {
		return "", fmt.Errorf("%w: validate recorded task worktree: %v", errStaleMergeSource, err)
	}
	if err := validateBranchName(ctx, repoRoot, claim.BranchName); err != nil {
		return "", fmt.Errorf("%w: validate recorded source branch: %v", errStaleMergeSource, err)
	}
	branchSHA, err := runGit(ctx, repoRoot, "rev-parse", "--verify", "refs/heads/"+claim.BranchName)
	if err != nil || !strings.EqualFold(branchSHA, claim.CommitSHA) {
		return "", fmt.Errorf("%w: source branch no longer points at the recorded commit", errStaleMergeSource)
	}
	ancestor, err := isAncestor(ctx, repoRoot, claim.BaseSHA, claim.CommitSHA)
	if err != nil {
		return "", err
	}
	if !ancestor {
		return "", fmt.Errorf("%w: source commit does not descend from its recorded base", errStaleMergeSource)
	}
	return repoRoot, nil
}

func validateRecordedRepoPath(ctx context.Context, repoPath string) (string, error) {
	repoRoot, err := filepath.Abs(strings.TrimSpace(repoPath))
	if err != nil || strings.TrimSpace(repoPath) == "" {
		return "", errors.New("recorded repository path is invalid")
	}
	repoRoot, err = filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve recorded repository: %w", err)
	}
	root, _, err := inspectQualityGitPath(ctx, repoRoot)
	if err != nil {
		return "", fmt.Errorf("inspect recorded repository: %w", err)
	}
	if root != repoRoot {
		return "", errors.New("recorded repository path is not its Git root")
	}
	return repoRoot, nil
}

func validateBranchName(ctx context.Context, repoRoot, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "refs/") {
		return errors.New("branch name must be a non-empty short Git branch name")
	}
	if _, err := runGit(ctx, repoRoot, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid branch name: %w", err)
	}
	return nil
}

func readRef(ctx context.Context, repoRoot, ref string) (string, bool, error) {
	output, exitCode, err := runGitExit(ctx, repoRoot, "show-ref", "--verify", "--hash", ref)
	if err != nil {
		return "", false, err
	}
	if exitCode == 1 {
		return "", false, nil
	}
	if exitCode != 0 {
		return "", false, fmt.Errorf("read integration ref %s: git exited %d: %s", ref, exitCode, output)
	}
	sha := strings.TrimSpace(output)
	if !gitCommitSHA.MatchString(sha) {
		return "", false, fmt.Errorf("integration ref %s points at an invalid commit SHA", ref)
	}
	return sha, true, nil
}

func isAncestor(ctx context.Context, repoRoot, ancestor, descendant string) (bool, error) {
	_, exitCode, err := runGitExit(ctx, repoRoot, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	switch exitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("git merge-base --is-ancestor exited %d", exitCode)
	}
}

func compareAndSwapRef(ctx context.Context, repoRoot, ref, newSHA, oldSHA string, expectedMissing bool) error {
	if expectedMissing {
		input := []byte("start\ncreate " + ref + " " + newSHA + "\nprepare\ncommit\n")
		_, exitCode, err := runGitInput(ctx, repoRoot, input, "update-ref", "--stdin")
		if err != nil {
			return err
		}
		if exitCode != 0 {
			return fmt.Errorf("create integration ref %s with compare-and-swap failed", ref)
		}
		return nil
	}
	_, exitCode, err := runGitExit(ctx, repoRoot, "update-ref", ref, newSHA, oldSHA)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("integration ref %s changed from captured base %s", ref, oldSHA)
	}
	return nil
}

func mergeCommitIntoWorktree(ctx context.Context, worktreePath, parent, sourceSHA string) (string, error) {
	absParent, err := filepath.Abs(parent)
	if err != nil || parent == "" {
		return "", errors.New("merge worktree parent path is invalid")
	}
	absWorktree, err := filepath.Abs(worktreePath)
	if err != nil || filepath.Clean(absWorktree) != filepath.Join(filepath.Clean(absParent), "tree") {
		return "", errors.New("merge worktree path is outside its generated parent")
	}
	hooksPath := filepath.Join(absParent, "empty-hooks")
	if err := os.MkdirAll(hooksPath, 0o700); err != nil {
		return "", fmt.Errorf("prepare isolated Git hooks directory: %w", err)
	}
	driverOverrides, err := disabledMergeDriverOverrides(ctx, worktreePath)
	if err != nil {
		return "", err
	}
	args := []string{"-c", "core.hooksPath=" + hooksPath}
	args = append(args, driverOverrides...)
	args = append(args, "merge", "--no-ff", "--no-edit", sourceSHA)
	return runGitWithEnv(ctx, worktreePath, map[string]string{
		"GIT_AUTHOR_NAME":     "Multica Merge Queue",
		"GIT_AUTHOR_EMAIL":    "merge-queue@multica.invalid",
		"GIT_COMMITTER_NAME":  "Multica Merge Queue",
		"GIT_COMMITTER_EMAIL": "merge-queue@multica.invalid",
	}, args...)
}

func disabledMergeDriverOverrides(ctx context.Context, repoRoot string) ([]string, error) {
	output, exitCode, err := runGitExit(ctx, repoRoot, "config", "--local", "--includes", "--name-only", "--get-regexp", `^merge\..*\.driver$`)
	if err != nil {
		return nil, fmt.Errorf("inspect configured Git merge drivers: %w", err)
	}
	if exitCode == 1 && strings.TrimSpace(output) == "" {
		return nil, nil
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("inspect configured Git merge drivers: git exited %d", exitCode)
	}
	overrides := make([]string, 0)
	for _, key := range strings.Split(output, "\n") {
		key = strings.TrimSpace(key)
		lower := strings.ToLower(key)
		if key == "" {
			continue
		}
		if !strings.HasPrefix(lower, "merge.") || !strings.HasSuffix(lower, ".driver") {
			return nil, fmt.Errorf("unexpected Git merge-driver config key %q", key)
		}
		// A custom merge attribute may still be present in the project tree.
		// Make such conflicts fail closed rather than executing a repo-configured
		// shell command on the server.
		overrides = append(overrides, "-c", key+"=git")
	}
	return overrides, nil
}

func verifiedGateEvidenceMatches(evidence map[string]any, candidateSHA string) bool {
	gates, ok := evidence["gates"].([]any)
	if !ok || len(gates) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, rawGate := range gates {
		gate, ok := rawGate.(map[string]any)
		if !ok || gate["passed"] != true || gate["commit_sha"] != candidateSHA || gate["task_id"] != evidence["source_task_id"] {
			return false
		}
		gateType, _ := gate["gate_type"].(string)
		seen[gateType] = true
	}
	return seen["build"] && seen["integration_test"]
}

func persistMergePhase(ctx context.Context, store *Store, claim MergeQueueClaim, evidence map[string]any, phase string) error {
	evidence["phase"] = phase
	return store.persistMergeEvidence(ctx, claim, evidence)
}

func newMergeWorktreePath(changeSetID pgtype.UUID) (string, string, error) {
	parent, err := os.MkdirTemp(os.TempDir(), "multica-merge-"+util.UUIDToString(changeSetID)+"-")
	if err != nil {
		return "", "", fmt.Errorf("create merge worktree directory: %w", err)
	}
	return parent, filepath.Join(parent, "tree"), nil
}

func cleanupMergeWorktree(ctx context.Context, claim MergeQueueClaim, evidence map[string]any) error {
	parent := mergeEvidenceString(evidence, "worktree_parent")
	worktree := mergeEvidenceString(evidence, "integration_worktree")
	if parent == "" && worktree == "" {
		return nil
	}
	if !safeMergeWorktreePath(parent, worktree, claim.ChangeSetID) {
		return errors.New("refuse to clean an unrecognized merge worktree path")
	}
	if _, err := os.Lstat(worktree); err == nil {
		if _, removeErr := runGit(ctx, claim.RepoPath, "worktree", "remove", "--force", worktree); removeErr != nil {
			if fileErr := os.RemoveAll(worktree); fileErr != nil {
				return errors.Join(removeErr, fileErr)
			}
			if _, pruneErr := runGit(ctx, claim.RepoPath, "worktree", "prune"); pruneErr != nil {
				return errors.Join(removeErr, pruneErr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.RemoveAll(parent); err != nil {
		return fmt.Errorf("remove temporary merge worktree directory: %w", err)
	}
	return nil
}

func safeMergeWorktreePath(parent, worktree string, changeSetID pgtype.UUID) bool {
	if !changeSetID.Valid || parent == "" || worktree == "" {
		return false
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil || filepath.Dir(absParent) != filepath.Clean(tempRoot) {
		return false
	}
	prefix := "multica-merge-" + util.UUIDToString(changeSetID) + "-"
	if !strings.HasPrefix(filepath.Base(absParent), prefix) {
		return false
	}
	absWorktree, err := filepath.Abs(worktree)
	return err == nil && filepath.Clean(absWorktree) == filepath.Join(filepath.Clean(absParent), "tree")
}

type limitedMergeOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedMergeOutput) Write(data []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = w.buffer.Write(data[:remaining])
	}
	if len(data) > remaining {
		w.truncated = true
	}
	return len(data), nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	output, code, err := runGitExit(ctx, dir, args...)
	if err != nil {
		return output, err
	}
	if code != 0 {
		return output, fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, strings.TrimSpace(output))
	}
	return strings.TrimSpace(output), nil
}

func runGitWithEnv(ctx context.Context, dir string, env map[string]string, args ...string) (string, error) {
	return runGitProcess(ctx, dir, nil, env, args...)
}

func runGitInput(ctx context.Context, dir string, input []byte, args ...string) (string, int, error) {
	return runGitProcessExit(ctx, dir, input, nil, args...)
}

func runGitExit(ctx context.Context, dir string, args ...string) (string, int, error) {
	return runGitProcessExit(ctx, dir, nil, nil, args...)
}

func runGitProcess(ctx context.Context, dir string, input []byte, env map[string]string, args ...string) (string, error) {
	output, code, err := runGitProcessExit(ctx, dir, input, env, args...)
	if err != nil {
		return output, err
	}
	if code != 0 {
		return output, fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, strings.TrimSpace(output))
	}
	return strings.TrimSpace(output), nil
}

func runGitProcessExit(ctx context.Context, dir string, input []byte, env map[string]string, args ...string) (string, int, error) {
	program, err := exec.LookPath("git")
	if err != nil {
		return "", -1, fmt.Errorf("git is unavailable: %w", err)
	}
	hooksPath, err := os.MkdirTemp(os.TempDir(), "multica-git-hooks-")
	if err != nil {
		return "", -1, fmt.Errorf("create isolated Git hooks directory: %w", err)
	}
	defer os.RemoveAll(hooksPath)
	gitArgs := append([]string{"-c", "core.hooksPath=" + hooksPath, "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.CommandContext(ctx, program, gitArgs...)
	cmd.Dir = dir
	if len(input) > 0 {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Env = mergeGitEnv(env)
	output := &limitedMergeOutput{limit: mergeGitOutputLimit}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	if output.truncated && len(args) > 0 && args[0] == "config" {
		return output.buffer.String(), -1, errors.New("Git merge-driver config output exceeded the safe inspection limit")
	}
	if err == nil {
		return output.buffer.String(), 0, nil
	}
	if ctx.Err() != nil {
		return output.buffer.String(), -1, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return output.buffer.String(), exitErr.ExitCode(), nil
	}
	return output.buffer.String(), -1, fmt.Errorf("run git %s: %w", strings.Join(args, " "), err)
}

func mergeGitEnv(overrides map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(overrides)+2)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			out = append(out, value)
		}
	}
	out = append(out, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	for key, value := range overrides {
		out = append(out, key+"="+value)
	}
	return out
}
