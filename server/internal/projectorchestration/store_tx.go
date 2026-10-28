package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

var ErrProjectAutonomousCancelled = errors.New("project autonomous execution is permanently cancelled")

func projectLeaderIssuePriority(priority int) string {
	switch {
	case priority >= 90:
		return "urgent"
	case priority >= 60:
		return "high"
	case priority >= 30:
		return "medium"
	case priority > 0:
		return "low"
	default:
		return "none"
	}
}

func findLogicalNode(nodes []LogicalPlanNode, id string) (LogicalPlanNode, bool) {
	for _, node := range nodes {
		if node.LogicalNodeID == id {
			return node, true
		}
	}
	return LogicalPlanNode{}, false
}

// persistPlanTx is the transaction-owned plan writer used by approved change
// application. PersistPlan remains the public planner entry point; this core
// prevents a visible new revision from escaping before node carry-forward and
// change-request state are committed.
func (s *Store) persistPlanTx(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, sourceRevision, plannerName, plannerModel string, plan Plan, brainMemories []preparedBrainMemory) (StoredPlan, error) {
	if s == nil || tx == nil {
		return StoredPlan{}, errors.New("project plan transaction is not configured")
	}
	normalizePlanArtifactEdges(&plan)
	if err := ValidatePlan(plan, DefaultMaxNodes); err != nil {
		return StoredPlan{}, err
	}
	if strings.TrimSpace(plan.SpecificationRevisionID) == "" {
		return StoredPlan{}, errors.New("project plan specification revision ID is required")
	}
	specificationRevisionID, err := parseProjectUUID(plan.SpecificationRevisionID)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("parse project plan specification revision ID: %w", err)
	}
	var verifiedRevisionID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM autonomous_project_specification_revision
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
		FOR KEY SHARE
	`, specificationRevisionID, workspaceID, projectID).Scan(&verifiedRevisionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredPlan{}, errors.New("project plan specification revision does not belong to workspace/project")
		}
		return StoredPlan{}, fmt.Errorf("verify project plan specification revision: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_control (project_id, workspace_id, paused, updated_at)
		VALUES ($1, $2, false, now())
		ON CONFLICT (project_id) DO UPDATE
		SET workspace_id=EXCLUDED.workspace_id, updated_at=now()
	`, projectID, workspaceID); err != nil {
		return StoredPlan{}, fmt.Errorf("ensure project control state before persisting plan: %w", err)
	}
	var cancelled bool
	err = tx.QueryRow(ctx, `
		SELECT cancelled_at IS NOT NULL
		FROM autonomous_project_control
		WHERE workspace_id=$1 AND project_id=$2
		FOR UPDATE
	`, workspaceID, projectID).Scan(&cancelled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StoredPlan{}, fmt.Errorf("check terminal project cancellation before persisting plan: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredPlan{}, errors.New("project control row disappeared before plan persistence")
	}
	if cancelled {
		return StoredPlan{}, ErrProjectAutonomousCancelled
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID).Scan(&revision); err != nil {
		return StoredPlan{}, fmt.Errorf("allocate project plan revision: %w", err)
	}
	var previousPlanID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2 ORDER BY revision DESC LIMIT 1`, workspaceID, projectID).Scan(&previousPlanID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StoredPlan{}, err
	}
	specJSON, err := json.Marshal(plan.Specification)
	if err != nil {
		return StoredPlan{}, err
	}
	policyJSON, err := json.Marshal(plan.Policy)
	if err != nil {
		return StoredPlan{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE autonomous_project_plan SET status='superseded',updated_at=now() WHERE workspace_id=$1 AND project_id=$2 AND status IN ('draft','active','blocked','awaiting_approval')`, workspaceID, projectID); err != nil {
		return StoredPlan{}, err
	}
	var planID pgtype.UUID
	var createdAt, updatedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `INSERT INTO autonomous_project_plan(workspace_id,project_id,revision,source_revision,specification_revision_id,planner_name,planner_model,goal,specification,policy,status,plan_digest) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,'draft',$11) RETURNING id,created_at,updated_at`, workspaceID, projectID, revision, sourceRevision, verifiedRevisionID, plannerName, plannerModel, plan.Goal, specJSON, policyJSON, strings.Repeat("0", 64)).Scan(&planID, &createdAt, &updatedAt); err != nil {
		return StoredPlan{}, err
	}
	for _, node := range plan.Nodes {
		capabilities, _ := json.Marshal(node.RequiredCapabilities)
		criteria, _ := json.Marshal(node.AcceptanceCriteria)
		requirementIDs := node.RequirementIDs
		if requirementIDs == nil {
			requirementIDs = []string{}
		}
		requirementIDsJSON, err := json.Marshal(requirementIDs)
		if err != nil {
			return StoredPlan{}, err
		}
		attempts := node.MaxAttempts
		if attempts <= 0 {
			attempts = 3
		}
		if _, err := tx.Exec(ctx, `INSERT INTO autonomous_project_plan_node(plan_id,workspace_id,project_id,node_key,kind,title,description,priority,required_role_family,required_capabilities,acceptance_criteria,requirement_ids,risk_level,max_attempts) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14)`, planID, workspaceID, projectID, node.Key, string(node.Kind), node.Title, node.Description, node.Priority, node.RequiredRoleFamily, capabilities, criteria, requirementIDsJSON, string(node.Risk), attempts); err != nil {
			return StoredPlan{}, err
		}
	}
	if previousPlanID.Valid {
		if _, err := tx.Exec(ctx, `UPDATE autonomous_project_plan_node fresh SET materialized_issue_id=prior.materialized_issue_id,assigned_role=prior.assigned_role,assigned_agent_id=prior.assigned_agent_id,attempt=prior.attempt,status=CASE WHEN prior.status IN('completed','cancelled','running','verification','blocked') THEN prior.status ELSE fresh.status END,ready_at=CASE WHEN prior.status IN('running','verification','blocked') THEN prior.ready_at ELSE fresh.ready_at END,started_at=CASE WHEN prior.status IN('running','verification','blocked','completed') THEN prior.started_at ELSE fresh.started_at END,completed_at=CASE WHEN prior.status IN('completed','cancelled') THEN prior.completed_at ELSE fresh.completed_at END,blocked_category=CASE WHEN prior.status='blocked' THEN prior.blocked_category ELSE NULL END,blocked_reason=CASE WHEN prior.status='blocked' THEN prior.blocked_reason ELSE NULL END,updated_at=now() FROM autonomous_project_plan_node prior WHERE fresh.plan_id=$1 AND prior.plan_id=$2 AND fresh.node_key=prior.node_key AND prior.materialized_issue_id IS NOT NULL AND (fresh.title=prior.title OR prior.status IN('running','verification','blocked'))`, planID, previousPlanID); err != nil {
			return StoredPlan{}, err
		}
	}
	for _, edge := range plan.Edges {
		if _, err := tx.Exec(ctx, `INSERT INTO autonomous_project_plan_edge(plan_id,workspace_id,project_id,from_node_key,to_node_key,dependency_type,required_artifact_type) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''))`, planID, workspaceID, projectID, edge.From, edge.To, string(edge.Type), edge.RequiredArtifactType); err != nil {
			return StoredPlan{}, err
		}
	}
	if err := seedBrain(ctx, tx, workspaceID, projectID, planID, brainMemories); err != nil {
		return StoredPlan{}, err
	}
	if err := upsertBudget(ctx, tx, workspaceID, projectID, plan.Policy.Budget); err != nil {
		return StoredPlan{}, err
	}
	lifecycle, err := FinalizePlanLifecycleTx(ctx, tx, planID, plan)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("finalize project plan lifecycle: %w", err)
	}
	if err := refreshReadyTx(ctx, tx, planID); err != nil {
		return StoredPlan{}, err
	}
	return StoredPlan{ID: util.UUIDToString(planID), WorkspaceID: util.UUIDToString(workspaceID), ProjectID: util.UUIDToString(projectID), Revision: revision, SourceRevision: sourceRevision, PlannerName: plannerName, PlannerModel: plannerModel, Status: lifecycle.Status, Plan: plan, CreatedAt: createdAt.Time, UpdatedAt: updatedAt.Time}, nil
}
