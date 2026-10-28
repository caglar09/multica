package projectorchestration

import (
	"errors"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
)

var ErrQualityGateUnavailable = errors.New("required deterministic quality gate runner is unavailable")

// ValidateQualityGatePass rejects a positive result unless its attributable
// evidence matches the exact task, worktree, repository, allowed command, and
// recorded commit. Webhook results must already have a verified HMAC.
func ValidateQualityGatePass(request QualityGateRequest, result QualityGateResult) error {
	if !result.Passed || result.ExitCode == nil || *result.ExitCode != 0 {
		return errors.New("quality gate did not return a successful process exit")
	}
	if !request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.PlanID.Valid ||
		!request.NodeID.Valid || !request.IssueID.Valid || !request.TaskID.Valid ||
		!gitCommitSHA.MatchString(request.CommitSHA) {
		return errors.New("quality gate request identity is incomplete")
	}
	if result.TaskID != util.UUIDToString(request.TaskID) || result.GateType != request.GateType ||
		result.WorktreePath != request.WorktreePath || result.RepoPath != request.RepoPath ||
		!strings.EqualFold(result.CommitSHA, request.CommitSHA) || result.Error != "" {
		return errors.New("quality gate result does not match the exact task, worktree, repository, and commit")
	}
	command, err := detectDaemonQualityCommand(request.WorktreePath, request.GateType, request.CommitSHA)
	if err != nil {
		return fmt.Errorf("detect required quality command: %w", err)
	}
	if result.ExecutedCommand != command.display || result.DurationMs < 0 ||
		result.DurationMs > defaultDaemonQualityTimeout.Milliseconds() ||
		len(result.Stdout) > maxDaemonQualityOutput || len(result.Stderr) > maxDaemonQualityOutput ||
		result.TestCount < 0 || result.FailedCount < 0 || result.FailedCount > result.TestCount {
		return errors.New("quality gate result has malformed command, duration, output, or test-count evidence")
	}
	if result.ArtifactDigest == "" || !digestSHA256.MatchString(result.ArtifactDigest) ||
		result.ArtifactDigest != qualityResultDigest(result) || result.Evidence == nil {
		return errors.New("quality gate result digest or evidence is missing or malformed")
	}
	evidence := result.Evidence
	if command.project == "node" {
		packageManagerVersion, ok := qualityEvidenceString(evidence, "package_manager_version")
		if !ok || packageManagerVersion != qualityPNPMVersion {
			return errors.New("quality gate did not preflight the configured pnpm version")
		}
	}
	taskID, taskOK := qualityEvidenceString(evidence, "task_id")
	gateType, gateOK := qualityEvidenceString(evidence, "gate_type")
	worktree, worktreeOK := qualityEvidenceString(evidence, "worktree_path")
	repo, repoOK := qualityEvidenceString(evidence, "repo_path")
	commit, commitOK := qualityEvidenceString(evidence, "commit_sha")
	executed, commandOK := qualityEvidenceString(evidence, "executed_command")
	runner, runnerOK := qualityEvidenceString(evidence, "runner_type")
	evidenceExit, exitOK := qualityEvidenceInt(evidence, "exit_code")
	evidenceDigest, digestOK := qualityEvidenceString(evidence, "artifact_digest")
	stdout, stdoutOK := evidence["stdout"].(string)
	stderr, stderrOK := evidence["stderr"].(string)
	duration, durationOK := qualityEvidenceInt(evidence, "duration_ms")
	testCount, testCountOK := qualityEvidenceInt(evidence, "test_count")
	failedCount, failedCountOK := qualityEvidenceInt(evidence, "failed_count")
	truncated, truncatedOK := evidence["output_truncated"].(bool)
	if !taskOK || taskID != result.TaskID || !gateOK || gateType != result.GateType ||
		!worktreeOK || worktree != result.WorktreePath || !repoOK || repo != result.RepoPath ||
		!commitOK || !strings.EqualFold(commit, result.CommitSHA) || !commandOK || executed != result.ExecutedCommand ||
		!runnerOK || runner != result.RunnerType || !exitOK || evidenceExit != *result.ExitCode ||
		!digestOK || evidenceDigest != result.ArtifactDigest || !stdoutOK || stdout != result.Stdout ||
		!stderrOK || stderr != result.Stderr || !durationOK || int64(duration) != result.DurationMs ||
		!testCountOK || testCount != result.TestCount || !failedCountOK || failedCount != result.FailedCount ||
		!truncatedOK || truncated != result.OutputTruncated {
		return errors.New("quality gate evidence fields do not match the recorded result")
	}
	if evidenceError, exists := evidence["error"]; exists {
		message, ok := evidenceError.(string)
		if !ok || message != result.Error {
			return errors.New("quality gate error evidence does not match the recorded result")
		}
	}
	image, imageOK := qualityEvidenceString(evidence, "container_image")
	imageID, imageIDOK := qualityEvidenceString(evidence, "container_image_id")
	snapshot, snapshotOK := qualityEvidenceString(evidence, "source_snapshot_digest")
	if !imageOK || image != command.image || !imageIDOK || !containerImageID.MatchString(imageID) ||
		!snapshotOK || !digestSHA256.MatchString(snapshot) || evidence["network_disabled"] != true ||
		evidence["host_environment_inherited"] != false || evidence["task_worktree_mounted"] != false {
		return errors.New("quality gate did not prove the configured OCI isolation boundary")
	}
	if !qualityResourceLimitsMatch(evidence["resource_limits"]) {
		return errors.New("quality gate did not prove the configured container resource limits")
	}
	switch result.RunnerType {
	case "daemon_oci":
		if evidence["source_snapshot"] != "git_archive_of_recorded_commit" {
			return errors.New("daemon quality result is not bound to a committed source snapshot")
		}
	case "webhook_oci":
		attestation, attestationOK := qualityEvidenceString(evidence, "attestation")
		if !attestationOK || attestation != result.Attestation || evidence["remote_attestation_verified"] != true {
			return errors.New("webhook quality result does not contain a verified remote attestation")
		}
	default:
		return errors.New("quality gate result came from an unsupported runner")
	}
	return nil
}

func qualityResourceLimitsMatch(value any) bool {
	limits := make(map[string]string, 4)
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			limits[key] = item
		}
	case map[string]any:
		for key, item := range typed {
			text, ok := item.(string)
			if !ok {
				return false
			}
			limits[key] = text
		}
	default:
		return false
	}
	return len(limits) == 4 && limits["memory"] == qualityOCIMemoryLimit &&
		limits["cpus"] == qualityOCICPUQuota && limits["pids"] == qualityOCIPIDLimit &&
		limits["tmpfs"] == qualityOCITmpLimit
}

// QualityRequirement is derived entirely from backend-owned policy. Models may
// propose QA nodes, but they cannot weaken these minimum gates.
type QualityRequirement struct {
	GateType      string
	Deterministic bool
}

func autonomyRank(level AutonomyLevel) int {
	switch level {
	case AutonomyAssisted:
		return 1
	case AutonomyDevelopment:
		return 2
	case AutonomyDelivery:
		return 3
	case AutonomyClosedLoop:
		return 4
	default:
		return 0
	}
}

// RequiredQualityGates is the backend-owned minimum quality policy.
//
// assisted: structured semantic review for code changes.
// development: review + deterministic unit/test execution.
// delivery: review + deterministic tests + integration.
// closed_loop: delivery gates + security for high-risk code and observation/
// deploy verification through their dedicated deterministic adapters.
func RequiredQualityGates(policy Policy, kind NodeKind, risk RiskLevel) []QualityRequirement {
	rank := autonomyRank(policy.Autonomy)
	out := make([]QualityRequirement, 0, 5)
	add := func(gate string, deterministic bool) {
		for _, existing := range out {
			if existing.GateType == gate {
				return
			}
		}
		out = append(out, QualityRequirement{GateType: gate, Deterministic: deterministic})
	}

	switch kind {
	case NodeImplementation, NodeMigration, NodeIntegration:
		add("review", false)
		if rank >= 2 {
			add("unit_test", true)
		}
		if rank >= 3 {
			add("integration_test", true)
		}
		if rank >= 4 && (risk == RiskHigh || risk == RiskCritical) {
			add("security", true)
		}
	case NodeQA:
		if rank >= 2 {
			add("acceptance", false)
		}
	case NodeSecurity:
		if rank >= 3 {
			add("security", true)
		}
	case NodeDeploy:
		if rank >= 3 {
			add("acceptance", true)
		}
	case NodeObserve:
		if rank >= 4 {
			add("performance", true)
		}
	}
	return out
}
