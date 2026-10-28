package projectorchestration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

// WebhookAdapterConfig configures backend-owned deployment/observation
// integrations. Credentials remain server-side and are never exposed to LLMs.
type WebhookAdapterConfig struct {
	RepositoryURL  string
	QualityURL     string
	DeploymentURL  string
	ObservationURL string
	BearerToken    string
	Timeout        time.Duration
}

type webhookAdapters struct {
	repositoryURL  string
	qualityURL     string
	deploymentURL  string
	observationURL string
	bearerToken    string
	client         *http.Client
}

// webhookDeploymentPayload exists only for the outbound call. It is never
// persisted as a deployment operation; the artifact URL is freshly presigned.
type webhookDeploymentPayload struct {
	Action         string                  `json:"action"`
	WorkspaceID    string                  `json:"workspace_id"`
	ProjectID      string                  `json:"project_id"`
	PlanID         string                  `json:"plan_id"`
	Environment    string                  `json:"environment"`
	ReleaseRef     string                  `json:"release_ref"`
	Manifest       ProviderReleaseManifest `json:"manifest"`
	ManifestDigest string                  `json:"manifest_digest"`
	ArtifactDigest string                  `json:"artifact_digest"`
	CommitSHA      string                  `json:"commit_sha"`
	ArtifactURL    string                  `json:"artifact_url"`
	Policy         Policy                  `json:"policy"`
}

func NewWebhookRepositoryAnalyzer(cfg WebhookAdapterConfig) RepositoryAnalyzer {
	if strings.TrimSpace(cfg.RepositoryURL) == "" {
		return nil
	}
	return newWebhookAdapters(cfg)
}

func NewWebhookQualityGateRunner(cfg WebhookAdapterConfig) QualityGateRunner {
	if strings.TrimSpace(cfg.QualityURL) == "" {
		return nil
	}
	return newWebhookAdapters(cfg)
}

func NewWebhookDeploymentAdapter(cfg WebhookAdapterConfig) DeploymentAdapter {
	if strings.TrimSpace(cfg.DeploymentURL) == "" {
		return nil
	}
	return newWebhookAdapters(cfg)
}

func NewWebhookObservationAdapter(cfg WebhookAdapterConfig) ObservationAdapter {
	if strings.TrimSpace(cfg.ObservationURL) == "" {
		return nil
	}
	return newWebhookAdapters(cfg)
}

func newWebhookAdapters(cfg WebhookAdapterConfig) *webhookAdapters {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &webhookAdapters{
		repositoryURL:  strings.TrimSpace(cfg.RepositoryURL),
		qualityURL:     strings.TrimSpace(cfg.QualityURL),
		deploymentURL:  strings.TrimSpace(cfg.DeploymentURL),
		observationURL: strings.TrimSpace(cfg.ObservationURL),
		bearerToken:    strings.TrimSpace(cfg.BearerToken),
		client:         &http.Client{Timeout: timeout},
	}
}

func (a *webhookAdapters) Snapshot(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) (RepositorySnapshot, error) {
	if a == nil || a.repositoryURL == "" {
		return RepositorySnapshot{}, ErrAdapterNotConfigured
	}
	payload := map[string]any{
		"action":       "snapshot",
		"workspace_id": util.UUIDToString(workspaceID),
		"project_id":   util.UUIDToString(projectID),
	}
	var result RepositorySnapshot
	if err := a.post(ctx, a.repositoryURL, payload, &result); err != nil {
		return RepositorySnapshot{}, err
	}
	return result, nil
}

func (a *webhookAdapters) Impact(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	request ChangeImpactRequest,
) (ChangeImpactEvidence, error) {
	if a == nil || a.repositoryURL == "" {
		return ChangeImpactEvidence{}, ErrAdapterNotConfigured
	}
	payload := map[string]any{
		"action":       "impact",
		"workspace_id": util.UUIDToString(workspaceID),
		"project_id":   util.UUIDToString(projectID),
		"request":      request,
	}
	var result ChangeImpactEvidence
	if err := a.post(ctx, a.repositoryURL, payload, &result); err != nil {
		return ChangeImpactEvidence{}, err
	}
	return result, nil
}

func (a *webhookAdapters) Run(ctx context.Context, request QualityGateRequest) (QualityGateResult, error) {
	if a == nil || a.qualityURL == "" {
		return QualityGateResult{}, ErrAdapterNotConfigured
	}
	if strings.TrimSpace(a.bearerToken) == "" {
		return QualityGateResult{}, errors.New("quality webhook requires a bearer secret for signed task evidence")
	}
	if !request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.PlanID.Valid ||
		!request.NodeID.Valid || !request.IssueID.Valid || !request.TaskID.Valid ||
		strings.TrimSpace(request.WorktreePath) == "" || strings.TrimSpace(request.RepoPath) == "" ||
		!gitCommitSHA.MatchString(request.CommitSHA) {
		return QualityGateResult{}, errors.New("quality webhook requires complete task, worktree, repository, and commit identity")
	}
	command, err := detectDaemonQualityCommand(request.WorktreePath, request.GateType, request.CommitSHA)
	if err != nil {
		return QualityGateResult{}, fmt.Errorf("detect allowed quality command: %w", err)
	}
	payload := map[string]any{
		"action":               "quality_gate",
		"workspace_id":         util.UUIDToString(request.WorkspaceID),
		"project_id":           util.UUIDToString(request.ProjectID),
		"plan_id":              util.UUIDToString(request.PlanID),
		"node_id":              util.UUIDToString(request.NodeID),
		"issue_id":             util.UUIDToString(request.IssueID),
		"task_id":              util.UUIDToString(request.TaskID),
		"gate_type":            request.GateType,
		"worktree_path":        request.WorktreePath,
		"repo_path":            request.RepoPath,
		"commit_sha":           request.CommitSHA,
		"expected_command":     command.display,
		"expected_image":       command.image,
		"expected_runner_type": "webhook_oci",
		"artifact":             request.Artifact,
	}
	var result QualityGateResult
	if err := a.postQuality(ctx, a.qualityURL, payload, &result); err != nil {
		return QualityGateResult{}, err
	}
	if err := validateWebhookQualityGateResult(a.bearerToken, request, command, &result); err != nil {
		result.Passed = false
		result.Error = boundedQualityString(err.Error(), 4096)
		return result, err
	}
	return result, nil
}

type qualityGateAttestationPayload struct {
	WorkspaceID              string `json:"workspace_id"`
	ProjectID                string `json:"project_id"`
	PlanID                   string `json:"plan_id"`
	NodeID                   string `json:"node_id"`
	IssueID                  string `json:"issue_id"`
	TaskID                   string `json:"task_id"`
	GateType                 string `json:"gate_type"`
	WorktreePath             string `json:"worktree_path"`
	RepoPath                 string `json:"repo_path"`
	CommitSHA                string `json:"commit_sha"`
	ExecutedCommand          string `json:"executed_command"`
	ContainerImage           string `json:"container_image"`
	ContainerImageID         string `json:"container_image_id"`
	PackageManagerVersion    string `json:"package_manager_version"`
	SourceSnapshot           string `json:"source_snapshot"`
	SourceSnapshotDigest     string `json:"source_snapshot_digest"`
	NetworkDisabled          bool   `json:"network_disabled"`
	HostEnvironmentInherited bool   `json:"host_environment_inherited"`
	TaskWorktreeMounted      bool   `json:"task_worktree_mounted"`
	MemoryLimit              string `json:"memory_limit"`
	CPULimit                 string `json:"cpu_limit"`
	PIDLimit                 string `json:"pid_limit"`
	TmpfsLimit               string `json:"tmpfs_limit"`
	ExitCode                 int    `json:"exit_code"`
	ResultDigest             string `json:"result_digest"`
}

func qualityGateAttestationPayloadFor(request QualityGateRequest, command daemonQualityCommand, result QualityGateResult) ([]byte, error) {
	if result.ExitCode == nil {
		return nil, errors.New("quality webhook result is missing an exit code")
	}
	if !request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.PlanID.Valid ||
		!request.NodeID.Valid || !request.IssueID.Valid || !request.TaskID.Valid {
		return nil, errors.New("quality webhook result has incomplete request identity")
	}
	imageID, ok := qualityEvidenceString(result.Evidence, "container_image_id")
	if !ok {
		return nil, errors.New("quality webhook result is missing a container image ID")
	}
	snapshotDigest, ok := qualityEvidenceString(result.Evidence, "source_snapshot_digest")
	if !ok {
		return nil, errors.New("quality webhook result is missing a source snapshot digest")
	}
	sourceSnapshot, ok := qualityEvidenceString(result.Evidence, "source_snapshot")
	if !ok {
		return nil, errors.New("quality webhook result is missing its source snapshot type")
	}
	networkDisabled, networkOK := result.Evidence["network_disabled"].(bool)
	hostEnvironmentInherited, hostEnvironmentOK := result.Evidence["host_environment_inherited"].(bool)
	taskWorktreeMounted, worktreeMountedOK := result.Evidence["task_worktree_mounted"].(bool)
	if !networkOK || !hostEnvironmentOK || !worktreeMountedOK {
		return nil, errors.New("quality webhook result is missing its OCI isolation claims")
	}
	memoryLimit, cpuLimit, pidLimit, tmpfsLimit := qualityResourceLimitValues(result.Evidence["resource_limits"])
	if memoryLimit == "" || cpuLimit == "" || pidLimit == "" || tmpfsLimit == "" {
		return nil, errors.New("quality webhook result is missing its OCI resource limits")
	}
	packageManagerVersion, _ := qualityEvidenceString(result.Evidence, "package_manager_version")
	if command.project == "node" && packageManagerVersion != qualityPNPMVersion {
		return nil, errors.New("quality webhook result is missing the preflighted pinned pnpm version")
	}
	payload := qualityGateAttestationPayload{
		WorkspaceID: util.UUIDToString(request.WorkspaceID),
		ProjectID:   util.UUIDToString(request.ProjectID), PlanID: util.UUIDToString(request.PlanID),
		NodeID: util.UUIDToString(request.NodeID), IssueID: util.UUIDToString(request.IssueID),
		TaskID: util.UUIDToString(request.TaskID), GateType: request.GateType,
		WorktreePath: request.WorktreePath, RepoPath: request.RepoPath,
		CommitSHA: request.CommitSHA, ExecutedCommand: command.display,
		ContainerImage: command.image, ContainerImageID: imageID, PackageManagerVersion: packageManagerVersion,
		SourceSnapshot: sourceSnapshot, SourceSnapshotDigest: snapshotDigest,
		NetworkDisabled: networkDisabled, HostEnvironmentInherited: hostEnvironmentInherited,
		TaskWorktreeMounted: taskWorktreeMounted,
		MemoryLimit:         memoryLimit, CPULimit: cpuLimit, PIDLimit: pidLimit, TmpfsLimit: tmpfsLimit,
		ExitCode: *result.ExitCode, ResultDigest: result.ArtifactDigest,
	}
	return json.Marshal(payload)
}

func signQualityGateAttestation(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func validateWebhookQualityGateResult(secret string, request QualityGateRequest, command daemonQualityCommand, result *QualityGateResult) error {
	if result == nil {
		return errors.New("quality webhook returned an empty result")
	}
	if result.RunnerType != "webhook_oci" || result.TaskID != util.UUIDToString(request.TaskID) ||
		result.GateType != request.GateType || result.WorktreePath != request.WorktreePath ||
		result.RepoPath != request.RepoPath || !strings.EqualFold(result.CommitSHA, request.CommitSHA) ||
		result.ExecutedCommand != command.display || result.ExitCode == nil {
		return errors.New("quality webhook result does not match the exact task, worktree, commit, command, and exit-code contract")
	}
	if result.ExitCode != nil && (*result.ExitCode < 0 || *result.ExitCode > 255) {
		return errors.New("quality webhook returned an invalid process exit code")
	}
	if len(result.Stdout) > maxDaemonQualityOutput || len(result.Stderr) > maxDaemonQualityOutput {
		return errors.New("quality webhook output exceeds the capture limit")
	}
	if result.DurationMs < 0 || result.DurationMs > defaultDaemonQualityTimeout.Milliseconds() ||
		result.TestCount < 0 || result.FailedCount < 0 || result.FailedCount > result.TestCount ||
		(result.Passed && result.Error != "") {
		return errors.New("quality webhook returned malformed bounded result fields")
	}
	if result.ArtifactDigest == "" || result.ArtifactDigest != qualityResultDigest(*result) {
		return errors.New("quality webhook result digest is missing or malformed")
	}
	if result.Evidence == nil {
		return errors.New("quality webhook result has no attributable evidence")
	}
	if err := validateQualityEvidenceKeys(result.Evidence); err != nil {
		return err
	}
	if !qualityEvidenceMatches(result, request) {
		return errors.New("quality webhook evidence does not match the exact task, worktree, repository, commit, command, or exit code")
	}
	image, imageOK := qualityEvidenceString(result.Evidence, "container_image")
	imageID, imageIDOK := qualityEvidenceString(result.Evidence, "container_image_id")
	snapshotDigest, snapshotOK := qualityEvidenceString(result.Evidence, "source_snapshot_digest")
	if !imageOK || image != command.image || !imageIDOK || !containerImageID.MatchString(imageID) ||
		!snapshotOK || !digestSHA256.MatchString(snapshotDigest) ||
		result.Evidence["source_snapshot"] != "git_archive_of_recorded_commit" {
		return errors.New("quality webhook did not identify its pinned image and committed source snapshot")
	}
	if command.project == "node" {
		packageManagerVersion, ok := qualityEvidenceString(result.Evidence, "package_manager_version")
		if !ok || packageManagerVersion != qualityPNPMVersion {
			return errors.New("quality webhook did not prove the pinned pnpm preflight")
		}
	}
	if result.Evidence["network_disabled"] != true || result.Evidence["host_environment_inherited"] != false ||
		result.Evidence["task_worktree_mounted"] != false || !qualityResourceLimitsMatch(result.Evidence["resource_limits"]) {
		return errors.New("quality webhook did not attest the required OCI isolation boundary")
	}
	if result.Passed && *result.ExitCode != 0 {
		return errors.New("quality webhook cannot pass when the recorded command exit code is nonzero")
	}
	if evidenceDigest, ok := qualityEvidenceString(result.Evidence, "artifact_digest"); !ok || evidenceDigest != result.ArtifactDigest {
		return errors.New("quality webhook evidence digest does not match its result")
	}
	if evidenceAttestation, ok := qualityEvidenceString(result.Evidence, "attestation"); !ok || evidenceAttestation != result.Attestation {
		return errors.New("quality webhook evidence is missing its matching attestation")
	}
	attestation, err := hex.DecodeString(result.Attestation)
	if err != nil || len(attestation) != sha256.Size {
		return errors.New("quality webhook attestation is missing or malformed")
	}
	payload, err := qualityGateAttestationPayloadFor(request, command, *result)
	if err != nil {
		return err
	}
	expected, err := hex.DecodeString(signQualityGateAttestation(secret, payload))
	if err != nil || !hmac.Equal(attestation, expected) {
		return errors.New("quality webhook attestation is invalid")
	}
	result.Evidence["attestation"] = result.Attestation
	result.Evidence["remote_attestation_verified"] = true
	return nil
}

func qualityEvidenceMatches(result *QualityGateResult, request QualityGateRequest) bool {
	taskID, taskOK := qualityEvidenceString(result.Evidence, "task_id")
	gateType, gateOK := qualityEvidenceString(result.Evidence, "gate_type")
	worktree, worktreeOK := qualityEvidenceString(result.Evidence, "worktree_path")
	repo, repoOK := qualityEvidenceString(result.Evidence, "repo_path")
	commit, commitOK := qualityEvidenceString(result.Evidence, "commit_sha")
	command, commandOK := qualityEvidenceString(result.Evidence, "executed_command")
	runner, runnerOK := qualityEvidenceString(result.Evidence, "runner_type")
	evidenceExit, exitOK := qualityEvidenceInt(result.Evidence, "exit_code")
	artifactDigest, digestOK := qualityEvidenceString(result.Evidence, "artifact_digest")
	stdout, stdoutOK := result.Evidence["stdout"].(string)
	stderr, stderrOK := result.Evidence["stderr"].(string)
	duration, durationOK := qualityEvidenceInt(result.Evidence, "duration_ms")
	testCount, testCountOK := qualityEvidenceInt(result.Evidence, "test_count")
	failedCount, failedCountOK := qualityEvidenceInt(result.Evidence, "failed_count")
	truncated, truncatedOK := result.Evidence["output_truncated"].(bool)
	return taskOK && taskID == util.UUIDToString(request.TaskID) && gateOK && gateType == request.GateType &&
		worktreeOK && worktree == request.WorktreePath && repoOK && repo == request.RepoPath &&
		commitOK && strings.EqualFold(commit, request.CommitSHA) && commandOK && command == result.ExecutedCommand &&
		runnerOK && runner == result.RunnerType && exitOK && result.ExitCode != nil && evidenceExit == *result.ExitCode &&
		digestOK && artifactDigest == result.ArtifactDigest && stdoutOK && stdout == result.Stdout &&
		stderrOK && stderr == result.Stderr && durationOK && int64(duration) == result.DurationMs &&
		testCountOK && testCount == result.TestCount && failedCountOK && failedCount == result.FailedCount &&
		truncatedOK && truncated == result.OutputTruncated
}

func validateQualityEvidenceKeys(evidence map[string]any) error {
	allowed := map[string]struct{}{
		"task_id": {}, "gate_type": {}, "worktree_path": {}, "repo_path": {}, "commit_sha": {},
		"executed_command": {}, "runner_type": {}, "exit_code": {}, "container_image": {},
		"container_image_id": {}, "source_snapshot_digest": {}, "network_disabled": {},
		"host_environment_inherited": {}, "task_worktree_mounted": {}, "stdout": {}, "stderr": {},
		"duration_ms": {}, "output_truncated": {}, "test_count": {}, "failed_count": {},
		"artifact_digest": {}, "attestation": {}, "resource_limits": {}, "package_manager_version": {},
		"source_snapshot": {}, "error": {},
	}
	for key := range evidence {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("quality webhook returned unknown evidence field %q", key)
		}
	}
	return nil
}

func qualityEvidenceString(evidence map[string]any, key string) (string, bool) {
	value, ok := evidence[key].(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func qualityEvidenceInt(evidence map[string]any, key string) (int, bool) {
	switch value := evidence[key].(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), int64(int(value)) == value
	case float64:
		return int(value), float64(int(value)) == value
	case json.Number:
		parsed, err := value.Int64()
		return int(parsed), err == nil && int64(int(parsed)) == parsed
	case *int:
		if value != nil {
			return *value, true
		}
	}
	return 0, false
}

func (a *webhookAdapters) postQuality(ctx context.Context, endpoint string, payload any, out *QualityGateResult) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("quality webhook response exceeds its size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("quality webhook returned HTTP %d", resp.StatusCode)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode quality webhook response shape: %w", err)
	}
	allowed := map[string]struct{}{
		"passed": {}, "evidence": {}, "error": {}, "runner_type": {}, "task_id": {},
		"gate_type": {}, "executed_command": {}, "exit_code": {}, "worktree_path": {},
		"repo_path": {}, "commit_sha": {}, "duration_ms": {}, "stdout": {}, "stderr": {},
		"output_truncated": {}, "test_count": {}, "failed_count": {}, "artifact_digest": {}, "attestation": {},
	}
	required := []string{
		"passed", "evidence", "error", "runner_type", "task_id", "gate_type", "executed_command",
		"exit_code", "worktree_path", "repo_path", "commit_sha", "duration_ms", "stdout", "stderr",
		"output_truncated", "test_count", "failed_count", "artifact_digest", "attestation",
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("quality webhook response contains unknown field %q", key)
		}
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("quality webhook response is missing field %q", key)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode quality webhook response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("quality webhook response contains trailing data")
	}
	return nil
}

func (a *webhookAdapters) Deploy(ctx context.Context, request DeploymentRequest) (DeploymentResult, error) {
	if a == nil || a.deploymentURL == "" {
		return DeploymentResult{}, ErrAdapterNotConfigured
	}
	manifestDigest, err := ParseReleaseReference(request.ReleaseRef)
	if err != nil {
		return DeploymentResult{}, err
	}
	if manifestDigest != request.Manifest.ManifestDigest || request.ArtifactURL == "" {
		return DeploymentResult{}, errors.New("deployment requires the exact manifest identity and a fresh artifact retrieval URL")
	}
	payload := webhookDeploymentPayload{
		Action: "deploy", WorkspaceID: util.UUIDToString(request.WorkspaceID),
		ProjectID: util.UUIDToString(request.ProjectID), PlanID: util.UUIDToString(request.PlanID),
		Environment: request.Environment, ReleaseRef: request.ReleaseRef,
		Manifest: request.Manifest, ManifestDigest: request.Manifest.ManifestDigest,
		ArtifactDigest: request.ArtifactDigest, CommitSHA: request.CommitSHA,
		ArtifactURL: request.ArtifactURL, Policy: request.Policy,
	}
	var result DeploymentResult
	if err := a.post(ctx, a.deploymentURL, payload, &result); err != nil {
		return DeploymentResult{}, err
	}
	if result.Provider == "" {
		result.Provider = "webhook"
	}
	switch result.Status {
	case "succeeded", "failed":
	default:
		return DeploymentResult{}, fmt.Errorf("deployment webhook returned unsupported terminal status %q", result.Status)
	}
	if result.Status == "succeeded" {
		if err := ValidateProviderAcknowledgement(result.Evidence, request.Manifest, request.CommitSHA, request.ArtifactDigest); err != nil {
			return DeploymentResult{}, err
		}
	}
	return result, nil
}

func (a *webhookAdapters) Rollback(ctx context.Context, request RollbackRequest) (DeploymentResult, error) {
	if a == nil || a.deploymentURL == "" {
		return DeploymentResult{}, ErrAdapterNotConfigured
	}
	if !request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.DeploymentID.Valid ||
		(request.TargetEnvironment != "staging" && request.TargetEnvironment != "production") {
		return DeploymentResult{}, errors.New("rollback requires a deployment and target release identity")
	}
	if _, err := util.ParseUUID(request.TargetManifestID); err != nil {
		return DeploymentResult{}, errors.New("rollback target manifest ID is invalid")
	}
	if _, err := ParseReleaseReference("sha256:" + request.TargetManifestDigest); err != nil {
		return DeploymentResult{}, errors.New("rollback target manifest digest is invalid")
	}
	payload := map[string]any{
		"action":                 "rollback",
		"workspace_id":           util.UUIDToString(request.WorkspaceID),
		"project_id":             util.UUIDToString(request.ProjectID),
		"deployment_id":          util.UUIDToString(request.DeploymentID),
		"target_manifest_id":     request.TargetManifestID,
		"target_manifest_digest": request.TargetManifestDigest,
		"target_environment":     request.TargetEnvironment,
		"reason":                 request.Reason,
	}
	var result DeploymentResult
	if err := a.post(ctx, a.deploymentURL, payload, &result); err != nil {
		return DeploymentResult{}, err
	}
	if result.Provider == "" {
		result.Provider = "webhook"
	}
	switch result.Status {
	case "succeeded", "rolled_back", "failed":
	default:
		return DeploymentResult{}, fmt.Errorf("deployment webhook returned unsupported rollback status %q", result.Status)
	}
	if result.Status == "succeeded" || result.Status == "rolled_back" {
		if result.Evidence == nil || result.Evidence["target_manifest_id"] != request.TargetManifestID ||
			result.Evidence["target_manifest_digest"] != request.TargetManifestDigest {
			return DeploymentResult{}, errors.New("deployment rollback acknowledgement does not match its target manifest")
		}
	}
	return result, nil
}

func (a *webhookAdapters) Observe(ctx context.Context, request ObservationRequest) (ObservationResult, error) {
	if a == nil || a.observationURL == "" {
		return ObservationResult{}, ErrAdapterNotConfigured
	}
	payload := map[string]any{
		"action":         "observe",
		"workspace_id":   util.UUIDToString(request.WorkspaceID),
		"project_id":     util.UUIDToString(request.ProjectID),
		"deployment_id":  util.UUIDToString(request.DeploymentID),
		"window_seconds": request.WindowSeconds,
	}
	var result ObservationResult
	if err := a.post(ctx, a.observationURL, payload, &result); err != nil {
		return ObservationResult{}, err
	}
	return result, nil
}

func (a *webhookAdapters) SmokeTest(ctx context.Context, request StagingSmokeRequest) (StagingSmokeResult, error) {
	if a == nil || a.deploymentURL == "" {
		return StagingSmokeResult{}, ErrAdapterNotConfigured
	}
	if !request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.DeploymentID.Valid || request.Environment != "staging" {
		return StagingSmokeResult{}, errors.New("staging smoke test requires a staging deployment identity")
	}
	if _, err := util.ParseUUID(request.ManifestID); err != nil {
		return StagingSmokeResult{}, errors.New("staging smoke test manifest ID is invalid")
	}
	if _, err := ParseReleaseReference("sha256:" + request.ManifestDigest); err != nil {
		return StagingSmokeResult{}, errors.New("staging smoke test manifest digest is invalid")
	}
	if _, err := ParseReleaseReference("sha256:" + request.ArtifactDigest); err != nil {
		return StagingSmokeResult{}, errors.New("staging smoke test artifact digest is invalid")
	}
	if !gitCommitSHA.MatchString(request.CommitSHA) {
		return StagingSmokeResult{}, errors.New("staging smoke test commit identity is invalid")
	}
	payload := map[string]any{
		"action": "staging_smoke_test", "workspace_id": util.UUIDToString(request.WorkspaceID),
		"project_id": util.UUIDToString(request.ProjectID), "deployment_id": util.UUIDToString(request.DeploymentID),
		"environment": request.Environment, "external_ref": request.ExternalRef,
		"manifest_id": request.ManifestID, "manifest_digest": request.ManifestDigest,
		"artifact_digest": request.ArtifactDigest, "commit_sha": request.CommitSHA,
	}
	var result StagingSmokeResult
	if err := a.post(ctx, a.deploymentURL, payload, &result); err != nil {
		return StagingSmokeResult{}, err
	}
	if result.ManifestDigest != request.ManifestDigest {
		return StagingSmokeResult{}, errors.New("staging smoke test acknowledged a different manifest digest")
	}
	return result, nil
}

func (a *webhookAdapters) post(ctx context.Context, endpoint string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if a.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("autonomous webhook returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode autonomous webhook response: %w", err)
	}
	return nil
}
