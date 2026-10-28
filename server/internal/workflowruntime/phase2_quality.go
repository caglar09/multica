package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type nodeQualityContext struct {
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	PlanID      pgtype.UUID
	NodeID      pgtype.UUID
	IssueID     pgtype.UUID
	NodeKey     string
	Kind        projectorchestration.NodeKind
	Risk        projectorchestration.RiskLevel
	Policy      projectorchestration.Policy
}

func (r *Runtime) loadNodeQualityByKey(ctx context.Context, workspaceID, projectID pgtype.UUID, nodeKey string) (nodeQualityContext, error) {
	var out nodeQualityContext
	var kind, risk string
	var policyJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT n.plan_id, n.id, n.materialized_issue_id, n.node_key, n.kind, n.risk_level, p.policy
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1 AND n.project_id = $2 AND n.node_key = $3
		  AND p.status IN ('active','blocked')
		ORDER BY p.revision DESC
		LIMIT 1
	`, workspaceID, projectID, nodeKey).Scan(
		&out.PlanID, &out.NodeID, &out.IssueID, &out.NodeKey, &kind, &risk, &policyJSON,
	)
	if err != nil {
		return nodeQualityContext{}, err
	}
	out.WorkspaceID, out.ProjectID = workspaceID, projectID
	out.Kind = projectorchestration.NodeKind(kind)
	out.Risk = projectorchestration.RiskLevel(risk)
	if err := json.Unmarshal(policyJSON, &out.Policy); err != nil {
		return nodeQualityContext{}, fmt.Errorf("decode project quality policy: %w", err)
	}
	return out, nil
}

func (r *Runtime) loadNodeQualityByIssue(ctx context.Context, workspaceID, issueID pgtype.UUID) (nodeQualityContext, error) {
	var out nodeQualityContext
	var kind, risk string
	var policyJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT n.project_id, n.plan_id, n.id, n.node_key, n.kind, n.risk_level, p.policy
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1 AND n.materialized_issue_id = $2
		  AND p.status IN ('active','blocked')
		ORDER BY p.revision DESC
		LIMIT 1
	`, workspaceID, issueID).Scan(
		&out.ProjectID, &out.PlanID, &out.NodeID, &out.NodeKey, &kind, &risk, &policyJSON,
	)
	if err != nil {
		return nodeQualityContext{}, err
	}
	out.WorkspaceID, out.IssueID = workspaceID, issueID
	out.Kind = projectorchestration.NodeKind(kind)
	out.Risk = projectorchestration.RiskLevel(risk)
	if err := json.Unmarshal(policyJSON, &out.Policy); err != nil {
		return nodeQualityContext{}, fmt.Errorf("decode project quality policy: %w", err)
	}
	return out, nil
}

func (r *Runtime) ensureNodeQualityPolicy(ctx context.Context, workspaceID, projectID pgtype.UUID, node projectorchestration.ReadyNode) error {
	q, err := r.loadNodeQualityByKey(ctx, workspaceID, projectID, node.Key)
	if err != nil {
		return err
	}
	for _, req := range projectorchestration.RequiredQualityGates(q.Policy, q.Kind, q.Risk) {
		evidence, _ := json.Marshal(map[string]any{
			"policy_requirement": true,
			"deterministic":      req.Deterministic,
			"autonomy":           q.Policy.Autonomy,
		})
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_quality_gate_run (
				workspace_id, project_id, plan_id, node_id, gate_type,
				status, required, evidence, attempt
			)
			SELECT $1,$2,$3,$4,$5,'pending',TRUE,$6,0
			WHERE NOT EXISTS (
				SELECT 1 FROM autonomous_project_quality_gate_run
				WHERE workspace_id=$1 AND project_id=$2 AND node_id=$4
				  AND gate_type=$5 AND required=TRUE
				  AND evidence ->> 'policy_requirement' = 'true'
			)
		`, q.WorkspaceID, q.ProjectID, q.PlanID, q.NodeID, req.GateType, evidence); err != nil {
			return fmt.Errorf("seed quality gate %s: %w", req.GateType, err)
		}
		if _, err := r.pool.Exec(ctx, `
			UPDATE autonomous_project_quality_gate_run
			SET evidence=jsonb_set(evidence, '{deterministic}', to_jsonb($5::boolean), TRUE)
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3 AND gate_type=$4
			  AND required=TRUE AND evidence ->> 'policy_requirement' = 'true'
		`, q.WorkspaceID, q.ProjectID, q.NodeID, req.GateType, req.Deterministic); err != nil {
			return fmt.Errorf("refresh quality gate %s policy: %w", req.GateType, err)
		}
	}
	return nil
}

func (r *Runtime) requiredGate(ctx context.Context, q nodeQualityContext, gateType string) (bool, bool, error) {
	var deterministic bool
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE((evidence ->> 'deterministic')::boolean, FALSE)
		FROM autonomous_project_quality_gate_run
		WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
		  AND gate_type=$4 AND required=TRUE
		  AND evidence ->> 'policy_requirement' = 'true'
		ORDER BY created_at DESC
		LIMIT 1
	`, q.WorkspaceID, q.ProjectID, q.NodeID, gateType).Scan(&deterministic)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if q.Kind == projectorchestration.NodeQA && gateType == "acceptance" {
		return true, false, err
	}
	return true, deterministic, err
}

func (r *Runtime) recordPolicyGate(ctx context.Context, q nodeQualityContext, gateType, status, gateErr string, evidence map[string]any) error {
	if evidence == nil {
		evidence = map[string]any{}
	}
	evidence["policy_requirement"] = true
	raw, _ := json.Marshal(evidence)
	tag, err := r.pool.Exec(ctx, `
		UPDATE autonomous_project_quality_gate_run
		SET status=$5,
		    evidence=evidence || $6::jsonb,
		    executed_command=NULLIF($6::jsonb #>> '{runner_evidence,executed_command}',''),
		    exit_code=CASE WHEN $6::jsonb #>> '{runner_evidence,exit_code}' ~ '^-?[0-9]+$' THEN ($6::jsonb #>> '{runner_evidence,exit_code}')::integer ELSE NULL END,
		    worktree_path=NULLIF($6::jsonb #>> '{runner_evidence,worktree_path}',''),
		    commit_sha=NULLIF($6::jsonb #>> '{runner_evidence,commit_sha}',''),
		    runner_type=NULLIF($6::jsonb #>> '{runner_evidence,runner_type}',''),
		    attempt=attempt+1,
		    last_error=NULLIF($7,''),
		    started_at=COALESCE(started_at,now()),
		    completed_at=CASE WHEN $5 IN ('passed','failed','blocked','unavailable','inconclusive','waived','skipped') THEN now() ELSE completed_at END,
		    updated_at=now()
		WHERE id = (
			SELECT id FROM autonomous_project_quality_gate_run
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
			  AND gate_type=$4 AND required=TRUE
			  AND evidence ->> 'policy_requirement' = 'true'
			ORDER BY created_at DESC LIMIT 1
		)
	`, q.WorkspaceID, q.ProjectID, q.NodeID, gateType, status, raw, gateErr)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("required quality gate %s was not seeded", gateType)
	}
	return nil
}

func (r *Runtime) passSemanticGate(ctx context.Context, q nodeQualityContext, gateType string, evidence map[string]any) error {
	required, deterministic, err := r.requiredGate(ctx, q, gateType)
	if err != nil || !required {
		return err
	}
	if deterministic {
		return fmt.Errorf("%w: gate=%s autonomy=%s", projectorchestration.ErrQualityGateUnavailable, gateType, q.Policy.Autonomy)
	}
	if evidence == nil {
		evidence = map[string]any{}
	}
	evidence["mode"] = "structured_semantic"
	return r.recordPolicyGate(ctx, q, gateType, "passed", "", evidence)
}

func (r *Runtime) runDeterministicGate(ctx context.Context, q nodeQualityContext, taskID, gateType string, artifact map[string]any) error {
	required, deterministic, err := r.requiredGate(ctx, q, gateType)
	if err != nil || !required {
		return err
	}
	if !deterministic {
		return r.passSemanticGate(ctx, q, gateType, artifact)
	}
	taskUUID, err := util.ParseUUID(taskID)
	if err != nil {
		gateErr := fmt.Errorf("invalid quality gate task identity: %w", err)
		return r.recordUnavailableTaskQualityGate(ctx, q, gateType, taskID, gateErr)
	}
	var worktreePath, repoPath, commitSHA string
	if err := r.pool.QueryRow(ctx, `
		SELECT worktree_path, repo_path, commit_sha
		FROM autonomous_project_change_set
		WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3
		  AND node_id=$4 AND issue_id=$5 AND source_task_id=$6
	`, q.WorkspaceID, q.ProjectID, q.PlanID, q.NodeID, q.IssueID, taskUUID).Scan(&worktreePath, &repoPath, &commitSHA); err != nil {
		gateErr := fmt.Errorf("load recorded task commit for quality gate: %w", err)
		return r.recordUnavailableTaskQualityGate(ctx, q, gateType, taskID, gateErr)
	}
	request := projectorchestration.QualityGateRequest{
		WorkspaceID: q.WorkspaceID, ProjectID: q.ProjectID, PlanID: q.PlanID,
		NodeID: q.NodeID, IssueID: q.IssueID, TaskID: taskUUID, GateType: gateType,
		WorktreePath: worktreePath, RepoPath: repoPath, CommitSHA: commitSHA,
		Artifact: withoutAgentTestClaims(artifact),
	}
	result, status, runErr := evaluateQualityGate(ctx, r.qualityGateRunner, request)
	if result.CommitSHA == commitSHA && (result.RunnerType == "daemon_oci" || result.RunnerType == "webhook_oci") {
		if commitURL := qualityCommitURL(ctx, repoPath, commitSHA); commitURL != "" {
			if result.Evidence == nil {
				result.Evidence = make(map[string]any)
			}
			result.Evidence["commit_url"] = commitURL
		}
	}
	mode := "deterministic_runner"
	if r.qualityGateRunner == nil {
		mode = "missing_runner"
	}
	evidence := map[string]any{"mode": mode, "runner_evidence": result.Evidence, "source_task_id": taskID}
	if artifactContent, ok := browserQAArtifactContent(gateType, status, taskID, commitSHA, result.RunnerType, result.Evidence); ok {
		if persistErr := r.persistBrowserQAArtifact(ctx, q, taskID, artifactContent); persistErr != nil {
			evidence["browser_qa_artifact_error"] = persistErr.Error()
			if runErr == nil {
				runErr = fmt.Errorf("persist browser QA evidence artifact: %w", persistErr)
				status = "unavailable"
			}
		}
	}
	if runErr != nil {
		message := runErr.Error()
		if r.qualityGateRunner == nil {
			message = fmt.Sprintf("%v: %s", projectorchestration.ErrQualityGateUnavailable, gateType)
			runErr = fmt.Errorf("%w: gate=%s autonomy=%s", projectorchestration.ErrQualityGateUnavailable, gateType, q.Policy.Autonomy)
		}
		if recordErr := r.recordPolicyGate(ctx, q, gateType, status, message, evidence); recordErr != nil {
			return errors.Join(runErr, fmt.Errorf("record deterministic quality gate %s: %w", gateType, recordErr))
		}
		return runErr
	}
	return r.recordPolicyGate(ctx, q, gateType, status, "", evidence)
}

func browserQAArtifactContent(
	gateType, status, taskID, commitSHA, runnerType string,
	evidence map[string]any,
) (map[string]any, bool) {
	if !strings.EqualFold(strings.TrimSpace(gateType), "acceptance") || evidence == nil {
		return nil, false
	}
	browserEvidence, ok := evidence["browser_qa"]
	if !ok || browserEvidence == nil {
		return nil, false
	}
	return map[string]any{
		"gate_type":   "acceptance",
		"status":      status,
		"task_id":     taskID,
		"commit_sha":  commitSHA,
		"runner_type": runnerType,
		"browser_qa":  browserEvidence,
	}, true
}

func (r *Runtime) persistBrowserQAArtifact(
	ctx context.Context,
	q nodeQualityContext,
	taskID string,
	content map[string]any,
) error {
	if r == nil || r.pool == nil {
		return errors.New("browser QA artifact storage is unavailable")
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("encode browser QA artifact: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id, project_id, plan_id, node_id, artifact_type,
			name, schema_version, content, immutable
		)
		VALUES ($1,$2,$3,$4,'qa_report',$5,1,$6::jsonb,true)
	`, q.WorkspaceID, q.ProjectID, q.PlanID, q.NodeID,
		fmt.Sprintf("acceptance browser QA %s", taskID), encoded)
	if err != nil {
		return fmt.Errorf("insert autonomous project browser QA artifact: %w", err)
	}
	return nil
}

func qualityCommitURL(ctx context.Context, repoPath, commitSHA string) string {
	repoPath = strings.TrimSpace(repoPath)
	commitSHA = strings.TrimSpace(commitSHA)
	if repoPath == "" || commitSHA == "" {
		return ""
	}
	output, err := exec.CommandContext(ctx, "git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return qualityCommitURLFromRemote(string(output), commitSHA)
}

func qualityCommitURLFromRemote(remote, commitSHA string) string {
	remote = strings.TrimSpace(remote)
	commitSHA = strings.TrimSpace(commitSHA)
	if remote == "" || commitSHA == "" {
		return ""
	}
	if strings.HasPrefix(remote, "git@") {
		at := strings.IndexByte(remote, '@')
		colon := strings.IndexByte(remote, ':')
		if at < 0 || colon <= at+1 {
			return ""
		}
		remote = "https://" + remote[at+1:colon] + "/" + strings.TrimLeft(remote[colon+1:], "/")
	}
	parsed, err := url.Parse(remote)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, ".git") + "/commit/" + url.PathEscape(commitSHA)
	return parsed.String()
}

func (r *Runtime) recordUnavailableTaskQualityGate(ctx context.Context, q nodeQualityContext, gateType, taskID string, gateErr error) error {
	evidence := map[string]any{"mode": "missing_task_commit", "source_task_id": taskID}
	if recordErr := r.recordPolicyGate(ctx, q, gateType, "unavailable", gateErr.Error(), evidence); recordErr != nil {
		return errors.Join(gateErr, fmt.Errorf("record unavailable task quality gate %s: %w", gateType, recordErr))
	}
	return gateErr
}

func evaluateQualityGate(
	ctx context.Context,
	runner projectorchestration.QualityGateRunner,
	request projectorchestration.QualityGateRequest,
) (projectorchestration.QualityGateResult, string, error) {
	if runner == nil {
		return projectorchestration.QualityGateResult{}, "unavailable", projectorchestration.ErrQualityGateUnavailable
	}
	result, err := runner.Run(ctx, request)
	if err != nil {
		return result, "unavailable", err
	}
	if result.Passed {
		if err := projectorchestration.ValidateQualityGatePass(request, result); err != nil {
			result.Passed = false
			result.Error = err.Error()
			return result, "unavailable", err
		}
		return result, "passed", nil
	}
	message := strings.TrimSpace(result.Error)
	if message == "" {
		message = "deterministic quality gate failed"
	}
	return result, "failed", errors.New(message)
}

func withoutAgentTestClaims(artifact map[string]any) map[string]any {
	if artifact == nil {
		return nil
	}
	cleaned := make(map[string]any, len(artifact))
	for key, value := range artifact {
		if key != "tests" {
			cleaned[key] = value
		}
	}
	result, ok := artifact["result"].(map[string]any)
	if !ok {
		return cleaned
	}
	cleanedResult := make(map[string]any, len(result))
	for key, value := range result {
		if key != "tests" {
			cleanedResult[key] = value
		}
	}
	cleaned["result"] = cleanedResult
	return cleaned
}

func (r *Runtime) blockForQualityFailure(ctx context.Context, q nodeQualityContext, gateType string, cause error) error {
	reason := fmt.Sprintf("required quality gate %s failed: %v", gateType, cause)
	if err := r.projectStore.SetNodeBlocked(ctx, q.WorkspaceID, q.ProjectID, q.NodeKey, "quality_policy", reason); err != nil {
		return err
	}
	if q.IssueID.Valid {
		_, _ = r.taskSvc.SetIssueStatusForWorkflow(ctx, q.IssueID, "blocked")
	}
	contextJSON, _ := json.Marshal(map[string]any{
		"node_key": q.NodeKey, "gate_type": gateType, "error": cause.Error(), "autonomy": q.Policy.Autonomy,
	})
	_, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, plan_id, node_id, category, severity, summary, context
		)
		SELECT $1,$2,$3,$4,'quality_policy','high',$5,$6
		WHERE NOT EXISTS (
			SELECT 1 FROM autonomous_project_escalation
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$4
			  AND category='quality_policy' AND status IN ('open','acknowledged')
		)
	`, q.WorkspaceID, q.ProjectID, q.PlanID, q.NodeID,
		"Required deterministic quality gate failed: "+q.NodeKey, contextJSON)
	return err
}

func (r *Runtime) runTaskQualityIfRequired(ctx context.Context, task db.AgentTaskQueue, issue db.Issue, artifactType string, artifact map[string]any) (bool, error) {
	q, err := r.loadNodeQualityByIssue(ctx, issue.WorkspaceID, issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return r.applyTaskQualityEvidence(ctx, q, util.UUIDToString(task.ID), artifactType, artifact)
}

func (r *Runtime) applyTaskQualityEvidence(ctx context.Context, q nodeQualityContext, taskID, artifactType string, artifact map[string]any) (bool, error) {
	for _, requirement := range projectorchestration.RequiredQualityGates(q.Policy, q.Kind, q.Risk) {
		if requirement.Deterministic && artifactType == "review" {
			continue
		}
		qaArtifactID := ""
		if !requirement.Deterministic {
			if requirement.GateType == "review" && artifactType == "review" && reviewApproved(artifact) {
				// The structured review handoff is evaluated below.
			} else if q.Kind == projectorchestration.NodeQA && requirement.GateType == "acceptance" {
				var valid bool
				var artifactErr error
				qaArtifactID, valid, artifactErr = r.hasDurableQAHandoffArtifact(ctx, q, taskID, artifactType, artifact)
				if artifactErr != nil {
					return false, fmt.Errorf("validate durable QA handoff artifact: %w", artifactErr)
				}
				if !valid {
					gateErr := errors.New("acceptance requires a valid durable qa_handoff artifact")
					if err := r.blockForQualityFailure(ctx, q, requirement.GateType, gateErr); err != nil {
						return true, err
					}
					return true, nil
				}
			} else {
				continue
			}
		}
		var status string
		var sourceTaskID string
		var qaHandoffArtifactID string
		err := r.pool.QueryRow(ctx, `
			SELECT status, COALESCE(evidence ->> 'source_task_id', ''), COALESCE(evidence ->> 'qa_handoff_artifact_id', '')
			FROM autonomous_project_quality_gate_run
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
			  AND gate_type=$4 AND required=TRUE
			  AND evidence ->> 'policy_requirement' = 'true'
			ORDER BY created_at DESC
			LIMIT 1
		`, q.WorkspaceID, q.ProjectID, q.NodeID, requirement.GateType).Scan(&status, &sourceTaskID, &qaHandoffArtifactID)
		if errors.Is(err, pgx.ErrNoRows) {
			if requirement.Deterministic {
				gateErr := fmt.Errorf("required quality gate %s was not seeded", requirement.GateType)
				if err := r.blockForQualityFailure(ctx, q, requirement.GateType, gateErr); err != nil {
					return true, err
				}
				return true, nil
			}
			continue
		}
		if err != nil {
			return false, err
		}
		if status == "passed" && sourceTaskID == taskID &&
			(q.Kind != projectorchestration.NodeQA || requirement.GateType != "acceptance" || qaHandoffArtifactID == qaArtifactID) {
			continue
		}

		var gateErr error
		if requirement.Deterministic {
			gateErr = r.runDeterministicGate(ctx, q, taskID, requirement.GateType, artifact)
		} else {
			evidence := map[string]any{
				"task_id": taskID, "artifact_type": artifactType,
			}
			if qaArtifactID != "" {
				evidence["qa_handoff_artifact_id"] = qaArtifactID
			}
			gateErr = r.passSemanticGate(ctx, q, requirement.GateType, evidence)
		}
		if gateErr == nil {
			continue
		}
		if err := r.blockForQualityFailure(ctx, q, requirement.GateType, gateErr); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, nil
}

type taskQualityEvidence struct {
	Status       string
	SourceTaskID string
}

func hasCurrentDeterministicQualityEvidence(
	requirements []projectorchestration.QualityRequirement,
	evidence map[string]taskQualityEvidence,
	taskID string,
) bool {
	required := false
	for _, requirement := range requirements {
		if !requirement.Deterministic {
			continue
		}
		required = true
		gate, ok := evidence[requirement.GateType]
		if !ok || gate.Status != "passed" || gate.SourceTaskID != taskID {
			return false
		}
	}
	return required
}

func (r *Runtime) hasCurrentDeterministicQualityEvidence(ctx context.Context, q nodeQualityContext, taskID string) (bool, error) {
	requirements := projectorchestration.RequiredQualityGates(q.Policy, q.Kind, q.Risk)
	evidence := make(map[string]taskQualityEvidence, len(requirements))
	for _, requirement := range requirements {
		if !requirement.Deterministic {
			continue
		}
		var gate taskQualityEvidence
		err := r.pool.QueryRow(ctx, `
			SELECT status, COALESCE(evidence ->> 'source_task_id', '')
			FROM autonomous_project_quality_gate_run
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
			  AND gate_type=$4 AND required=TRUE
			  AND evidence ->> 'policy_requirement' = 'true'
			ORDER BY created_at DESC LIMIT 1
		`, q.WorkspaceID, q.ProjectID, q.NodeID, requirement.GateType).Scan(&gate.Status, &gate.SourceTaskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read deterministic quality evidence for %s: %w", requirement.GateType, err)
		}
		evidence[requirement.GateType] = gate
	}
	return hasCurrentDeterministicQualityEvidence(requirements, evidence, taskID), nil
}

func structuredTestsPassed(gateType string, artifact map[string]any) (int, bool) {
	switch gateType {
	case "unit_test", "integration_test", "acceptance":
	default:
		return 0, false
	}
	result, _ := artifact["result"].(map[string]any)
	tests, _ := result["tests"].([]any)
	if len(tests) == 0 {
		return 0, false
	}
	findings, _ := result["findings"].([]any)
	for _, item := range tests {
		test, _ := item.(map[string]any)
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(test["status"])), "passed") {
			continue
		}
		if !nonBlockingTestFailure(test, findings) {
			return len(tests), false
		}
	}
	return len(tests), true
}

func structuredQAHandoff(content []byte) bool {
	var result map[string]any
	if err := json.Unmarshal(content, &result); err != nil ||
		!strings.EqualFold(strings.TrimSpace(fmt.Sprint(result["verdict"])), "pass") ||
		!strings.EqualFold(strings.TrimSpace(fmt.Sprint(result["status"])), "passed") {
		return false
	}
	_, passed := structuredTestsPassed("acceptance", map[string]any{"result": result})
	return passed
}

func validatedQAHandoffArtifactID(artifactType string, artifact map[string]any) (string, bool) {
	if artifactType != "qa_handoff" {
		return "", false
	}
	valid, ok := artifact["valid"].(bool)
	if !ok || !valid {
		return "", false
	}
	artifactID, ok := artifact["artifact_id"].(string)
	if !ok || strings.TrimSpace(artifactID) == "" {
		return "", false
	}
	if _, err := util.ParseUUID(artifactID); err != nil {
		return "", false
	}
	agentID, ok := artifact["agent_id"].(string)
	if !ok || strings.TrimSpace(agentID) == "" {
		return "", false
	}
	if _, err := util.ParseUUID(agentID); err != nil {
		return "", false
	}
	result, ok := artifact["result"]
	if !ok {
		return "", false
	}
	content, err := json.Marshal(result)
	if err != nil || !structuredQAHandoff(content) {
		return "", false
	}
	return artifactID, true
}

func (r *Runtime) hasDurableQAHandoffArtifact(
	ctx context.Context,
	q nodeQualityContext,
	taskID, artifactType string,
	artifact map[string]any,
) (string, bool, error) {
	artifactID, valid := validatedQAHandoffArtifactID(artifactType, artifact)
	if !valid {
		return "", false, nil
	}
	resultJSON, err := json.Marshal(artifact["result"])
	if err != nil {
		return "", false, nil
	}
	agentID, ok := artifact["agent_id"].(string)
	if !ok {
		return "", false, nil
	}
	var durable bool
	err = r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM autonomous_project_artifact
			WHERE id=$1::uuid AND workspace_id=$2 AND project_id=$3
			  AND plan_id=$4 AND node_id=$5
			  AND artifact_type='qa_handoff' AND status='active'
			  AND valid=TRUE AND immutable=TRUE AND producer_agent_id=$8::uuid
			  AND content ->> 'task_id'=$6 AND content ->> 'issue_id'=$7
			  AND content -> 'result'=$9::jsonb
		)
	`, artifactID, q.WorkspaceID, q.ProjectID, q.PlanID, q.NodeID, taskID,
		util.UUIDToString(q.IssueID), agentID, string(resultJSON)).Scan(&durable)
	if err != nil {
		return "", false, err
	}
	return artifactID, durable, nil
}

func nonBlockingTestFailure(test map[string]any, findings []any) bool {
	evidence := strings.TrimSpace(fmt.Sprint(test["evidence"]))
	if evidence == "" {
		return false
	}
	testName := strings.ToLower(strings.TrimSpace(fmt.Sprint(test["name"])))
	for _, item := range findings {
		finding, _ := item.(map[string]any)
		if blocking, _ := finding["blocking"].(bool); blocking {
			continue
		}
		findingEvidence := strings.TrimSpace(fmt.Sprint(finding["evidence"]))
		if findingEvidence == evidence {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(test["status"])), "skipped") && strings.EqualFold(strings.TrimSpace(fmt.Sprint(finding["category"])), "test") {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(finding["category"])), "migration") && strings.Contains(testName, "migration") && sharedNumericToken(evidence, fmt.Sprint(finding["description"])) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(evidence), "outside this change")
}

func sharedNumericToken(left, right string) bool {
	for _, token := range strings.FieldsFunc(left, func(r rune) bool { return r < '0' || r > '9' }) {
		if len(token) >= 3 && strings.Contains(right, token) {
			return true
		}
	}
	return false
}

func reviewApproved(artifact map[string]any) bool {
	result, _ := artifact["result"].(map[string]any)
	if result == nil {
		return false
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return false
	}
	var approval struct {
		Verdict            string `json:"verdict"`
		GoalAlignmentScore *int   `json:"goal_alignment_score"`
	}
	if err := json.Unmarshal(encoded, &approval); err != nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(approval.Verdict), "approved") {
		return false
	}
	return approval.GoalAlignmentScore == nil || *approval.GoalAlignmentScore >= 80
}

func (r *Runtime) reconcileCompletedProjectQuality(ctx context.Context, issue db.Issue) error {
	q, err := r.loadNodeQualityByIssue(ctx, issue.WorkspaceID, issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT artifact_type, content
		FROM autonomous_project_artifact
		WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
		  AND status='active' AND valid=TRUE
		ORDER BY CASE WHEN artifact_type='review' THEN 1 ELSE 0 END, created_at ASC
	`, q.WorkspaceID, q.ProjectID, q.NodeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var artifactType string
		var raw []byte
		if err := rows.Scan(&artifactType, &raw); err != nil {
			return err
		}
		var artifact map[string]any
		if err := json.Unmarshal(raw, &artifact); err != nil {
			return err
		}
		taskID, _ := artifact["task_id"].(string)
		blocked, err := r.applyTaskQualityEvidence(ctx, q, taskID, artifactType, artifact)
		if err != nil || blocked {
			return err
		}
	}
	return rows.Err()
}
