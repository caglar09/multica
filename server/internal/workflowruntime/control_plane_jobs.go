package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
)

const (
	controlPlaneJobTeamPlanner    = "team_planner"
	controlPlaneJobTeamReplan     = "team_replan"
	controlPlaneJobProjectPlanner = "project_planner"
	controlPlaneJobBrainLearning  = "brain_learning"
	controlPlaneJobQualityGate    = "quality_gate"
	controlPlaneJobImpactAnalysis = "impact_analysis"
	controlPlaneJobPlanMutation   = "plan_mutation"

	controlPlaneWorkerCount        = 4
	controlPlaneLease              = 45 * time.Second
	controlPlaneJobTimeout         = 6 * time.Minute
	controlPlaneQualityResumeEvent = "control-plane:quality-gate-passed"
)

type controlPlaneJob struct {
	ID             pgtype.UUID
	WorkspaceID    pgtype.UUID
	ProjectID      pgtype.UUID
	Type           string
	IdempotencyKey string
	Payload        []byte
	Attempts       int
	MaxAttempts    int
	LeaseOwner     string
}

type teamPlannerJobPayload struct{}

type projectPlannerJobPayload struct {
	SourceRevision string `json:"source_revision"`
}

type teamReplanJobPayload struct {
	SourceRevision string `json:"source_revision"`
}

type brainLearningJobPayload struct {
	TaskID   string          `json:"task_id"`
	Evidence json.RawMessage `json:"evidence"`
}

type qualityGateJobPayload struct {
	TaskID       string         `json:"task_id"`
	IssueID      string         `json:"issue_id"`
	GateType     string         `json:"gate_type"`
	ArtifactType string         `json:"artifact_type"`
	Artifact     map[string]any `json:"artifact"`
}

type impactAnalysisJobPayload struct {
	Request projectorchestration.ChangeImpactRequest `json:"request"`
}

type planMutationJobPayload struct {
	ChangeRequestID string                                        `json:"change_request_id"`
	Operations      []projectorchestration.PlanMutationOperation `json:"operations"`
	PlannerName     string                                        `json:"planner_name"`
	PlannerModel    string                                        `json:"planner_model"`
}

func (r *Runtime) enqueueControlPlaneJob(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	jobType, idempotencyKey string,
	payload any,
	priority, maxAttempts int,
) error {
	if r == nil || r.pool == nil || !workspaceID.Valid || !projectID.Valid {
		return errors.New("control-plane job requires runtime, workspace and project")
	}
	jobType = strings.TrimSpace(jobType)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if jobType == "" || idempotencyKey == "" {
		return errors.New("control-plane job type and idempotency key are required")
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode control-plane job payload: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_control_plane_job (
			workspace_id, project_id, job_type, idempotency_key,
			payload, status, priority, max_attempts, available_at
		)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$7,now())
		ON CONFLICT (workspace_id, project_id, job_type, idempotency_key) DO NOTHING
	`, workspaceID, projectID, jobType, idempotencyKey, raw, priority, maxAttempts)
	return err
}

func (r *Runtime) claimControlPlaneJob(ctx context.Context, leaseOwner string) (controlPlaneJob, bool, error) {
	var job controlPlaneJob
	err := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT j.id
			FROM autonomous_control_plane_job j
			WHERE (
				(j.status IN ('pending','retry') AND j.available_at <= now())
				OR (j.status IN ('claimed','running') AND j.lease_expires_at < now())
			)
			AND NOT EXISTS (
				SELECT 1
				FROM autonomous_control_plane_job active
				WHERE active.project_id = j.project_id
				  AND active.id <> j.id
				  AND active.status IN ('claimed','running')
				  AND active.lease_expires_at >= now()
			)
			ORDER BY j.priority DESC, j.available_at ASC, j.created_at ASC
			FOR UPDATE OF j SKIP LOCKED
			LIMIT 1
		)
		UPDATE autonomous_control_plane_job j
		SET status = 'claimed',
		    lease_owner = $1,
		    lease_expires_at = now() + ($2 * interval '1 second'),
		    claimed_at = now(),
		    started_at = NULL,
		    completed_at = NULL,
		    attempts = j.attempts + 1,
		    updated_at = now()
		FROM candidate c
		WHERE j.id = c.id
		RETURNING j.id, j.workspace_id, j.project_id, j.job_type,
		          j.idempotency_key, j.payload, j.attempts, j.max_attempts,
		          COALESCE(j.lease_owner,'')
	`, leaseOwner, int64(controlPlaneLease/time.Second)).Scan(
		&job.ID, &job.WorkspaceID, &job.ProjectID, &job.Type,
		&job.IdempotencyKey, &job.Payload, &job.Attempts, &job.MaxAttempts,
		&job.LeaseOwner,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return controlPlaneJob{}, false, nil
	}
	if err != nil {
		return controlPlaneJob{}, false, err
	}
	return job, true, nil
}

func (r *Runtime) markControlPlaneJobRunning(ctx context.Context, job controlPlaneJob) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE autonomous_control_plane_job
		SET status='running', started_at=COALESCE(started_at,now()), updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='claimed' AND lease_expires_at >= now()
	`, job.ID, job.LeaseOwner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("control-plane job lease was lost before start")
	}
	return nil
}

func (r *Runtime) heartbeatControlPlaneJob(ctx context.Context, job controlPlaneJob) bool {
	tag, err := r.pool.Exec(ctx, `
		UPDATE autonomous_control_plane_job
		SET lease_expires_at=now()+($3 * interval '1 second'), updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='running'
	`, job.ID, job.LeaseOwner, int64(controlPlaneLease/time.Second))
	if err != nil {
		slog.Warn("control-plane job heartbeat failed", "job_id", util.UUIDToString(job.ID), "error", err)
		return false
	}
	return tag.RowsAffected() == 1
}

func (r *Runtime) completeControlPlaneJob(ctx context.Context, job controlPlaneJob, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE autonomous_control_plane_job
		SET status='completed', result=$3, completed_at=now(),
		    lease_owner=NULL, lease_expires_at=NULL, last_error=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='running'
	`, job.ID, job.LeaseOwner, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("control-plane job completion rejected after lease loss")
	}
	return nil
}

func controlPlaneBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	backoff := time.Duration(1<<min(attempt-1, 6)) * 2 * time.Second
	if backoff > 2*time.Minute {
		return 2 * time.Minute
	}
	return backoff
}

func (r *Runtime) failControlPlaneJob(ctx context.Context, job controlPlaneJob, cause error) (bool, error) {
	terminal := job.Attempts >= job.MaxAttempts
	status := "retry"
	if terminal {
		status = "failed"
	}
	message := cause.Error()
	if len(message) > 4000 {
		message = message[:4000]
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE autonomous_control_plane_job
		SET status=$3,
		    available_at=CASE WHEN $3='retry' THEN now()+($4 * interval '1 second') ELSE available_at END,
		    lease_owner=NULL, lease_expires_at=NULL,
		    completed_at=CASE WHEN $3='failed' THEN now() ELSE NULL END,
		    last_error=$5, updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='running'
	`, job.ID, job.LeaseOwner, status, int64(controlPlaneBackoff(job.Attempts)/time.Second), message)
	if err != nil {
		return terminal, err
	}
	if tag.RowsAffected() != 1 {
		return terminal, errors.New("control-plane job failure rejected after lease loss")
	}
	return terminal, nil
}

func (r *Runtime) runControlPlaneWorkers(ctx context.Context) {
	for i := 0; i < controlPlaneWorkerCount; i++ {
		owner := fmt.Sprintf("control-plane-%d-%d", time.Now().UnixNano(), i)
		go r.runControlPlaneWorker(ctx, owner)
	}
}

func (r *Runtime) runControlPlaneWorker(ctx context.Context, owner string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for {
			job, ok, err := r.claimControlPlaneJob(ctx, owner)
			if err != nil {
				slog.Error("control-plane job claim failed", "worker", owner, "error", err)
				break
			}
			if !ok {
				break
			}
			r.executeControlPlaneJob(ctx, job)
		}
	}
}

func (r *Runtime) executeControlPlaneJob(parent context.Context, job controlPlaneJob) {
	if err := r.markControlPlaneJobRunning(parent, job); err != nil {
		slog.Warn("control-plane job could not start", "job_id", util.UUIDToString(job.ID), "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(parent, controlPlaneJobTimeout)
	defer cancel()

	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(controlPlaneLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !r.heartbeatControlPlaneJob(ctx, job) {
					cancel()
					return
				}
			}
		}
	}()

	result, err := r.handleControlPlaneJob(ctx, job)
	close(heartbeatDone)
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer persistCancel()
	if err == nil {
		if completeErr := r.completeControlPlaneJob(persistCtx, job, result); completeErr != nil {
			slog.Error("control-plane job completion persist failed", "job_id", util.UUIDToString(job.ID), "error", completeErr)
		}
		return
	}
	terminal, failErr := r.failControlPlaneJob(persistCtx, job, err)
	if failErr != nil {
		slog.Error("control-plane job failure persist failed", "job_id", util.UUIDToString(job.ID), "error", failErr)
		return
	}
	if terminal {
		r.onTerminalControlPlaneFailure(persistCtx, job, err)
	}
}

func (r *Runtime) handleControlPlaneJob(ctx context.Context, job controlPlaneJob) (any, error) {
	switch job.Type {
	case controlPlaneJobTeamPlanner:
		if r.team == nil {
			return nil, errors.New("team planner is unavailable")
		}
		draft, err := r.team.PrepareProject(ctx, job.WorkspaceID, job.ProjectID)
		if err != nil {
			return nil, err
		}
		r.setProjectControlError(ctx, job.WorkspaceID, job.ProjectID, "")
		return map[string]any{"status": draft.Status}, nil

	case controlPlaneJobProjectPlanner:
		var payload projectPlannerJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, err
		}
		if r.projectPlanner == nil || r.projectStore == nil || r.team == nil {
			return nil, errors.New("project planner is unavailable")
		}
		if err := r.planProjectRevision(ctx, job.WorkspaceID, job.ProjectID, payload.SourceRevision); err != nil {
			return nil, err
		}
		r.setProjectControlError(ctx, job.WorkspaceID, job.ProjectID, "")
		return map[string]any{"source_revision": payload.SourceRevision}, nil

	case controlPlaneJobTeamReplan:
		var payload teamReplanJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, err
		}
		if r.team == nil {
			return nil, errors.New("team planner is unavailable")
		}
		_, plan, err := r.team.ReconcileProject(ctx, job.WorkspaceID, job.ProjectID, payload.SourceRevision)
		if err != nil {
			return nil, err
		}
		requestedAt, err := time.Parse(time.RFC3339Nano, payload.SourceRevision)
		if err != nil {
			return nil, fmt.Errorf("decode team replan source revision: %w", err)
		}
		_, err = r.pool.Exec(ctx, `
			UPDATE autonomous_project_control
			SET replan_completed_at=$3, last_error=NULL, updated_at=now()
			WHERE workspace_id=$1 AND project_id=$2 AND replan_requested_at=$3
		`, job.WorkspaceID, job.ProjectID, requestedAt)
		if err != nil {
			return nil, err
		}
		return map[string]any{"roles": len(plan.Roles), "source_revision": payload.SourceRevision}, nil

	case controlPlaneJobBrainLearning:
		return r.executeBrainLearningJob(ctx, job)

	case controlPlaneJobQualityGate:
		return r.executeQualityGateJob(ctx, job)

	case controlPlaneJobImpactAnalysis:
		var payload impactAnalysisJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, err
		}
		if r.repositoryAnalyzer == nil {
			return nil, projectorchestration.ErrAdapterNotConfigured
		}
		return r.repositoryAnalyzer.Impact(ctx, job.WorkspaceID, job.ProjectID, payload.Request)

	case controlPlaneJobPlanMutation:
		var payload planMutationJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, err
		}
		if r.projectStore == nil {
			return nil, errors.New("project store is unavailable")
		}
		changeID, err := util.ParseUUID(payload.ChangeRequestID)
		if err != nil {
			return nil, err
		}
		return r.projectStore.ApplyChangePlanMutation(
			ctx, job.WorkspaceID, job.ProjectID, changeID,
			payload.Operations, payload.PlannerName, payload.PlannerModel,
		)
	default:
		return nil, fmt.Errorf("unsupported control-plane job type %q", job.Type)
	}
}

func (r *Runtime) setProjectControlError(ctx context.Context, workspaceID, projectID pgtype.UUID, message string) {
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, _ = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_control (project_id, workspace_id, last_error, updated_at)
		VALUES ($1,$2,NULLIF($3,''),now())
		ON CONFLICT (project_id) DO UPDATE
		SET last_error=NULLIF(EXCLUDED.last_error,''), updated_at=now()
		WHERE autonomous_project_control.workspace_id=EXCLUDED.workspace_id
	`, projectID, workspaceID, message)
}

func (r *Runtime) onTerminalControlPlaneFailure(ctx context.Context, job controlPlaneJob, cause error) {
	switch job.Type {
	case controlPlaneJobTeamPlanner:
		r.setProjectControlError(ctx, job.WorkspaceID, job.ProjectID, "Project bootstrap failed: "+cause.Error())
	case controlPlaneJobProjectPlanner:
		r.setProjectControlError(ctx, job.WorkspaceID, job.ProjectID, "Project planning failed: "+cause.Error())
	case controlPlaneJobTeamReplan:
		r.setProjectControlError(ctx, job.WorkspaceID, job.ProjectID, "Team replan failed: "+cause.Error())
	case controlPlaneJobQualityGate:
		var payload qualityGateJobPayload
		if json.Unmarshal(job.Payload, &payload) != nil {
			return
		}
		issueID, err := util.ParseUUID(payload.IssueID)
		if err != nil {
			return
		}
		q, err := r.loadNodeQualityByIssue(ctx, job.WorkspaceID, issueID)
		if err == nil {
			_ = r.blockForQualityFailure(ctx, q, payload.GateType, cause)
		}
	}
}

func (r *Runtime) executeBrainLearningJob(ctx context.Context, job controlPlaneJob) (any, error) {
	if r.projectStore == nil || r.brainExecutor == nil {
		return nil, errors.New("project brain runtime is unavailable")
	}
	var payload brainLearningJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, err
	}
	taskID, err := util.ParseUUID(payload.TaskID)
	if err != nil {
		return nil, err
	}
	cfg, err := r.projectStore.GetBrainConfig(ctx, job.WorkspaceID, job.ProjectID)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || cfg.LearningMode == "deterministic" {
		return map[string]any{"mode": "deterministic"}, nil
	}
	result, err := r.brainExecutor.Execute(ctx, cfg, payload.Evidence)
	if err != nil {
		return nil, err
	}
	memories, err := decodeBrainMemories(result.Output)
	if err != nil {
		return nil, err
	}
	source := projectorchestration.MemorySource{
		SourceType: "brain_learning", SourceID: util.UUIDToString(taskID),
		CreatedByType: "agent", Authority: projectorchestration.AuthorityAgentInference,
		Evidence: payload.Evidence, ObservedAt: time.Now().UTC(),
	}
	for _, memory := range memories {
		retention, err := r.projectStore.RetainMemoryGoverned(ctx, job.WorkspaceID, job.ProjectID, memory, source)
		if err != nil {
			return nil, err
		}
		if err := r.projectStore.AssessMemoryImpact(ctx, job.WorkspaceID, job.ProjectID, memory, retention, source); err != nil {
			return nil, err
		}
	}
	return map[string]any{"provider": result.Provider, "model": result.Model, "memories": len(memories)}, nil
}

func (r *Runtime) executeQualityGateJob(ctx context.Context, job controlPlaneJob) (any, error) {
	var payload qualityGateJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, err
	}
	issueID, err := util.ParseUUID(payload.IssueID)
	if err != nil {
		return nil, err
	}
	q, err := r.loadNodeQualityByIssue(ctx, job.WorkspaceID, issueID)
	if err != nil {
		return nil, err
	}
	required, deterministic, err := r.requiredGate(ctx, q, payload.GateType)
	if err != nil {
		return nil, err
	}
	if !required {
		return map[string]any{"required": false}, nil
	}
	if !deterministic {
		return nil, errors.New("semantic quality gate was incorrectly queued as deterministic work")
	}
	if err := r.runDeterministicGate(ctx, q, payload.GateType, payload.Artifact); err != nil {
		return nil, err
	}
	if err := r.handleTaskCompleted(ctx, events.Event{
		Type:        controlPlaneQualityResumeEvent,
		WorkspaceID: util.UUIDToString(job.WorkspaceID),
		TaskID:      payload.TaskID,
	}); err != nil {
		return nil, fmt.Errorf("resume task after quality gate: %w", err)
	}
	return map[string]any{"passed": true, "gate_type": payload.GateType}, nil
}

func (r *Runtime) enqueueImpactAnalysis(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	key string,
	request projectorchestration.ChangeImpactRequest,
) error {
	return r.enqueueControlPlaneJob(ctx, workspaceID, projectID, controlPlaneJobImpactAnalysis, key,
		impactAnalysisJobPayload{Request: request}, 60, 3)
}

func (r *Runtime) enqueuePlanMutation(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	key, changeRequestID string,
	operations []projectorchestration.PlanMutationOperation,
	plannerName, plannerModel string,
) error {
	return r.enqueueControlPlaneJob(ctx, workspaceID, projectID, controlPlaneJobPlanMutation, key,
		planMutationJobPayload{ChangeRequestID: changeRequestID, Operations: operations, PlannerName: plannerName, PlannerModel: plannerModel}, 70, 3)
}
