package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const postgresOperationTimeout = 5 * time.Second

// PostgresStore is the production durability boundary for autonomous workflows.
// Event deduplication, revision-checked state transition and pending-action
// creation are committed in one transaction by Apply.
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) GetOrCreateRun(event Event, definition Definition) (Run, error) {
	if s == nil || s.pool == nil {
		return Run{}, errors.New("workflow postgres store is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), postgresOperationTimeout)
	defer cancel()

	workspaceID, err := parseUUID(event.WorkspaceID)
	if err != nil {
		return Run{}, fmt.Errorf("parse workspace id: %w", err)
	}
	issueID, err := parseUUID(event.IssueID)
	if err != nil {
		return Run{}, fmt.Errorf("parse issue id: %w", err)
	}

	projectID, err := optionalUUID(event.ProjectID)
	if err != nil {
		return Run{}, fmt.Errorf("parse project id: %w", err)
	}
	ownerAgentID, err := optionalUUID(event.OwnerAgentID)
	if err != nil {
		return Run{}, fmt.Errorf("parse owner agent id: %w", err)
	}
	reviewerAgentID, err := optionalUUID(event.ReviewerAgentID)
	if err != nil {
		return Run{}, fmt.Errorf("parse reviewer agent id: %w", err)
	}
	accountableUserID, err := optionalUUID(event.AccountableUserID)
	if err != nil {
		return Run{}, fmt.Errorf("parse accountable user id: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO autonomous_workflow_run (
			id, workflow_name, workflow_version, workspace_id, project_id, issue_id,
			state, revision, owner_agent_id, reviewer_agent_id, accountable_user_id
		)
		VALUES (
			gen_random_uuid(), $1, $2, $3, $4, $5,
			$6, 1, $7, $8, $9
		)
		ON CONFLICT DO NOTHING
	`, definition.Name, definition.Version, workspaceID, nullableUUID(projectID), issueID,
		definition.InitialState, nullableUUID(ownerAgentID), nullableUUID(reviewerAgentID), nullableUUID(accountableUserID))
	if err != nil {
		return Run{}, fmt.Errorf("create workflow run: %w", err)
	}

	// Fill metadata that may have been unavailable on the event that won the
	// create race. Existing non-NULL actors stay stable for the lifetime of the
	// workflow.
	_, err = s.pool.Exec(ctx, `
		UPDATE autonomous_workflow_run
		SET project_id = COALESCE(project_id, $4),
		    owner_agent_id = COALESCE(owner_agent_id, $5),
		    reviewer_agent_id = COALESCE(reviewer_agent_id, $6),
		    accountable_user_id = COALESCE(accountable_user_id, $7),
		    updated_at = now()
		WHERE workflow_name = $1
		  AND workspace_id = $2
		  AND issue_id = $3
	`, definition.Name, workspaceID, issueID, nullableUUID(projectID), nullableUUID(ownerAgentID), nullableUUID(reviewerAgentID), nullableUUID(accountableUserID))
	if err != nil {
		return Run{}, fmt.Errorf("hydrate workflow run actors: %w", err)
	}

	return s.findRun(ctx, definition.Name, workspaceID, issueID)
}

func (s *PostgresStore) Apply(request ApplyRequest) (ApplyResult, error) {
	if s == nil || s.pool == nil {
		return ApplyResult{}, errors.New("workflow postgres store is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), postgresOperationTimeout)
	defer cancel()

	runID, err := parseUUID(request.RunID)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("parse run id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("begin workflow transition: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO autonomous_workflow_processed_event (event_id, run_id, event_type)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id) DO NOTHING
	`, request.EventID, runID, request.EventType)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("record workflow event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		run, err := scanRun(tx.QueryRow(ctx, runByIDSQL, runID))
		if err != nil {
			return ApplyResult{}, fmt.Errorf("load duplicate workflow event run: %w", err)
		}
		return ApplyResult{Run: run, Duplicate: true}, nil
	}

	run, err := scanRun(tx.QueryRow(ctx, `
		UPDATE autonomous_workflow_run
		SET state = $2,
		    revision = revision + 1,
		    review_cycles = review_cycles + CASE WHEN $3 = 'in_review' AND $2 = 'in_progress' THEN 1 ELSE 0 END,
		    updated_at = now()
		WHERE id = $1
		  AND revision = $4
		  AND state = $3
		RETURNING
			id, workflow_name, workflow_version, workspace_id, project_id, issue_id,
			state, revision, owner_agent_id, reviewer_agent_id, accountable_user_id,
			review_cycles, updated_at
	`, runID, request.To, request.From, request.ExpectedRevision))
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplyResult{}, ErrRevisionConflict
	}
	if err != nil {
		return ApplyResult{}, fmt.Errorf("transition workflow run: %w", err)
	}

	for position, action := range request.Actions {
		params, err := json.Marshal(action.Params)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("encode workflow action params: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_workflow_action (
				id, run_id, event_id, position, action_type, params,
				status, attempts, max_attempts, available_at
			)
			VALUES (
				gen_random_uuid(), $1, $2, $3, $4, $5,
				'pending', 0, 5, now()
			)
		`, runID, request.EventID, position, action.Type, params); err != nil {
			return ApplyResult{}, fmt.Errorf("enqueue workflow action: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ApplyResult{}, fmt.Errorf("commit workflow transition: %w", err)
	}
	return ApplyResult{Run: run, Applied: true}, nil
}

const runColumns = `
	id, workflow_name, workflow_version, workspace_id, project_id, issue_id,
	state, revision, owner_agent_id, reviewer_agent_id, accountable_user_id,
	review_cycles, updated_at
`

const runByIDSQL = `
	SELECT ` + runColumns + `
	FROM autonomous_workflow_run
	WHERE id = $1
`

func (s *PostgresStore) FindRun(ctx context.Context, workflowName, workspaceID, issueID string) (Run, bool, error) {
	ws, err := parseUUID(workspaceID)
	if err != nil {
		return Run{}, false, err
	}
	issue, err := parseUUID(issueID)
	if err != nil {
		return Run{}, false, err
	}
	run, err := s.findRun(ctx, workflowName, ws, issue)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, nil
	}
	return run, err == nil, err
}

func (s *PostgresStore) findRun(ctx context.Context, workflowName string, workspaceID, issueID pgtype.UUID) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `
		SELECT `+runColumns+`
		FROM autonomous_workflow_run
		WHERE workflow_name = $1
		  AND workspace_id = $2
		  AND issue_id = $3
	`, workflowName, workspaceID, issueID))
}

func (s *PostgresStore) GetRun(ctx context.Context, runID string) (Run, error) {
	id, err := parseUUID(runID)
	if err != nil {
		return Run{}, err
	}
	return scanRun(s.pool.QueryRow(ctx, runByIDSQL, id))
}

// ListActiveRuns returns bounded non-terminal runs for restart reconciliation.
func (s *PostgresStore) ListActiveRuns(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, "SELECT "+runColumns+" FROM autonomous_workflow_run WHERE state IN ('in_progress', 'in_review') ORDER BY updated_at ASC LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("list active workflow runs: %w", err)
	}
	defer rows.Close()

	out := make([]Run, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan active workflow run: %w", err)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active workflow runs: %w", err)
	}
	return out, nil
}

// ClaimPendingAction leases one runnable action. Later actions in the same
// transition stay blocked until every earlier position is completed, so a
// reviewer cannot start before the issue has actually moved to In Review.
func (s *PostgresStore) ClaimPendingAction(ctx context.Context, lease time.Duration) (*PendingAction, error) {
	if lease <= 0 {
		lease = 30 * time.Second
	}
	row := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT a.id
			FROM autonomous_workflow_action a
			WHERE (
				(a.status = 'pending' AND a.available_at <= now())
				OR
				(a.status = 'running' AND a.lease_expires_at < now())
			)
			AND NOT EXISTS (
				SELECT 1
				FROM autonomous_workflow_action prior
				WHERE prior.run_id = a.run_id
				  AND prior.event_id = a.event_id
				  AND prior.position < a.position
				  AND prior.status <> 'completed'
			)
			ORDER BY a.available_at ASC, a.created_at ASC, a.position ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE autonomous_workflow_action a
		SET status = 'running',
		    attempts = attempts + 1,
		    lease_token = gen_random_uuid(),
		    lease_expires_at = now() + make_interval(secs => $1::double precision),
		    updated_at = now()
		FROM candidate
		WHERE a.id = candidate.id
		RETURNING
			a.id, a.run_id, a.event_id, a.position, a.action_type, a.params,
			a.status, a.attempts, a.max_attempts, a.lease_token
	`, lease.Seconds())

	var (
		id, runID, leaseToken pgtype.UUID
		eventID, actionType, status string
		position, attempts, maxAttempts int
		params []byte
	)
	if err := row.Scan(&id, &runID, &eventID, &position, &actionType, &params, &status, &attempts, &maxAttempts, &leaseToken); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claim workflow action: %w", err)
	}
	var actionParams map[string]string
	if len(params) > 0 {
		if err := json.Unmarshal(params, &actionParams); err != nil {
			return nil, fmt.Errorf("decode workflow action params: %w", err)
		}
	}
	return &PendingAction{
		ID:          uuidString(id),
		RunID:       uuidString(runID),
		EventID:     eventID,
		Position:    position,
		Action:      Action{Type: actionType, Params: actionParams},
		Status:      status,
		Attempts:    attempts,
		MaxAttempts: maxAttempts,
		LeaseToken:  uuidString(leaseToken),
	}, nil
}

func (s *PostgresStore) CompleteAction(ctx context.Context, actionID, leaseToken string) error {
	id, err := parseUUID(actionID)
	if err != nil {
		return err
	}
	lease, err := parseUUID(leaseToken)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_workflow_action
		SET status = 'completed',
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    last_error = NULL,
		    updated_at = now()
		WHERE id = $1
		  AND status = 'running'
		  AND lease_token = $2
	`, id, lease)
	if err != nil {
		return fmt.Errorf("complete workflow action: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("workflow action lease was lost before completion")
	}
	return nil
}

func (s *PostgresStore) FailAction(ctx context.Context, action PendingAction, cause error) error {
	id, err := parseUUID(action.ID)
	if err != nil {
		return err
	}
	lease, err := parseUUID(action.LeaseToken)
	if err != nil {
		return err
	}
	message := "workflow action failed"
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 2000 {
		message = message[:2000]
	}

	if action.Attempts >= action.MaxAttempts {
		_, err = s.pool.Exec(ctx, `
			UPDATE autonomous_workflow_action
			SET status = 'failed',
			    lease_token = NULL,
			    lease_expires_at = NULL,
			    last_error = $3,
			    updated_at = now()
			WHERE id = $1
			  AND status = 'running'
			  AND lease_token = $2
		`, id, lease, message)
		return err
	}

	backoff := time.Second << min(action.Attempts-1, 6)
	_, err = s.pool.Exec(ctx, `
		UPDATE autonomous_workflow_action
		SET status = 'pending',
		    available_at = now() + make_interval(secs => $3::double precision),
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    last_error = $4,
		    updated_at = now()
		WHERE id = $1
		  AND status = 'running'
		  AND lease_token = $2
	`, id, lease, backoff.Seconds(), message)
	return err
}

func scanRun(row pgx.Row) (Run, error) {
	var (
		id, workspaceID, projectID, issueID pgtype.UUID
		ownerAgentID, reviewerAgentID, accountableUserID pgtype.UUID
		updatedAt pgtype.Timestamptz
		run Run
	)
	if err := row.Scan(
		&id, &run.WorkflowName, &run.Version, &workspaceID, &projectID, &issueID,
		&run.State, &run.Revision, &ownerAgentID, &reviewerAgentID, &accountableUserID,
		&run.ReviewCycles, &updatedAt,
	); err != nil {
		return Run{}, err
	}
	run.ID = uuidString(id)
	run.WorkspaceID = uuidString(workspaceID)
	run.ProjectID = uuidString(projectID)
	run.IssueID = uuidString(issueID)
	run.OwnerAgentID = uuidString(ownerAgentID)
	run.ReviewerAgentID = uuidString(reviewerAgentID)
	run.AccountableUserID = uuidString(accountableUserID)
	if updatedAt.Valid {
		run.UpdatedAt = updatedAt.Time
	}
	return run, nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	var out pgtype.UUID
	if err := out.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	if !out.Valid {
		return pgtype.UUID{}, errors.New("uuid is required")
	}
	return out, nil
}

func optionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return parseUUID(value)
}

func nullableUUID(value pgtype.UUID) any {
	if !value.Valid {
		return nil
	}
	return value
}

func uuidString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}
