package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const (
	projectLeaderAgentName   = "Project Manager"
	projectLeaderSessionKind = "autonomous_project_leader"
)

// MikaProjectLeaderExecutor uses the provisioned product_manager role as the
// project's durable Project Manager. The session is shared by project members.
type MikaProjectLeaderExecutor struct {
	base *MikaProjectPlanExecutor
}

func NewMikaProjectLeaderExecutor(pool *pgxpool.Pool, taskSvc *service.TaskService) *MikaProjectLeaderExecutor {
	return &MikaProjectLeaderExecutor{base: NewMikaProjectPlanExecutor(pool, taskSvc)}
}

// EnsureProjectLeaderSession returns the shared per-project manager chat.
func (e *MikaProjectLeaderExecutor) EnsureProjectLeaderSession(ctx context.Context, workspaceID, projectID, creatorID pgtype.UUID) (db.ChatSession, db.Agent, error) {
	if e == nil || e.base == nil || e.base.pool == nil || e.base.taskSvc == nil {
		return db.ChatSession{}, db.Agent{}, errors.New("project leader runtime is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid || !creatorID.Valid {
		return db.ChatSession{}, db.Agent{}, errors.New("workspace, project and creator are required")
	}
	carrier, _, err := e.ensureCarrier(ctx, workspaceID, projectID)
	if err != nil {
		return db.ChatSession{}, db.Agent{}, err
	}

	tx, err := e.base.pool.Begin(ctx)
	if err != nil {
		return db.ChatSession{}, db.Agent{}, fmt.Errorf("begin project leader session: %w", err)
	}
	defer tx.Rollback(ctx)
	lockKey := "autonomous-project-manager-session:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return db.ChatSession{}, db.Agent{}, fmt.Errorf("lock project leader session: %w", err)
	}
	var sessionID, sessionAgentID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, agent_id FROM chat_session
		WHERE workspace_id=$1 AND project_id=$2
		  AND session_kind=$3 AND status='active'
		FOR UPDATE
	`, workspaceID, projectID, projectLeaderSessionKind).Scan(&sessionID, &sessionAgentID)
	if err == nil && sessionAgentID != carrier.ID {
		_, err = tx.Exec(ctx, `
			UPDATE chat_session
			SET agent_id=$2, runtime_id=$3, title='Project Manager', updated_at=now()
			WHERE id=$1
		`, sessionID, carrier.ID, carrier.RuntimeID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id, is_agent_intro, project_id, id, session_kind, explicitly_created_at)
			VALUES ($1,$2,$3,'Project Manager',$4,FALSE,$5,$6,$7,now())
			RETURNING id
		`, workspaceID, carrier.ID, creatorID, carrier.RuntimeID, projectID, dbid.NewV7(), projectLeaderSessionKind).Scan(&sessionID)
	}
	if err != nil {
		return db.ChatSession{}, db.Agent{}, fmt.Errorf("ensure project leader session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ChatSession{}, db.Agent{}, err
	}
	session, err := e.base.taskSvc.Queries.GetChatSession(ctx, sessionID)
	if err != nil {
		return db.ChatSession{}, db.Agent{}, err
	}
	return session, carrier, nil
}

func (e *MikaProjectLeaderExecutor) ensureCarrier(ctx context.Context, workspaceID, projectID pgtype.UUID) (db.Agent, db.AgentRuntime, error) {
	return e.base.ensureProjectManagerCarrier(ctx, workspaceID, projectID)
}

// ProcessCompletion is called after the normal task completion transaction.
// A malformed or non-proposal answer is visible in chat but cannot mutate the
// project; a valid answer becomes an approval_required change request.
func (e *MikaProjectLeaderExecutor) ProcessCompletion(ctx context.Context, taskID pgtype.UUID, result []byte) error {
	if e == nil || e.base == nil || e.base.pool == nil || !taskID.Valid {
		return nil
	}
	var sessionID, workspaceID, projectID, agentID pgtype.UUID
	var output string
	err := e.base.pool.QueryRow(ctx, `
		SELECT t.chat_session_id, s.workspace_id, s.project_id, t.agent_id
		FROM agent_task_queue t
		JOIN chat_session s ON s.id=t.chat_session_id
		JOIN agent a ON a.id=t.agent_id
		WHERE t.id=$1 AND s.session_kind=$2
	`, taskID, projectLeaderSessionKind).Scan(&sessionID, &workspaceID, &projectID, &agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var envelope struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		return fmt.Errorf("decode project leader completion: %w", err)
	}
	output = strings.TrimSpace(envelope.Output)
	if output == "" {
		return nil
	}
	var response projectorchestration.ProjectLeaderResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return fmt.Errorf("project leader returned invalid JSON: %w", err)
	}
	if err := projectorchestration.ValidateProjectLeaderResponse(response); err != nil {
		return err
	}
	if response.Proposal == nil {
		return nil
	}
	var model pgtype.Text
	if err := e.base.pool.QueryRow(ctx, `SELECT model FROM agent WHERE id=$1`, agentID).Scan(&model); err != nil {
		return err
	}
	_, err = projectorchestration.NewStore(e.base.pool).ApplyProjectLeaderProposal(ctx, workspaceID, projectID, *response.Proposal, projectLeaderAgentName, model.String)
	return err
}

// ApproveChange atomically records approval and enqueues the existing durable
// plan-mutation worker. It never touches issue/task/agent rows.
func (e *MikaProjectLeaderExecutor) ApproveChange(ctx context.Context, workspaceID, projectID, changeID pgtype.UUID, idempotencyKey string) error {
	if e == nil || e.base == nil || e.base.pool == nil {
		return errors.New("project leader runtime is not configured")
	}
	change, err := projectorchestration.NewStore(e.base.pool).LoadChangeRequest(ctx, changeID)
	if err != nil {
		return err
	}
	if change.WorkspaceID != util.UUIDToString(workspaceID) || change.ProjectID != util.UUIDToString(projectID) {
		return errors.New("change request does not belong to project")
	}
	if change.State == projectorchestration.ChangeApplied {
		return nil
	}
	if change.State != projectorchestration.ChangeApprovalRequired {
		return fmt.Errorf("change request is not awaiting approval: %s", change.State)
	}
	var proposal projectorchestration.ProjectChangeProposal
	if err := json.Unmarshal(change.Proposal, &proposal); err != nil {
		return err
	}
	current, ok, err := projectorchestration.NewStore(e.base.pool).LoadLatestPlan(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("project has no plan")
	}
	if change.BasePlanID != "" && change.BasePlanID != current.ID {
		return fmt.Errorf("stale project leader proposal: base plan %s, current plan %s", change.BasePlanID, current.ID)
	}
	if proposal.BasePlanRevision != current.Revision {
		return fmt.Errorf("stale project leader proposal: base revision %d, current revision %d", proposal.BasePlanRevision, current.Revision)
	}
	nodes, err := projectorchestration.NewStore(e.base.pool).LoadLogicalPlanNodes(ctx, workspaceID, projectID, current.ID)
	if err != nil {
		return err
	}
	operations, err := projectorchestration.CompileProjectLeaderProposal(proposal, nodes)
	if err != nil {
		return err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = "project-leader-apply:" + change.ID
	}
	raw, err := json.Marshal(planMutationJobPayload{ChangeRequestID: change.ID, Operations: operations, PlannerName: projectLeaderAgentName})
	if err != nil {
		return err
	}
	tx, err := e.base.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "autonomous-project-leader-apply:"+util.UUIDToString(projectID)); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM autonomous_project_change_request WHERE id=$1 AND workspace_id=$2 AND project_id=$3 FOR UPDATE`, changeID, workspaceID, projectID).Scan(&state); err != nil {
		return err
	}
	if state == string(projectorchestration.ChangeApplied) {
		return tx.Commit(ctx)
	}
	if state != string(projectorchestration.ChangeApprovalRequired) && state != string(projectorchestration.ChangeApproved) {
		return fmt.Errorf("change request cannot be approved from %s", state)
	}
	if _, err := tx.Exec(ctx, `UPDATE autonomous_project_change_request SET state='approved',updated_at=now() WHERE id=$1`, changeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO autonomous_control_plane_job(workspace_id,project_id,job_type,idempotency_key,payload,status,priority,max_attempts,available_at) VALUES($1,$2,'plan_mutation',$3,$4,'pending',70,3,now()) ON CONFLICT(workspace_id,project_id,job_type,idempotency_key) DO NOTHING`, workspaceID, projectID, idempotencyKey, raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
