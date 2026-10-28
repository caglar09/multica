package projectorchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

var ErrEpicExpansionStale = errors.New("active project Epic changed before Task decomposition could be persisted")

// DecomposeEpic asks the planner for a Task-only sub-DAG when an Epic becomes
// active. The returned plan is still a complete graph so it can be validated,
// shown in the project snapshot, and persisted as an immutable revision.
func (p *Planner) DecomposeEpic(ctx context.Context, input PlanningInput, plan Plan, epicKey string) (Plan, RuntimeExecution, error) {
	if p == nil || p.executor == nil {
		return Plan{}, RuntimeExecution{}, ErrPlannerUnavailable
	}
	if err := ValidatePlan(plan, p.maxNodes); err != nil {
		return Plan{}, RuntimeExecution{}, fmt.Errorf("validate current plan before Epic decomposition: %w", err)
	}
	index := planNodeIndex(plan.Nodes, strings.TrimSpace(epicKey))
	if index < 0 || !plan.Nodes[index].IsEpic {
		return Plan{}, RuntimeExecution{}, errors.New("active Epic was not found in the current plan")
	}
	for _, node := range plan.Nodes {
		if node.ParentEpicKey == epicKey {
			return Plan{}, RuntimeExecution{}, errors.New("Epic already has Task children")
		}
	}

	input.CurrentPlan = &plan
	input.Policy = plan.Policy
	execution, err := p.executor.ExecuteProjectPlan(ctx, input, projectPlannerSystemPrompt(), projectEpicDecompositionUserPrompt(input, plan.Nodes[index]))
	if err != nil {
		return Plan{}, RuntimeExecution{}, err
	}
	decomposition, err := ParsePlan(execution.Output)
	if err != nil {
		return Plan{}, execution, fmt.Errorf("decode Epic Task decomposition: %w", err)
	}
	expanded, err := mergeEpicTasks(plan, plan.Nodes[index], decomposition.Nodes, decomposition.Edges, p.maxNodes)
	if err != nil {
		return Plan{}, execution, fmt.Errorf("validate Epic Task decomposition: %w", err)
	}
	return expanded, execution, nil
}

func mergeEpicTasks(plan Plan, epic NodeSpec, taskNodes []NodeSpec, taskEdges []EdgeSpec, maxNodes int) (Plan, error) {
	if len(taskNodes) == 0 {
		return Plan{}, errors.New("Epic decomposition must create at least one Task")
	}
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}
	known := make(map[string]struct{}, len(plan.Nodes)+len(taskNodes))
	for _, node := range plan.Nodes {
		known[node.Key] = struct{}{}
		if node.ParentEpicKey == epic.Key {
			return Plan{}, fmt.Errorf("Epic %q already has Task children", epic.Key)
		}
	}
	allowedRequirements := make(map[string]struct{}, len(epic.RequirementIDs))
	for _, id := range epic.RequirementIDs {
		allowedRequirements[id] = struct{}{}
	}
	children := make([]NodeSpec, len(taskNodes))
	for i, node := range taskNodes {
		node.Key = strings.TrimSpace(node.Key)
		if node.Key == "" {
			return Plan{}, fmt.Errorf("Task %d has no key", i)
		}
		if _, exists := known[node.Key]; exists {
			return Plan{}, fmt.Errorf("Task key %q already exists in the plan", node.Key)
		}
		known[node.Key] = struct{}{}
		if node.IsEpic {
			return Plan{}, fmt.Errorf("nested Epic %q is not supported", node.Key)
		}
		node.ParentEpicKey = epic.Key
		for _, id := range node.RequirementIDs {
			if _, ok := allowedRequirements[id]; !ok {
				return Plan{}, fmt.Errorf("Task %q references requirement %q outside Epic %q", node.Key, id, epic.Key)
			}
		}
		children[i] = cloneNodeSpec(node)
	}

	childKeys := make(map[string]struct{}, len(children))
	for _, node := range children {
		childKeys[node.Key] = struct{}{}
	}
	childIncoming := make(map[string]bool, len(children))
	for _, edge := range taskEdges {
		if _, ok := childKeys[edge.From]; !ok {
			return Plan{}, fmt.Errorf("Task edge source %q is outside Epic %q", edge.From, epic.Key)
		}
		if _, ok := childKeys[edge.To]; !ok {
			return Plan{}, fmt.Errorf("Task edge target %q is outside Epic %q", edge.To, epic.Key)
		}
		if edge.Type == DependencyHard || edge.Type == DependencyArtifact {
			childIncoming[edge.To] = true
		}
	}

	expanded := plan
	expanded.Nodes = append(cloneNodeSpecs(plan.Nodes), children...)
	expanded.Edges = append([]EdgeSpec(nil), plan.Edges...)
	expanded.Edges = append(expanded.Edges, taskEdges...)
	for _, edge := range plan.Edges {
		if edge.To != epic.Key {
			continue
		}
		for _, child := range children {
			if childIncoming[child.Key] {
				continue
			}
			expanded.Edges = append(expanded.Edges, EdgeSpec{
				From: edge.From, To: child.Key, Type: edge.Type,
				RequiredArtifactType: edge.RequiredArtifactType,
			})
		}
	}
	expanded = EnsureLifecycle(expanded)
	expanded = HardenPlan(expanded)
	if err := ValidatePlan(expanded, maxNodes); err != nil {
		return Plan{}, err
	}
	return expanded, nil
}

// PersistEpicExpansion replaces the active plan with a new immutable revision
// after checking the source revision's exact approved digest and active Epic.
// FinalizePlanLifecycleTx then applies the normal approval policy to the new
// digest, so the old approval is never transferred to a changed graph.
func (s *Store) PersistEpicExpansion(
	ctx context.Context,
	workspaceID, projectID, sourcePlanID pgtype.UUID,
	sourceDigest, epicKey, plannerName, plannerModel string,
	expanded Plan,
) (StoredPlan, error) {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid || !sourcePlanID.Valid {
		return StoredPlan{}, errors.New("Epic expansion requires a configured store and valid project plan")
	}
	if strings.TrimSpace(sourceDigest) == "" || strings.TrimSpace(epicKey) == "" {
		return StoredPlan{}, errors.New("Epic expansion requires the source digest and Epic key")
	}
	if err := ValidatePlan(expanded, DefaultMaxNodes); err != nil {
		return StoredPlan{}, err
	}
	if idx := planNodeIndex(expanded.Nodes, epicKey); idx < 0 || !expanded.Nodes[idx].IsEpic {
		return StoredPlan{}, errors.New("expanded plan must retain the target Epic")
	}
	hasChildren := false
	for _, node := range expanded.Nodes {
		if node.ParentEpicKey == epicKey {
			hasChildren = true
			break
		}
	}
	if !hasChildren {
		return StoredPlan{}, errors.New("expanded plan has no Tasks for the target Epic")
	}

	brainMemories, err := preparePlanBrainMemories(ctx, expanded)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("prepare expanded plan brain embeddings: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StoredPlan{}, err
	}
	defer tx.Rollback(ctx)
	lockKey := "autonomous-project-plan:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return StoredPlan{}, err
	}
	var latestID pgtype.UUID
	var latestRevision int64
	var status, digest string
	var approvedAt pgtype.Timestamptz
	var digestMatches bool
	err = tx.QueryRow(ctx, `
		SELECT id, revision, status, plan_digest, approved_at,
		       plan_digest = autonomous_project_plan_digest(id)
		FROM autonomous_project_plan
		WHERE workspace_id=$1 AND project_id=$2
		ORDER BY revision DESC
		LIMIT 1
		FOR UPDATE
	`, workspaceID, projectID).Scan(&latestID, &latestRevision, &status, &digest, &approvedAt, &digestMatches)
	if err != nil {
		return StoredPlan{}, err
	}
	if latestID != sourcePlanID || status != "active" || digest != sourceDigest || !digestMatches || !approvedAt.Valid {
		return StoredPlan{}, ErrEpicExpansionStale
	}
	var epicStatus string
	err = tx.QueryRow(ctx, `
		SELECT n.status
		FROM autonomous_project_plan_node n
		WHERE n.plan_id=$1 AND n.node_key=$2 AND n.is_epic=TRUE
		  AND NOT EXISTS (
		      SELECT 1 FROM autonomous_project_plan_node child
		      WHERE child.plan_id=n.plan_id AND child.parent_epic_key=n.node_key
		  )
		FOR UPDATE
	`, sourcePlanID, epicKey).Scan(&epicStatus)
	if err != nil || epicStatus != "ready" {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return StoredPlan{}, err
		}
		return StoredPlan{}, ErrEpicExpansionStale
	}
	if expanded.SpecificationRevisionID == "" {
		return StoredPlan{}, errors.New("expanded plan specification revision ID is required")
	}
	sourceRevision := "epic-expansion:" + util.UUIDToString(sourcePlanID) + ":" + epicKey
	stored, err := s.persistPlanTx(ctx, tx, workspaceID, projectID, sourceRevision, plannerName, plannerModel, expanded, brainMemories)
	if err != nil {
		return StoredPlan{}, err
	}
	newPlanID, err := parseProjectUUID(stored.ID)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("parse expanded plan ID: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status='running', ready_at=NULL,
		    started_at=COALESCE(started_at, now()), updated_at=now()
		WHERE plan_id=$1 AND node_key=$2 AND is_epic=TRUE
	`, newPlanID, epicKey)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("mark expanded Epic active: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredPlan{}, err
	}
	return stored, nil
}

// ReconcileCompletedEpics closes active Epics whose Task children all passed.
// refreshReadyTx calls this before releasing successors into the ready queue.
func reconcileCompletedEpicsTx(ctx context.Context, tx pgx.Tx, planID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node epic
		SET status='completed', completed_at=COALESCE(completed_at, now()), updated_at=now()
		FROM autonomous_project_plan p
		WHERE epic.plan_id=$1 AND p.id=epic.plan_id
		  AND p.status='active'
		  AND p.approved_at IS NOT NULL
		  AND p.plan_digest=autonomous_project_plan_digest(p.id)
		  AND epic.is_epic=TRUE AND epic.status='running'
		  AND EXISTS (
		      SELECT 1 FROM autonomous_project_plan_node child
		      WHERE child.plan_id=epic.plan_id AND child.parent_epic_key=epic.node_key
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM autonomous_project_plan_node child
		      WHERE child.plan_id=epic.plan_id AND child.parent_epic_key=epic.node_key
		        AND child.status <> 'completed'
		  )
	`, planID)
	return err
}
