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

	// Preserve node/issue identity across replans. node_key is the planner's
	// stable logical identity; the latest prior revision is therefore the only
	// authoritative source for carry-forward state.
	var previousPlanID pgtype.UUID
	previousPlanErr := tx.QueryRow(ctx, `
		SELECT id
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY revision DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(&previousPlanID)
	if previousPlanErr != nil && !errors.Is(previousPlanErr, pgx.ErrNoRows) {
		return StoredPlan{}, fmt.Errorf("load previous project plan: %w", previousPlanErr)
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
		  AND status IN ('draft', 'active', 'blocked')
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

	// Reuse the prior revision's materialized issue and execution state when
	// the planner keeps the same logical node. Matching title+key is the normal
	// identity fence. In-flight/blocked work is carried by stable key even when
	// wording changed so a replan cannot kill active side effects mid-run.
	// Repurposed pending/completed keys with a new title are deliberately NOT
	// carried: the old issue is retired after commit and the new scope gets a
	// fresh issue. Plain pending/ready readiness is recomputed from the NEW graph.
	if previousPlanID.Valid {
		if _, err := tx.Exec(ctx, `
			UPDATE autonomous_project_plan_node fresh
			SET materialized_issue_id = prior.materialized_issue_id,
			    assigned_role = prior.assigned_role,
			    assigned_agent_id = prior.assigned_agent_id,
			    attempt = prior.attempt,
			    status = CASE
			        WHEN prior.status IN ('completed','cancelled','running','verification','blocked')
			            THEN prior.status
			        ELSE fresh.status
			    END,
			    ready_at = CASE WHEN prior.status IN ('running','verification','blocked') THEN prior.ready_at ELSE fresh.ready_at END,
			    started_at = CASE WHEN prior.status IN ('running','verification','blocked','completed') THEN prior.started_at ELSE fresh.started_at END,
			    completed_at = CASE WHEN prior.status IN ('completed','cancelled') THEN prior.completed_at ELSE fresh.completed_at END,
			    blocked_category = CASE WHEN prior.status = 'blocked' THEN prior.blocked_category ELSE NULL END,
			    blocked_reason = CASE WHEN prior.status = 'blocked' THEN prior.blocked_reason ELSE NULL END,
			    updated_at = now()
			FROM autonomous_project_plan_node prior
			WHERE fresh.plan_id = $1
			  AND prior.plan_id = $2
			  AND fresh.node_key = prior.node_key
			  AND prior.materialized_issue_id IS NOT NULL
			  AND (
			      fresh.title = prior.title
			      OR prior.status IN ('running','verification','blocked')
			  )
		`, planID, previousPlanID); err != nil {
			return StoredPlan{}, fmt.Errorf("carry forward project node identity: %w", err)
		}
	}

	for _, edge := range plan.Edges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_plan_edge (
				plan_id, workspace_id, project_id, from_node_key, to_node_key,
				dependency_type, required_artifact_type
			)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		`, planID, workspaceID, projectID, edge.From, edge.To, string(edge.Type), edge.RequiredArtifactType); err != nil {
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

// ResumeCompletedPlanForDiscoveredWork reopens the latest completed plan when
// an autonomous team member discovers additional project work after the plan
// reached Done. Completion is a quiescent state for a closed-loop project, not
// a tombstone: newly discovered work must be adoptable without a manual replan.
//
// The latest-plan fence prevents an older completed revision from being revived
// after a concurrent replan has already produced a newer plan.
func (s *Store) ResumeCompletedPlanForDiscoveredWork(
	ctx context.Context,
	workspaceID, projectID, planID pgtype.UUID,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_plan p
		SET status = 'active', updated_at = now()
		WHERE p.id = $1
		  AND p.workspace_id = $2
		  AND p.project_id = $3
		  AND p.status = 'completed'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM autonomous_project_plan newer
		      WHERE newer.workspace_id = p.workspace_id
		        AND newer.project_id = p.project_id
		        AND newer.revision > p.revision
		  )
	`, planID, workspaceID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var status string
	err = s.pool.QueryRow(ctx, `
		SELECT status
		FROM autonomous_project_plan
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
	`, planID, workspaceID, projectID).Scan(&status)
	if err != nil {
		return err
	}
	if status == "active" || status == "blocked" {
		return nil
	}
	return fmt.Errorf("project plan cannot be resumed from status %q", status)
}

func (s *Store) AppendPlanDelta(
	ctx context.Context,
	workspaceID, projectID, planID pgtype.UUID,
	nodes []NodeSpec,
	edges []EdgeSpec,
	primaryNodeKey string,
	issueID pgtype.UUID,
	assignedRole string,
	assignedAgentID pgtype.UUID,
	blockSourceKey string,
	brainSubject string,
	brainContent any,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid || !planID.Valid {
		return errors.New("workspace_id, project_id and plan_id are required")
	}
	if strings.TrimSpace(primaryNodeKey) == "" || !issueID.Valid {
		return errors.New("primary discovered node and issue are required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	lockKey := "autonomous-project-plan:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return fmt.Errorf("lock project plan delta: %w", err)
	}

	var current bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_plan
			WHERE id = $1
			  AND workspace_id = $2
			  AND project_id = $3
			  AND status IN ('active', 'blocked')
		)
	`, planID, workspaceID, projectID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return errors.New("project plan changed before discovered work could be adopted")
	}

	for _, node := range nodes {
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
			ON CONFLICT (plan_id, node_key) DO NOTHING
		`, planID, workspaceID, projectID, node.Key, string(node.Kind), node.Title,
			node.Description, node.Priority, node.RequiredRoleFamily, capabilities,
			criteria, string(node.Risk), maxAttempts); err != nil {
			return fmt.Errorf("insert discovered project node %q: %w", node.Key, err)
		}
	}

	var nodeCount int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM autonomous_project_plan_node
		WHERE plan_id = $1
	`, planID).Scan(&nodeCount); err != nil {
		return err
	}
	if nodeCount > DefaultMaxNodes {
		return fmt.Errorf("%w: discovered work would exceed maximum project node count %d", ErrInvalidPlan, DefaultMaxNodes)
	}

	for _, edge := range edges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_plan_edge (
				plan_id, workspace_id, project_id,
				from_node_key, to_node_key, dependency_type, required_artifact_type
			)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
			ON CONFLICT (plan_id, from_node_key, to_node_key, dependency_type) DO NOTHING
		`, planID, workspaceID, projectID, edge.From, edge.To, string(edge.Type), edge.RequiredArtifactType); err != nil {
			return fmt.Errorf("insert discovered project edge %s -> %s: %w", edge.From, edge.To, err)
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET materialized_issue_id = $5,
		    assigned_role = COALESCE(NULLIF($6, ''), assigned_role),
		    assigned_agent_id = CASE WHEN $7::uuid IS NULL THEN assigned_agent_id ELSE $7 END,
		    updated_at = now()
		WHERE plan_id = $1
		  AND workspace_id = $2
		  AND project_id = $3
		  AND node_key = $4
		  AND (materialized_issue_id IS NULL OR materialized_issue_id = $5)
	`, planID, workspaceID, projectID, primaryNodeKey, issueID, assignedRole, assignedAgentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("discovered project node %q could not bind issue", primaryNodeKey)
	}

	if strings.TrimSpace(blockSourceKey) != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE autonomous_project_plan_node
			SET status = 'blocked',
			    blocked_category = 'dependency',
			    blocked_reason = $5,
			    updated_at = now()
			WHERE plan_id = $1
			  AND workspace_id = $2
			  AND project_id = $3
			  AND node_key = $4
			  AND status NOT IN ('completed', 'cancelled')
		`, planID, workspaceID, projectID, blockSourceKey,
			"runtime-discovered prerequisite "+primaryNodeKey+" must complete"); err != nil {
			return fmt.Errorf("block source node for discovered prerequisite: %w", err)
		}
	}

	if brainContent != nil {
		raw, err := json.Marshal(brainContent)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry (
				workspace_id, project_id, plan_id, entry_type, subject, content,
				source_type, source_id, confidence, created_by_type
			)
			SELECT $1, $2, $3, 'fact', $4, $5,
			       'discovered_issue', $6, 0.9, 'agent'
			WHERE NOT EXISTS (
				SELECT 1
				FROM autonomous_project_brain_entry
				WHERE workspace_id = $1
				  AND project_id = $2
				  AND source_type = 'discovered_issue'
				  AND source_id = $6
			)
		`, workspaceID, projectID, planID, brainSubject, raw, util.UUIDToString(issueID)); err != nil {
			return fmt.Errorf("record discovered work in project brain: %w", err)
		}
	}

	if err := refreshReadyTx(ctx, tx, planID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	// Team/project planning can consume provider quota before the durable plan
	// (and therefore the budget row) exists. Seed first creation from already
	// attributed usage so the budget and the report describe the same project
	// cost from bootstrap onward. Existing budget counters are never reset on a
	// replan.
	_, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_budget (
			project_id, workspace_id, token_limit, runtime_seconds_limit,
			cost_microunits_limit, max_parallel_nodes, max_total_attempts,
			tokens_used, runtime_seconds_used, cost_microunits_used
		)
		SELECT
			$1, $2, NULLIF($3, 0), NULLIF($4, 0), NULLIF($5, 0), $6, $7,
			COALESCE(SUM(tokens), 0)::bigint,
			COALESCE(SUM(runtime_seconds), 0)::bigint,
			COALESCE(SUM(cost_microunits), 0)::bigint
		FROM autonomous_project_usage_accounting
		WHERE workspace_id = $2 AND project_id = $1
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
		WHERE workspace_id = $1 AND project_id = $2 AND status IN ('active', 'blocked')
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
			  AND (
			      (e.dependency_type = 'hard' AND dep.status <> 'completed')
			      OR (
			          e.dependency_type = 'artifact'
			          AND (
			              dep.status <> 'completed'
			              OR e.required_artifact_type IS NULL
			              OR NOT EXISTS (
			                  SELECT 1
			                  FROM autonomous_project_artifact a
			                  WHERE a.plan_id = e.plan_id
			                    AND a.node_id = dep.id
			                    AND a.artifact_type = e.required_artifact_type
			                    AND a.status = 'active'
			                    AND a.valid = TRUE
			                    AND a.artifact_revision = dep.spec_revision
			                    AND a.id = (
			                        SELECT latest.id
			                        FROM autonomous_project_artifact latest
			                        WHERE latest.plan_id = e.plan_id
			                          AND latest.node_id = dep.id
			                          AND latest.artifact_type = e.required_artifact_type
			                        ORDER BY latest.created_at DESC, latest.id DESC
			                        LIMIT 1
			                    )
			              )
			          )
			      )
			  )
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
		  AND p.status IN ('active', 'blocked')
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
		  AND p.status IN ('active', 'blocked')
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

func (s *Store) ListPlanNodes(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	limit int,
) ([]PlannedNode, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("project orchestration store is not configured")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if err := s.RefreshReady(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.node_key, n.kind, n.title, n.description, n.priority,
		       n.risk_level, COALESCE(n.required_role_family, ''),
		       n.required_capabilities, n.acceptance_criteria, n.max_attempts,
		       n.status, n.materialized_issue_id,
		       COALESCE(n.assigned_role, ''), n.assigned_agent_id, n.attempt
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.project_id = $2
		  AND p.status IN ('active', 'blocked')
		  AND n.status NOT IN ('completed', 'cancelled')
		ORDER BY n.priority DESC, n.created_at ASC, n.node_key ASC
		LIMIT $3
	`, workspaceID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]PlannedNode, 0, limit)
	for rows.Next() {
		var item PlannedNode
		var id, issueID, agentID pgtype.UUID
		var kind, risk string
		var capabilitiesJSON, criteriaJSON []byte
		if err := rows.Scan(
			&id, &item.Key, &kind, &item.Title, &item.Description, &item.Priority,
			&risk, &item.RequiredRoleFamily, &capabilitiesJSON, &criteriaJSON,
			&item.MaxAttempts, &item.Status, &issueID, &item.AssignedRole, &agentID, &item.Attempt,
		); err != nil {
			return nil, err
		}
		item.ID = util.UUIDToString(id)
		item.Kind = NodeKind(kind)
		item.Risk = RiskLevel(risk)
		item.MaterializedIssueID = util.UUIDToString(issueID)
		item.AssignedAgentID = util.UUIDToString(agentID)
		_ = json.Unmarshal(capabilitiesJSON, &item.RequiredCapabilities)
		_ = json.Unmarshal(criteriaJSON, &item.AcceptanceCriteria)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) BindNodeIssue(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey string,
	issueID pgtype.UUID,
	assignedRole string,
	assignedAgentID pgtype.UUID,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	if !issueID.Valid {
		return errors.New("materialized issue id is required")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node n
		SET materialized_issue_id = $4,
		    assigned_role = COALESCE(NULLIF($5, ''), n.assigned_role),
		    assigned_agent_id = CASE WHEN $6::uuid IS NULL THEN n.assigned_agent_id ELSE $6 END,
		    updated_at = now()
		FROM autonomous_project_plan p
		WHERE n.plan_id = p.id
		  AND n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND p.status IN ('active', 'blocked')
		  AND n.status NOT IN ('completed', 'cancelled')
		  AND (n.materialized_issue_id IS NULL OR n.materialized_issue_id = $4)
	`, workspaceID, projectID, nodeKey, issueID, assignedRole, assignedAgentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("project plan node %q cannot bind issue", nodeKey)
	}
	return nil
}

func (s *Store) ClaimReadyNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey string,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
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
	}
	if totalAttempts >= maxTotalAttempts {
		return ErrBudgetExceeded
	}

	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node n
		SET status = 'running',
		    started_at = COALESCE(n.started_at, now()),
		    attempt = n.attempt + 1,
		    blocked_reason = NULL,
		    updated_at = now()
		FROM autonomous_project_plan p
		WHERE n.plan_id = p.id
		  AND n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND n.materialized_issue_id IS NOT NULL
		  AND n.status = 'ready'
		  AND n.attempt < n.max_attempts
		  AND p.status IN ('active', 'blocked')
	`, workspaceID, projectID, nodeKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("project node %q is no longer ready for execution", nodeKey)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_budget
		SET total_attempts = total_attempts + 1, updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'active', updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'blocked'
	`, workspaceID, projectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReleaseNodeClaim(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey, reason string,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'ready',
		    attempt = GREATEST(attempt - 1, 0),
		    ready_at = COALESCE(ready_at, now()),
		    blocked_reason = NULLIF($4, ''),
		    updated_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND node_key = $3
		  AND status = 'running'
	`, workspaceID, projectID, nodeKey, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_budget
		SET total_attempts = GREATEST(total_attempts - 1, 0), updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetNodeBlocked(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey, category, reason string,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	switch category {
	case "dependency", "approval", "external_dependency", "technical_failure", "budget", "manual":
	default:
		return fmt.Errorf("unsupported project node block category %q", category)
	}
	if len(reason) > 2000 {
		reason = reason[:2000]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node n
		SET status = 'blocked',
		    blocked_category = $4,
		    blocked_reason = $5,
		    updated_at = now()
		FROM autonomous_project_plan p
		WHERE n.plan_id = p.id
		  AND n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND p.status IN ('active', 'blocked')
		  AND n.status NOT IN ('completed', 'cancelled')
	`, workspaceID, projectID, nodeKey, category, reason)
	return err
}

func (s *Store) ListBlockedNodes(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) ([]BlockedNode, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("project orchestration store is not configured")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.node_key, n.kind, n.title, n.description, n.priority,
		       n.risk_level, COALESCE(n.required_role_family, ''),
		       n.required_capabilities, n.acceptance_criteria, n.max_attempts,
		       n.status, n.materialized_issue_id,
		       COALESCE(n.assigned_role, ''), n.assigned_agent_id, n.attempt,
		       COALESCE(n.blocked_category, 'manual'), COALESCE(n.blocked_reason, '')
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.project_id = $2
		  AND p.status IN ('active', 'blocked')
		  AND n.status = 'blocked'
		ORDER BY n.priority DESC, n.updated_at ASC
	`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BlockedNode{}
	for rows.Next() {
		var item BlockedNode
		var id, issueID, agentID pgtype.UUID
		var kind, risk string
		var caps, criteria []byte
		if err := rows.Scan(
			&id, &item.Key, &kind, &item.Title, &item.Description, &item.Priority,
			&risk, &item.RequiredRoleFamily, &caps, &criteria, &item.MaxAttempts,
			&item.Status, &issueID, &item.AssignedRole, &agentID, &item.Attempt,
			&item.Category, &item.Reason,
		); err != nil {
			return nil, err
		}
		item.ID = util.UUIDToString(id)
		item.Kind = NodeKind(kind)
		item.Risk = RiskLevel(risk)
		item.MaterializedIssueID = util.UUIDToString(issueID)
		item.AssignedAgentID = util.UUIDToString(agentID)
		_ = json.Unmarshal(caps, &item.RequiredCapabilities)
		_ = json.Unmarshal(criteria, &item.AcceptanceCriteria)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ResumeBlockedNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey string,
) (bool, error) {
	if s == nil || s.pool == nil {
		return false, errors.New("project orchestration store is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var planID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT n.plan_id
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND n.status = 'blocked'
		  AND p.status IN ('active', 'blocked')
		ORDER BY p.revision DESC
		LIMIT 1
		FOR UPDATE OF n
	`, workspaceID, projectID, nodeKey).Scan(&planID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	var depsSatisfied bool
	if err := tx.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT 1
			FROM autonomous_project_plan_edge e
			JOIN autonomous_project_plan_node dep
			  ON dep.plan_id = e.plan_id
			 AND dep.node_key = e.from_node_key
			WHERE e.plan_id = $1
			  AND e.to_node_key = $2
			  AND (
			      (e.dependency_type = 'hard' AND dep.status <> 'completed')
			      OR (
			          e.dependency_type = 'artifact'
			          AND (
			              dep.status <> 'completed'
			              OR e.required_artifact_type IS NULL
			              OR NOT EXISTS (
			                  SELECT 1
			                  FROM autonomous_project_artifact a
			                  WHERE a.plan_id = e.plan_id
			                    AND a.node_id = dep.id
			                    AND a.artifact_type = e.required_artifact_type
			                    AND a.status = 'active'
			                    AND a.valid = TRUE
			                    AND a.artifact_revision = dep.spec_revision
			                    AND a.id = (
			                        SELECT latest.id
			                        FROM autonomous_project_artifact latest
			                        WHERE latest.plan_id = e.plan_id
			                          AND latest.node_id = dep.id
			                          AND latest.artifact_type = e.required_artifact_type
			                        ORDER BY latest.created_at DESC, latest.id DESC
			                        LIMIT 1
			                    )
			              )
			          )
			      )
			  )
		)
	`, planID, nodeKey).Scan(&depsSatisfied); err != nil {
		return false, err
	}
	if !depsSatisfied {
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'ready',
		    blocked_category = NULL,
		    blocked_reason = NULL,
		    ready_at = COALESCE(ready_at, now()),
		    updated_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND node_key = $3
		  AND status = 'blocked'
	`, workspaceID, projectID, nodeKey); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'active', updated_at = now()
		WHERE id = $1 AND status = 'blocked'
	`, planID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// ResumeNodeForWorkflowRetry re-attaches a successful task-level retry to the
// Project OS node that was already running before an execution failure blocked
// it. This is a continuation of the same logical node attempt, so it does not
// increment the node/project attempt budget.
//
// The method intentionally refuses policy-owned blocks (approval, dependency,
// external dependency, budget). A Retry button must not become a back door
// around deterministic project policy. If the issue is not materialized by
// Project OS, true is returned so the issue-level workflow can recover normally.
func (s *Store) ResumeNodeForWorkflowRetry(
	ctx context.Context,
	workspaceID, issueID pgtype.UUID,
) (bool, error) {
	if s == nil || s.pool == nil {
		return false, errors.New("project orchestration store is not configured")
	}
	if !workspaceID.Valid || !issueID.Valid {
		return true, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var nodeID, planID pgtype.UUID
	var status, category string
	err = tx.QueryRow(ctx, `
		SELECT n.id, n.plan_id, n.status, COALESCE(n.blocked_category, '')
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.materialized_issue_id = $2
		  AND p.status IN ('active', 'blocked')
		  AND n.status NOT IN ('completed', 'cancelled')
		ORDER BY p.revision DESC, n.updated_at DESC
		LIMIT 1
		FOR UPDATE OF n
	`, workspaceID, issueID).Scan(&nodeID, &planID, &status, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}

	switch status {
	case "running", "verification":
		return true, tx.Commit(ctx)
	case "ready":
		// A conductor cycle may already have cleared the technical block while
		// the manual retry was active. Attach that already-running task instead
		// of letting the scheduler dispatch a duplicate attempt.
	case "blocked":
		if category != "technical_failure" && category != "manual" {
			return false, tx.Commit(ctx)
		}
	default:
		return false, tx.Commit(ctx)
	}

	var depsSatisfied bool
	if err := tx.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT 1
			FROM autonomous_project_plan_edge e
			JOIN autonomous_project_plan_node dep
			  ON dep.plan_id = e.plan_id
			 AND dep.node_key = e.from_node_key
			JOIN autonomous_project_plan_node current
			  ON current.plan_id = e.plan_id
			 AND current.id = $1
			WHERE e.plan_id = $2
			  AND e.to_node_key = current.node_key
			  AND (
			      (e.dependency_type = 'hard' AND dep.status <> 'completed')
			      OR (
			          e.dependency_type = 'artifact'
			          AND (
			              dep.status <> 'completed'
			              OR e.required_artifact_type IS NULL
			              OR NOT EXISTS (
			                  SELECT 1
			                  FROM autonomous_project_artifact a
			                  WHERE a.plan_id = e.plan_id
			                    AND a.node_id = dep.id
			                    AND a.artifact_type = e.required_artifact_type
			                    AND a.status = 'active'
			                    AND a.valid = TRUE
			                    AND a.artifact_revision = dep.spec_revision
			                    AND a.id = (
			                        SELECT latest.id
			                        FROM autonomous_project_artifact latest
			                        WHERE latest.plan_id = e.plan_id
			                          AND latest.node_id = dep.id
			                          AND latest.artifact_type = e.required_artifact_type
			                        ORDER BY latest.created_at DESC, latest.id DESC
			                        LIMIT 1
			                    )
			              )
			          )
			      )
			  )
		)
	`, nodeID, planID).Scan(&depsSatisfied); err != nil {
		return false, err
	}
	if !depsSatisfied {
		return false, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'running',
		    started_at = COALESCE(started_at, now()),
		    blocked_category = NULL,
		    blocked_reason = NULL,
		    updated_at = now()
		WHERE id = $1
	`, nodeID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'active', updated_at = now()
		WHERE id = $1 AND status = 'blocked'
	`, planID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) AccountTaskUsage(
	ctx context.Context,
	workspaceID, projectID, taskID pgtype.UUID,
	tokens, runtimeSeconds, costMicrounits int64,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid || !taskID.Valid {
		return nil
	}
	if tokens < 0 || runtimeSeconds < 0 || costMicrounits < 0 {
		return errors.New("project usage deltas cannot be negative")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_usage_accounting (
			task_id, workspace_id, project_id, tokens, runtime_seconds, cost_microunits
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id) DO NOTHING
	`, taskID, workspaceID, projectID, tokens, runtimeSeconds, costMicrounits)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	tag, err = tx.Exec(ctx, `
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
		SELECT n.id, n.project_id, n.node_key, n.attempt, n.max_attempts
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.materialized_issue_id = $2
		  AND n.status IN ('running', 'verification', 'ready')
		  AND p.status IN ('active', 'blocked')
		ORDER BY p.revision DESC, n.updated_at DESC
		LIMIT 1
		FOR UPDATE OF n
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
		    blocked_category = CASE WHEN $3 = 'blocked' THEN 'technical_failure' ELSE NULL END,
		    blocked_reason = $4,
		    ready_at = CASE WHEN $3 = 'ready' THEN now() ELSE ready_at END,
		    updated_at = now()
		WHERE id = $1 AND workspace_id = $2
	`, nodeID, workspaceID, nextStatus, reason); err != nil {
		return "", "", pgtype.UUID{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", pgtype.UUID{}, err
	}
	return disposition, nodeKey, projectID, nil
}

func (s *Store) ResumePlanAfterNodeRetry(
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
	var nodeID, projectID, planID pgtype.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'completed',
		    completed_at = COALESCE(completed_at, now()),
		    blocked_category = NULL,
		    blocked_reason = NULL,
		    updated_at = now()
		WHERE id = (
			SELECT n.id
			FROM autonomous_project_plan_node n
			JOIN autonomous_project_plan p ON p.id = n.plan_id
			WHERE n.workspace_id = $1
			  AND n.materialized_issue_id = $2
			  AND n.status NOT IN ('completed', 'cancelled')
			  AND p.status IN ('active', 'blocked')
			ORDER BY p.revision DESC, n.updated_at DESC
			LIMIT 1
		)
		RETURNING id, project_id, plan_id
	`, workspaceID, issueID).Scan(&nodeID, &projectID, &planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
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
		UPDATE autonomous_project_escalation
		SET status = 'resolved',
		    resolution = jsonb_build_object(
		        'decision', 'auto_resolved',
		        'reason', 'node_completed'
		    ),
		    resolved_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND node_id = $3
		  AND status IN ('open','acknowledged')
	`, workspaceID, projectID, nodeID); err != nil {
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
		SELECT from_node_key, to_node_key, dependency_type, COALESCE(required_artifact_type, '')
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
		if err := edgeRows.Scan(&edge.From, &edge.To, &typ, &edge.RequiredArtifactType); err != nil {
			return nil, nil, err
		}
		edge.Type = DependencyType(typ)
		edges = append(edges, edge)
	}
	return nodes, edges, edgeRows.Err()
}

func (s *Store) StartExternalNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey string,
) (pgtype.UUID, pgtype.UUID, error) {
	if s == nil || s.pool == nil {
		return pgtype.UUID{}, pgtype.UUID{}, errors.New("project orchestration store is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	defer tx.Rollback(ctx)

	var maxTotal, total int
	if err := tx.QueryRow(ctx, `
		SELECT max_total_attempts, total_attempts
		FROM autonomous_project_budget
		WHERE workspace_id = $1 AND project_id = $2
		FOR UPDATE
	`, workspaceID, projectID).Scan(&maxTotal, &total); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if total >= maxTotal {
		return pgtype.UUID{}, pgtype.UUID{}, ErrBudgetExceeded
	}

	var nodeID, planID pgtype.UUID
	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node n
		SET status = 'running',
		    attempt = attempt + 1,
		    started_at = COALESCE(started_at, now()),
		    blocked_reason = NULL,
		    updated_at = now()
		FROM autonomous_project_plan p
		WHERE n.plan_id = p.id
		  AND n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND n.status = 'ready'
		  AND n.attempt < n.max_attempts
		  AND p.status IN ('active', 'blocked')
	`, workspaceID, projectID, nodeKey)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if tag.RowsAffected() == 0 {
		return pgtype.UUID{}, pgtype.UUID{}, errors.New("project node is no longer ready")
	}
	if err := tx.QueryRow(ctx, `
		SELECT id, plan_id
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1 AND project_id = $2 AND node_key = $3
		ORDER BY updated_at DESC
		LIMIT 1
	`, workspaceID, projectID, nodeKey).Scan(&nodeID, &planID); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_budget
		SET total_attempts = total_attempts + 1, updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'active', updated_at = now()
		WHERE id = $1 AND status = 'blocked'
	`, planID); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	return nodeID, planID, nil
}

func (s *Store) CompleteExternalNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey string,
) error {
	var nodeID pgtype.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'completed',
		    completed_at = COALESCE(completed_at, now()),
		    blocked_category = NULL,
		    blocked_reason = NULL,
		    updated_at = now()
		WHERE id = (
			SELECT n.id
			FROM autonomous_project_plan_node n
			JOIN autonomous_project_plan p ON p.id = n.plan_id
			WHERE n.workspace_id = $1
			  AND n.project_id = $2
			  AND n.node_key = $3
			  AND n.status = 'running'
			  AND p.status IN ('active','blocked')
			ORDER BY p.revision DESC
			LIMIT 1
		)
		RETURNING id
	`, workspaceID, projectID, nodeKey).Scan(&nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_escalation
		SET status = 'resolved',
		    resolution = jsonb_build_object(
		        'decision', 'auto_resolved',
		        'reason', 'node_completed'
		    ),
		    resolved_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND node_id = $3
		  AND status IN ('open','acknowledged')
	`, workspaceID, projectID, nodeID); err != nil {
		return err
	}
	if err := s.RefreshReady(ctx, workspaceID, projectID); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE autonomous_project_plan p
		SET status = 'completed', updated_at = now()
		WHERE p.workspace_id = $1
		  AND p.project_id = $2
		  AND p.status IN ('active', 'blocked')
		  AND NOT EXISTS (
			SELECT 1
			FROM autonomous_project_plan_node n
			WHERE n.plan_id = p.id
			  AND n.status NOT IN ('completed', 'cancelled')
		  )
	`, workspaceID, projectID)
	return err
}

func (s *Store) FailExternalNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey, reason string,
) (FailureDisposition, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var attempt, maxAttempts int
	if err := tx.QueryRow(ctx, `
		SELECT attempt, max_attempts
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1 AND project_id = $2 AND node_key = $3
		FOR UPDATE
	`, workspaceID, projectID, nodeKey).Scan(&attempt, &maxAttempts); err != nil {
		return "", err
	}
	disposition := FailureRetry
	status := "ready"
	if attempt >= maxAttempts {
		disposition = FailureBlocked
		status = "blocked"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = $4,
		    blocked_category = CASE WHEN $4 = 'blocked' THEN 'external_dependency' ELSE NULL END,
		    blocked_reason = $5,
		    ready_at = CASE WHEN $4 = 'ready' THEN now() ELSE ready_at END,
		    updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2 AND node_key = $3
	`, workspaceID, projectID, nodeKey, status, reason); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return disposition, nil
}

func (s *Store) ResetDeletedIssue(
	ctx context.Context,
	workspaceID, issueID pgtype.UUID,
) error {
	if s == nil || s.pool == nil || !workspaceID.Valid || !issueID.Valid {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET materialized_issue_id = NULL,
		    status = CASE
		      WHEN status IN ('running', 'verification') THEN 'ready'
		      ELSE status
		    END,
		    ready_at = CASE
		      WHEN status IN ('running', 'verification') THEN now()
		      ELSE ready_at
		    END,
		    blocked_reason = CASE
		      WHEN status IN ('running', 'verification') THEN 'materialized issue was deleted'
		      ELSE blocked_reason
		    END,
		    updated_at = now()
		WHERE id = (
			SELECT n.id
			FROM autonomous_project_plan_node n
			JOIN autonomous_project_plan p ON p.id = n.plan_id
			WHERE n.workspace_id = $1
			  AND n.materialized_issue_id = $2
			  AND n.status NOT IN ('completed', 'cancelled')
			  AND p.status IN ('active', 'blocked')
			ORDER BY p.revision DESC, n.updated_at DESC
			LIMIT 1
		)
	`, workspaceID, issueID)
	return err
}

func (s *Store) CleanupProject(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) error {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	statements := []string{
		`DELETE FROM autonomous_project_incident WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_deployment WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_escalation WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_quality_gate_run WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_artifact WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_brain_learning_job WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_brain_config WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_brain_entry WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_plan_edge WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_plan_node WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_plan WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_usage_accounting WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_budget WHERE workspace_id = $1 AND project_id = $2`,
		`DELETE FROM autonomous_project_bootstrap WHERE workspace_id = $1 AND project_id = $2`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, workspaceID, projectID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) CleanupWorkspace(ctx context.Context, workspaceID pgtype.UUID) error {
	if s == nil || s.pool == nil || !workspaceID.Valid {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	statements := []string{
		`DELETE FROM autonomous_project_incident WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_deployment WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_escalation WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_quality_gate_run WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_artifact WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_brain_learning_job WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_brain_config WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_brain_entry WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_plan_edge WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_plan_node WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_plan WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_usage_accounting WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_budget WHERE workspace_id = $1`,
		`DELETE FROM autonomous_project_bootstrap WHERE workspace_id = $1`,
		`DELETE FROM autonomous_agent_performance WHERE workspace_id = $1`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, workspaceID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
