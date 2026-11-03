package workflowruntime

import (
	"bytes"
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
	store := projectorchestration.NewStore(e.base.pool)
	if response.SpecificationProposal != nil {
		_, err := store.RecordProjectLeaderSpecificationProposal(ctx, workspaceID, projectID, *response.SpecificationProposal)
		return err
	}
	if response.Proposal == nil {
		return nil
	}
	var model pgtype.Text
	if err := e.base.pool.QueryRow(ctx, `SELECT model FROM agent WHERE id=$1`, agentID).Scan(&model); err != nil {
		return err
	}
	_, err = store.ApplyProjectLeaderProposal(ctx, workspaceID, projectID, *response.Proposal, projectLeaderAgentName, model.String)
	return err
}

// ApproveChange atomically records approval and enqueues the existing durable
// plan-mutation worker. It never touches issue/task/agent rows.
func (e *MikaProjectLeaderExecutor) ApproveChange(ctx context.Context, workspaceID, projectID, changeID, actorID pgtype.UUID, idempotencyKey string) error {
	if e == nil || e.base == nil || e.base.pool == nil {
		return errors.New("project leader runtime is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid || !changeID.Valid || !actorID.Valid {
		return errors.New("workspace, project, change request and actor are required")
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
	var actorRole string
	if err := tx.QueryRow(ctx, `
		SELECT role FROM member
		WHERE workspace_id=$1 AND user_id=$2
		FOR SHARE
	`, workspaceID, actorID).Scan(&actorRole); err != nil {
		return fmt.Errorf("load project leader approver membership: %w", err)
	}
	if actorRole != "owner" && actorRole != "admin" {
		return errors.New("workspace owner or admin required to approve project leader change")
	}
	var scopedProjectID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM project
		WHERE workspace_id=$1 AND id=$2
		FOR SHARE
	`, workspaceID, projectID).Scan(&scopedProjectID); err != nil {
		return fmt.Errorf("load project scope for project leader approval: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "autonomous-project-leader-apply:"+util.UUIDToString(projectID)); err != nil {
		return err
	}
	planLockKey := "autonomous-project-plan:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", planLockKey); err != nil {
		return fmt.Errorf("lock project plan for change approval: %w", err)
	}
	var state string
	var lockedProposal []byte
	var lockedBasePlanID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT state,proposal,base_plan_id
		FROM autonomous_project_change_request
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
		FOR UPDATE
	`, changeID, workspaceID, projectID).Scan(&state, &lockedProposal, &lockedBasePlanID); err != nil {
		return err
	}
	if state == string(projectorchestration.ChangeApplied) {
		return tx.Commit(ctx)
	}
	if state != string(projectorchestration.ChangeApprovalRequired) && state != string(projectorchestration.ChangeApproved) {
		return fmt.Errorf("change request cannot be approved from %s", state)
	}
	lockedBasePlanText := ""
	if lockedBasePlanID.Valid {
		lockedBasePlanText = util.UUIDToString(lockedBasePlanID)
	}
	if !bytes.Equal(lockedProposal, change.Proposal) || lockedBasePlanText != change.BasePlanID {
		return errors.New("stale project leader proposal: change request changed during approval")
	}
	currentPlanID := util.MustParseUUID(current.ID)
	var lockedPlanRevision int64
	var storedPlanDigest string
	if err := tx.QueryRow(ctx, `
		SELECT revision,plan_digest FROM autonomous_project_plan
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
		FOR UPDATE
	`, currentPlanID, workspaceID, projectID).Scan(&lockedPlanRevision, &storedPlanDigest); err != nil {
		return fmt.Errorf("lock project leader base plan: %w", err)
	}
	if lockedPlanRevision != current.Revision || storedPlanDigest != current.PlanDigest {
		return errors.New("stale project leader proposal: base plan changed during approval")
	}
	if err := lockProjectLeaderPlanRows(ctx, tx, currentPlanID, nodes); err != nil {
		return err
	}
	var currentPlanDigest string
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_plan_digest($1)`, currentPlanID).Scan(&currentPlanDigest); err != nil {
		return fmt.Errorf("calculate locked project leader base plan digest: %w", err)
	}
	if currentPlanDigest != storedPlanDigest {
		return errors.New("stale project leader proposal: base plan content changed during approval")
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('multica.approval_actor_type','human',true),set_config('multica.approval_actor_ref',$1,true)`, util.UUIDToString(actorID)); err != nil {
		return fmt.Errorf("set project leader approval actor: %w", err)
	}
	var proposalDigest string
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_change_proposal_digest($1)`, changeID).Scan(&proposalDigest); err != nil {
		return fmt.Errorf("calculate project leader proposal digest: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE autonomous_project_change_request SET state='approved',updated_at=now() WHERE id=$1`, changeID); err != nil {
		return err
	}
	if err := projectorchestration.RecordApprovalTx(ctx, tx, projectorchestration.ApprovalEvent{
		WorkspaceID: workspaceID, ProjectID: projectID,
		IdempotencyKey: "change-request-approve:" + util.UUIDToString(changeID),
		SubjectType:    "change_request", SubjectID: util.UUIDToString(changeID), SubjectDigest: proposalDigest,
		Decision: "approved", ActorType: "human", ActorID: actorID, ActorDisplay: util.UUIDToString(actorID),
		Rationale:      "Approved the current project change proposal after base revision validation",
		SourceEndpoint: "/api/projects/:id/autonomous/change-requests/:changeRequestId/approve", Origin: "local",
	}); err != nil {
		return fmt.Errorf("record project leader change approval: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO autonomous_control_plane_job(workspace_id,project_id,job_type,idempotency_key,payload,status,priority,max_attempts,available_at) VALUES($1,$2,'plan_mutation',$3,$4,'pending',70,3,now()) ON CONFLICT(workspace_id,project_id,job_type,idempotency_key) DO NOTHING`, workspaceID, projectID, idempotencyKey, raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockProjectLeaderPlanRows(ctx context.Context, tx pgx.Tx, planID pgtype.UUID, loaded []projectorchestration.LogicalPlanNode) error {
	rows, err := tx.Query(ctx, `
		SELECT logical_node_id::text,status
		FROM autonomous_project_plan_node
		WHERE plan_id=$1
		ORDER BY id
		FOR UPDATE
	`, planID)
	if err != nil {
		return fmt.Errorf("lock project leader plan nodes: %w", err)
	}
	lockedNodeStatuses := make(map[string]string, len(loaded))
	for rows.Next() {
		var logicalNodeID, status string
		if err := rows.Scan(&logicalNodeID, &status); err != nil {
			rows.Close()
			return fmt.Errorf("read locked project leader plan node: %w", err)
		}
		lockedNodeStatuses[logicalNodeID] = status
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read locked project leader plan nodes: %w", err)
	}
	rows.Close()
	if len(lockedNodeStatuses) != len(loaded) {
		return errors.New("stale project leader proposal: base plan nodes changed during approval")
	}
	for _, node := range loaded {
		if status, ok := lockedNodeStatuses[node.LogicalNodeID]; !ok || status != node.Status {
			return errors.New("stale project leader proposal: base plan node state changed during approval")
		}
	}
	edges, err := tx.Query(ctx, `
		SELECT id FROM autonomous_project_plan_edge
		WHERE plan_id=$1
		ORDER BY id
		FOR UPDATE
	`, planID)
	if err != nil {
		return fmt.Errorf("lock project leader plan edges: %w", err)
	}
	defer edges.Close()
	for edges.Next() {
		var id pgtype.UUID
		if err := edges.Scan(&id); err != nil {
			return fmt.Errorf("read locked project leader plan edge: %w", err)
		}
	}
	if err := edges.Err(); err != nil {
		return fmt.Errorf("read locked project leader plan edges: %w", err)
	}
	return nil
}
