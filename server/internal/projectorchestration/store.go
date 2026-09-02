package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
)

var ErrBudgetExceeded = errors.New("autonomous project budget exhausted")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) PersistPlan(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	sourceRevision, plannerName, plannerModel string,
	plan Plan,
) (StoredPlan, error) {
	if s == nil || s.pool == nil {
		return StoredPlan{}, errors.New("project orchestration store is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid {
		return StoredPlan{}, errors.New("workspace_id and project_id are required")
	}
	if strings.TrimSpace(sourceRevision) == "" {
		return StoredPlan{}, errors.New("source revision is required")
	}
	if err := ValidatePlan(plan, DefaultMaxNodes); err != nil {
		return StoredPlan{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("begin project plan persistence: %w", err)
	}
	defer tx.Rollback(ctx)

	lockKey := "autonomous-project-plan:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return StoredPlan{}, fmt.Errorf("lock project plan: %w", err)
	}

	var existingID pgtype.UUID
	var existingRevision int64
	err = tx.QueryRow(ctx, `
		SELECT id, revision
		FROM autonomous_project_plan
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND source_revision = $3
		ORDER BY revision DESC
		LIMIT 1
	`, workspaceID, projectID, sourceRevision).Scan(&existingID, &existingRevision)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return StoredPlan{}, err
		}
		return s.LoadPlan(ctx, workspaceID, projectID, existingRevision)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StoredPlan{}, fmt.Errorf("check existing project plan revision: %w", err)
	}

	var revision int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(revision), 0) + 1
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(&revision); err != nil {
		return StoredPlan{}, fmt.Errorf("allocate project plan revision: %w", err)
	}

	specJSON, err := json.Marshal(plan.Specification)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("encode project specification: %w", err)
	}
	policyJSON, err := json.Marshal(plan.Policy)
	if err != nil {
		return StoredPlan{}, fmt.Errorf("encode project policy: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'superseded', updated_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND status IN ('draft', 'active')
	`, workspaceID, projectID); err != nil {
		return StoredPlan{}, fmt.Errorf("supersede previous project plan: %w", err)
	}

	var planID pgtype.UUID
	var createdAt, updatedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_plan (
			workspace_id, project_id, revision, source_revision,
			planner_name, planner_model, goal, specification, policy, status
		)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, 'active')
		RETURNING id, created_at, updated_at
	`, workspaceID, projectID, revision, sourceRevision, plannerName, plannerModel, plan.Goal, specJSON, policyJSON).
		Scan(&planID, &createdAt, &updatedAt); err != nil {
		return StoredPlan{}, fmt.Errorf("insert autonomous project plan: %w", err)
	}

	for _, node := range plan.Nodes {
		capabilities, _ := json.Marshal(node.RequiredCapabilities)
		criteria, _ := json.Marshal(node.AcceptanceCriteria)
		maxAttempts := node.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_plan_node (
				plan_id, workspace_id, project_id, node_key, kind, title,
				description, priority, required_role_family, required_capabilities,
				acceptance_criteria, risk_level, max_attempts
			)
			VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, NULLIF($9, ''), $10,
				$11, $12, $13
			)
		`, planID, workspaceID, projectID, node.Key, string(node.Kind), node.Title,
			node.Description, node.Priority, node.RequiredRoleFamily, capabilities,
			criteria, string(node.Risk), maxAttempts); err != nil {
			return StoredPlan{}, fmt.Errorf("insert project plan node %q: %w", node.Key, err)
		}
	}

	for _, edge := range plan.Edges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_plan_edge (
				plan_id, workspace_id, project_id, from_node_key, to_node_key, dependency_type
			)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, planID, workspaceID, projectID, edge.From, edge.To, string(edge.Type)); err != nil {
			return StoredPlan{}, fmt.Errorf("insert project plan edge %s -> %s: %w", edge.From, edge.To, err)
		}
	}

	if err := seedBrain(ctx, tx, workspaceID, projectID, planID, plan); err != nil {
		return StoredPlan{}, err
	}
	if err := upsertBudget(ctx, tx, workspaceID, projectID, plan.Policy.Budget); err != nil {
		return StoredPlan{}, err
	}
	if err := refreshReadyTx(ctx, tx, planID); err != nil {
		return StoredPlan{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return StoredPlan{}, fmt.Errorf("commit project plan: %w", err)
	}

	return StoredPlan{
		ID:             util.UUIDToString(planID),
		WorkspaceID:    util.UUIDToString(workspaceID),
		ProjectID:      util.UUIDToString(projectID),
		Revision:       revision,
		SourceRevision: sourceRevision,
		PlannerName:    plannerName,
		PlannerModel:   plannerModel,
		Status:         "active",
		Plan:           plan,
		CreatedAt:      createdAt.Time,
		UpdatedAt:      updatedAt.Time,
	}, nil
}

func seedBrain(ctx context.Context, tx pgx.Tx, workspaceID, projectID, planID pgtype.UUID, plan Plan) error {
	insert := func(entryType, subject string, content any) error {
		raw, err := json.Marshal(content)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry (
				workspace_id, project_id, plan_id, entry_type, subject, content,
				source_type, source_id, confidence, created_by_type
			)
			VALUES ($1, $2, $3, $4, $5, $6, 'project_plan', $7, 1.0, 'system')
		`, workspaceID, projectID, planID, entryType, subject, raw, util.UUIDToString(planID))
		return err
	}

	if err := insert("requirement", "goal", map[string]any{"goal": plan.Goal}); err != nil {
		return fmt.Errorf("seed project brain goal: %w", err)
	}
	for i, requirement := range plan.Specification.Requirements {
		if err := insert("requirement", fmt.Sprintf("requirement:%d", i+1), map[string]any{"text": requirement}); err != nil {
			return fmt.Errorf("seed project brain requirement: %w", err)
		}
	}
	for i, constraint := range plan.Specification.Constraints {
		if err := insert("constraint", fmt.Sprintf("constraint:%d", i+1), map[string]any{"text": constraint}); err != nil {
			return fmt.Errorf("seed project brain constraint: %w", err)
		}
	}
	return nil
}

func upsertBudget(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, budget BudgetPolicy) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_budget (
			project_id, workspace_id, token_limit, runtime_seconds_limit,
			cost_microunits_limit, max_parallel_nodes, max_total_attempts
		)
		VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, 0), NULLIF($5, 0), $6, $7)
		ON CONFLICT (project_id) DO UPDATE
		SET token_limit = EXCLUDED.token_limit,
		    runtime_seconds_limit = EXCLUDED.runtime_seconds_limit,
		    cost_microunits_limit = EXCLUDED.cost_microunits_limit,
		    max_parallel_nodes = EXCLUDED.max_parallel_nodes,
		    max_total_attempts = EXCLUDED.max_total_attempts,
		    updated_at = now()
		WHERE autonomous_project_budget.workspace_id = EXCLUDED.workspace_id
	`, projectID, workspaceID, budget.TokenLimit, budget.RuntimeSeconds, budget.CostMicrounits,
		budget.MaxParallelNodes, budget.MaxTotalAttempts)
	if err != nil {
		return fmt.Errorf("upsert project budget: %w", err)
	}
	return nil
}

func (s *Store) RefreshReady(ctx context.Context, workspaceID, projectID pgtype.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var planID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
		ORDER BY revision DESC
		LIMIT 1
		FOR UPDATE
	`, workspaceID, projectID).Scan(&planID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if err := refreshReadyTx(ctx, tx, planID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func refreshReadyTx(ctx context.Context, tx pgx.Tx, planID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node n
		SET status = 'ready',
		    ready_at = COALESCE(ready_at, now()),
		    updated_at = now()
		WHERE n.plan_id = $1
		  AND n.status = 'pending'
		  AND NOT EXISTS (
			SELECT 1
			FROM autonomous_project_plan_edge e
			JOIN autonomous_project_plan_node dep
			  ON dep.plan_id = e.plan_id
			 AND dep.node_key = e.from_node_key
			WHERE e.plan_id = n.plan_id
			  AND e.to_node_key = n.node_key
			  AND e.dependency_type IN ('hard', 'artifact')
			  AND dep.status <> 'completed'
		  )
	`, planID)
	if err != nil {
		return fmt.Errorf("refresh ready project plan nodes: %w", err)
	}
	return nil
}

func (s *Store) ListReadyNodes(ctx context.Context, workspaceID, projectID pgtype.UUID, limit int) ([]ReadyNode, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if err := s.RefreshReady(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}

	var maxParallel int
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(max_parallel_nodes, 4)
		FROM autonomous_project_budget
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(&maxParallel); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	var running int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE p.workspace_id = $1
		  AND p.project_id = $2
		  AND p.status = 'active'
		  AND n.status IN ('running', 'verification')
	`, workspaceID, projectID).Scan(&running); err != nil {
		return nil, err
	}
	available := maxParallel - running
	if available <= 0 {
		return []ReadyNode{}, nil
	}
	if limit > available {
		limit = available
	}

	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.node_key, n.kind, n.title, n.description, n.priority,
		       n.risk_level, COALESCE(n.required_role_family, ''),
		       n.required_capabilities, n.acceptance_criteria, n.max_attempts
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		LEFT JOIN autonomous_project_control c
		  ON c.project_id = n.project_id AND c.workspace_id = n.workspace_id
		WHERE n.workspace_id = $1
		  AND n.project_id = $2
		  AND p.status = 'active'
		  AND n.status = 'ready'
		  AND COALESCE(c.paused, FALSE) = FALSE
		ORDER BY n.priority DESC, n.ready_at ASC, n.created_at ASC
		LIMIT $3
	`, workspaceID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ReadyNode, 0, limit)
	for rows.Next() {
		var item ReadyNode
		var id pgtype.UUID
		var kind, risk string
		var capabilitiesJSON, criteriaJSON []byte
		if err := rows.Scan(
			&id, &item.Key, &kind, &item.Title, &item.Description, &item.Priority,
			&risk, &item.RequiredRoleFamily, &capabilitiesJSON, &criteriaJSON, &item.MaxAttempts,
		); err != nil {
			return nil, err
		}
		item.ID = util.UUIDToString(id)
		item.Kind = NodeKind(kind)
		item.Risk = RiskLevel(risk)
		_ = json.Unmarshal(capabilitiesJSON, &item.RequiredCapabilities)
		_ = json.Unmarshal(criteriaJSON, &item.AcceptanceCriteria)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MarkNodeMaterialized(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey string,
	issueID pgtype.UUID,
) error {
	if !issueID.Valid {
		return errors.New("materialized issue id is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var maxTotalAttempts, totalAttempts int
	if err := tx.QueryRow(ctx, `
		SELECT max_total_attempts, total_attempts
		FROM autonomous_project_budget
		WHERE workspace_id = $1 AND project_id = $2
		FOR UPDATE
	`, workspaceID, projectID).Scan(&maxTotalAttempts, &totalAttempts); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		maxTotalAttempts = 100
		totalAttempts = 0
	}
	if totalAttempts >= maxTotalAttempts {
		return ErrBudgetExceeded
	}

	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node n
		SET materialized_issue_id = $4,
		    status = CASE WHEN status = 'ready' THEN 'running' ELSE status END,
		    started_at = CASE WHEN status = 'ready' THEN COALESCE(started_at, now()) ELSE started_at END,
		    attempt = CASE WHEN status = 'ready' THEN attempt + 1 ELSE attempt END,
		    updated_at = now()
		FROM autonomous_project_plan p
		WHERE n.plan_id = p.id
		  AND n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND p.status = 'active'
		  AND n.status = 'ready'
		  AND n.attempt < n.max_attempts
	`, workspaceID, projectID, nodeKey, issueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("active project plan node %q is not ready or exhausted", nodeKey)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_budget
		SET total_attempts = total_attempts + 1, updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AddUsage(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	tokens, runtimeSeconds, costMicrounits int64,
) error {
	if tokens < 0 || runtimeSeconds < 0 || costMicrounits < 0 {
		return errors.New("project usage deltas cannot be negative")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_budget
		SET tokens_used = tokens_used + $3,
		    runtime_seconds_used = runtime_seconds_used + $4,
		    cost_microunits_used = cost_microunits_used + $5,
		    updated_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND (token_limit IS NULL OR tokens_used + $3 <= token_limit)
		  AND (runtime_seconds_limit IS NULL OR runtime_seconds_used + $4 <= runtime_seconds_limit)
		  AND (cost_microunits_limit IS NULL OR cost_microunits_used + $5 <= cost_microunits_limit)
	`, workspaceID, projectID, tokens, runtimeSeconds, costMicrounits)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrBudgetExceeded
	}
	return nil
}


type FailureDisposition string

const (
	FailureRetry   FailureDisposition = "retry"
	FailureBlocked FailureDisposition = "blocked"
)

func (s *Store) FailNodeByIssue(
	ctx context.Context,
	workspaceID, issueID pgtype.UUID,
	reason string,
) (FailureDisposition, string, pgtype.UUID, error) {
	if s == nil || s.pool == nil {
		return "", "", pgtype.UUID{}, errors.New("project orchestration store is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", "", pgtype.UUID{}, err
	}
	defer tx.Rollback(ctx)

	var nodeID, projectID pgtype.UUID
	var nodeKey string
	var attempt, maxAttempts int
	err = tx.QueryRow(ctx, `
		SELECT id, project_id, node_key, attempt, max_attempts
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1
		  AND materialized_issue_id = $2
		  AND status IN ('running', 'verification', 'ready')
		ORDER BY updated_at DESC
		LIMIT 1
		FOR UPDATE
	`, workspaceID, issueID).Scan(&nodeID, &projectID, &nodeKey, &attempt, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", pgtype.UUID{}, nil
	}
	if err != nil {
		return "", "", pgtype.UUID{}, err
	}
	if len(reason) > 2000 {
		reason = reason[:2000]
	}

	disposition := FailureBlocked
	nextStatus := "blocked"
	if attempt < maxAttempts {
		disposition = FailureRetry
		nextStatus = "ready"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = $3,
		    blocked_reason = $4,
		    ready_at = CASE WHEN $3 = 'ready' THEN now() ELSE ready_at END,
		    updated_at = now()
		WHERE id = $1 AND workspace_id = $2
	`, nodeID, workspaceID, nextStatus, reason); err != nil {
		return "", "", pgtype.UUID{}, err
	}
	if disposition == FailureBlocked {
		if _, err := tx.Exec(ctx, `
			UPDATE autonomous_project_plan p
			SET status = 'blocked', updated_at = now()
			WHERE p.id = (
				SELECT plan_id FROM autonomous_project_plan_node WHERE id = $1
			)
			  AND p.status = 'active'
		`, nodeID); err != nil {
			return "", "", pgtype.UUID{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", pgtype.UUID{}, err
	}
	return disposition, nodeKey, projectID, nil
}

func (s *ResumePlanAfterNodeRetry(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'active', updated_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND status = 'blocked'
		  AND EXISTS (
			SELECT 1
			FROM autonomous_project_plan_node n
			WHERE n.plan_id = autonomous_project_plan.id
			  AND n.status IN ('ready', 'running', 'verification')
		  )
	`, workspaceID, projectID)
	return err
}

func (s *Store) CompleteNodeByIssue(ctx context.Context, workspaceID, issueID pgtype.UUID) error {
	var projectID, planID pgtype.UUID
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'completed', completed_at = COALESCE(completed_at, now()), updated_at = now()
		WHERE workspace_id = $1
		  AND materialized_issue_id = $2
		  AND status IN ('running', 'verification')
	`, workspaceID, issueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT project_id, plan_id
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1 AND materialized_issue_id = $2
		ORDER BY updated_at DESC
		LIMIT 1
	`, workspaceID, issueID).Scan(&projectID, &planID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := refreshReadyTx(ctx, tx, planID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan p
		SET status = 'completed', updated_at = now()
		WHERE p.id = $1
		  AND NOT EXISTS (
			SELECT 1 FROM autonomous_project_plan_node n
			WHERE n.plan_id = p.id
			  AND n.status NOT IN ('completed', 'cancelled')
		  )
	`, planID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) LoadPlan(ctx context.Context, workspaceID, projectID pgtype.UUID, revision int64) (StoredPlan, error) {
	var out StoredPlan
	var id pgtype.UUID
	var model pgtype.Text
	var specJSON, policyJSON []byte
	var createdAt, updatedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		SELECT id, revision, source_revision, planner_name, planner_model,
		       goal, specification, policy, status, created_at, updated_at
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2 AND revision = $3
	`, workspaceID, projectID, revision).Scan(
		&id, &out.Revision, &out.SourceRevision, &out.PlannerName, &model,
		&out.Plan.Goal, &specJSON, &policyJSON, &out.Status, &createdAt, &updatedAt,
	)
	if err != nil {
		return StoredPlan{}, err
	}
	out.ID = util.UUIDToString(id)
	out.WorkspaceID = util.UUIDToString(workspaceID)
	out.ProjectID = util.UUIDToString(projectID)
	out.PlannerModel = model.String
	out.Plan.Version = CurrentPlanVersion
	out.CreatedAt = createdAt.Time
	out.UpdatedAt = updatedAt.Time
	if err := json.Unmarshal(specJSON, &out.Plan.Specification); err != nil {
		return StoredPlan{}, err
	}
	if err := json.Unmarshal(policyJSON, &out.Plan.Policy); err != nil {
		return StoredPlan{}, err
	}
	nodes, edges, err := s.loadSpecs(ctx, id)
	if err != nil {
		return StoredPlan{}, err
	}
	out.Plan.Nodes = nodes
	out.Plan.Edges = edges
	return out, nil
}

func (s *Store) LoadLatestPlan(ctx context.Context, workspaceID, projectID pgtype.UUID) (StoredPlan, bool, error) {
	var revision int64
	err := s.pool.QueryRow(ctx, `
		SELECT revision
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY revision DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredPlan{}, false, nil
	}
	if err != nil {
		return StoredPlan{}, false, err
	}
	plan, err := s.LoadPlan(ctx, workspaceID, projectID, revision)
	return plan, err == nil, err
}

func (s *Store) loadSpecs(ctx context.Context, planID pgtype.UUID) ([]NodeSpec, []EdgeSpec, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT node_key, kind, title, description, priority,
		       COALESCE(required_role_family, ''), required_capabilities,
		       acceptance_criteria, risk_level, max_attempts
		FROM autonomous_project_plan_node
		WHERE plan_id = $1
		ORDER BY created_at, node_key
	`, planID)
	if err != nil {
		return nil, nil, err
	}
	nodes := []NodeSpec{}
	for rows.Next() {
		var node NodeSpec
		var kind, risk string
		var caps, criteria []byte
		if err := rows.Scan(&node.Key, &kind, &node.Title, &node.Description, &node.Priority,
			&node.RequiredRoleFamily, &caps, &criteria, &risk, &node.MaxAttempts); err != nil {
			rows.Close()
			return nil, nil, err
		}
		node.Kind = NodeKind(kind)
		node.Risk = RiskLevel(risk)
		_ = json.Unmarshal(caps, &node.RequiredCapabilities)
		_ = json.Unmarshal(criteria, &node.AcceptanceCriteria)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	edgeRows, err := s.pool.Query(ctx, `
		SELECT from_node_key, to_node_key, dependency_type
		FROM autonomous_project_plan_edge
		WHERE plan_id = $1
		ORDER BY created_at, from_node_key, to_node_key
	`, planID)
	if err != nil {
		return nil, nil, err
	}
	defer edgeRows.Close()
	edges := []EdgeSpec{}
	for edgeRows.Next() {
		var edge EdgeSpec
		var typ string
		if err := edgeRows.Scan(&edge.From, &edge.To, &typ); err != nil {
			return nil, nil, err
		}
		edge.Type = DependencyType(typ)
		edges = append(edges, edge)
	}
	return nodes, edges, edgeRows.Err()
}
