package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
			"deterministic": req.Deterministic,
			"autonomy": q.Policy.Autonomy,
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
		    attempt=attempt+1,
		    last_error=NULLIF($7,''),
		    started_at=COALESCE(started_at,now()),
		    completed_at=CASE WHEN $5 IN ('passed','failed','skipped') THEN now() ELSE completed_at END,
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

func (r *Runtime) runDeterministicGate(ctx context.Context, q nodeQualityContext, gateType string, artifact map[string]any) error {
	required, deterministic, err := r.requiredGate(ctx, q, gateType)
	if err != nil || !required {
		return err
	}
	if !deterministic {
		return r.passSemanticGate(ctx, q, gateType, artifact)
	}
	if r.qualityGateRunner == nil {
		gateErr := fmt.Sprintf("%v: %s", projectorchestration.ErrQualityGateUnavailable, gateType)
		_ = r.recordPolicyGate(ctx, q, gateType, "failed", gateErr, map[string]any{"mode": "missing_runner"})
		return fmt.Errorf("%w: gate=%s autonomy=%s", projectorchestration.ErrQualityGateUnavailable, gateType, q.Policy.Autonomy)
	}
	result, runErr := r.qualityGateRunner.Run(ctx, projectorchestration.QualityGateRequest{
		WorkspaceID: q.WorkspaceID, ProjectID: q.ProjectID, PlanID: q.PlanID,
		NodeID: q.NodeID, IssueID: q.IssueID, GateType: gateType, Artifact: artifact,
	})
	evidence := map[string]any{"mode": "deterministic_runner", "runner_evidence": result.Evidence}
	if runErr != nil {
		_ = r.recordPolicyGate(ctx, q, gateType, "failed", runErr.Error(), evidence)
		return runErr
	}
	if !result.Passed {
		msg := strings.TrimSpace(result.Error)
		if msg == "" {
			msg = "deterministic quality gate failed"
		}
		_ = r.recordPolicyGate(ctx, q, gateType, "failed", msg, evidence)
		return errors.New(msg)
	}
	return r.recordPolicyGate(ctx, q, gateType, "passed", "", evidence)
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
	gateType := projectQualityGateType(q.Kind)
	if gateType == "" {
		return false, nil
	}
	required, deterministic, err := r.requiredGate(ctx, q, gateType)
	if err != nil || !required {
		return false, err
	}
	var gateErr error
	if deterministic {
		gateErr = r.runDeterministicGate(ctx, q, gateType, artifact)
	} else {
		gateErr = r.passSemanticGate(ctx, q, gateType, map[string]any{
			"task_id": util.UUIDToString(task.ID), "artifact_type": artifactType,
		})
	}
	if gateErr == nil {
		return false, nil
	}
	if err := r.blockForQualityFailure(ctx, q, gateType, gateErr); err != nil {
		return true, err
	}
	return true, nil
}
