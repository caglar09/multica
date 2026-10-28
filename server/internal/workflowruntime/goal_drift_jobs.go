package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
)

func (r *Runtime) enqueueGoalDriftAuditCheckpoints(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) (bool, error) {
	var planID, planDigest, specificationRevisionID string
	var revision int64
	var completed, total int
	err := r.pool.QueryRow(ctx, `
		SELECT p.id::text, p.revision, p.plan_digest, p.specification_revision_id::text,
		       count(n.id) FILTER (WHERE n.status='completed')::int,
	       count(n.id) FILTER (WHERE n.status<>'cancelled')::int
		FROM autonomous_project_plan p
		LEFT JOIN autonomous_project_plan_node n
		  ON n.plan_id=p.id AND n.workspace_id=p.workspace_id AND n.project_id=p.project_id
		 AND n.is_epic=FALSE
		WHERE p.workspace_id=$1 AND p.project_id=$2
		  AND p.status IN ('active','blocked')
		GROUP BY p.id
		ORDER BY p.revision DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(
		&planID, &revision, &planDigest, &specificationRevisionID, &completed, &total,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read goal drift checkpoint progress: %w", err)
	}
	if total <= 0 {
		return false, nil
	}
	for _, checkpoint := range goalDriftAuditCheckpoints(0, completed, total) {
		key := fmt.Sprintf("%s:%s:%d", planID, planDigest, checkpoint)
		payload := goalDriftAuditJobPayload{
			PlanID:                  planID,
			PlanRevision:            revision,
			PlanDigest:              planDigest,
			SpecificationRevisionID: specificationRevisionID,
			CheckpointPercent:       checkpoint,
		}
		if err := r.enqueueControlPlaneJob(
			ctx, workspaceID, projectID, controlPlaneJobGoalDriftAudit, key, payload, 50, 3,
		); err != nil {
			return false, err
		}
		var status string
		if err := r.pool.QueryRow(ctx, `
			SELECT status FROM autonomous_control_plane_job
			WHERE workspace_id=$1 AND project_id=$2 AND job_type=$3 AND idempotency_key=$4
		`, workspaceID, projectID, controlPlaneJobGoalDriftAudit, key).Scan(&status); err != nil {
			return false, fmt.Errorf("read goal drift audit job status: %w", err)
		}
		if status != "completed" {
			return true, nil
		}
	}
	return false, nil
}

func (r *Runtime) goalDriftAuditBlocksProject(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) (bool, error) {
	var blocked bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_plan p
			JOIN autonomous_project_artifact a
			  ON a.plan_id=p.id AND a.workspace_id=p.workspace_id AND a.project_id=p.project_id
			WHERE p.workspace_id=$1 AND p.project_id=$2
			  AND p.status IN ('active','blocked')
			  AND a.artifact_type='review' AND a.status='active' AND a.valid=TRUE
			  AND a.content->>'goal_drift_audit'='true'
			  AND a.content->>'verdict'='changes_requested'
		)
	`, workspaceID, projectID).Scan(&blocked)
	return blocked, err
}

func (r *Runtime) executeGoalDriftAuditJob(ctx context.Context, job controlPlaneJob) (any, error) {
	var payload goalDriftAuditJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode goal drift audit job: %w", err)
	}
	if payload.PlanID == "" || payload.PlanDigest == "" || payload.SpecificationRevisionID == "" ||
		payload.PlanRevision <= 0 || (payload.CheckpointPercent != 25 && payload.CheckpointPercent != 50 && payload.CheckpointPercent != 75 && payload.CheckpointPercent != 100) {
		return nil, errors.New("goal drift audit job is missing its plan binding or checkpoint")
	}
	jobID := util.UUIDToString(job.ID)
	var existing json.RawMessage
	err := r.pool.QueryRow(ctx, `
		SELECT content FROM autonomous_project_artifact
		WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3::uuid
		  AND artifact_type='review' AND content->>'audit_job_id'=$4
		LIMIT 1
	`, job.WorkspaceID, job.ProjectID, payload.PlanID, jobID).Scan(&existing)
	if err == nil {
		var stored map[string]any
		_ = json.Unmarshal(existing, &stored)
		return map[string]any{"status": "already_recorded", "verdict": stored["verdict"]}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check prior goal drift audit artifact: %w", err)
	}
	if r.goalDriftAuditor == nil {
		cause := errors.New("goal drift auditor webhook is not configured")
		if escalationErr := r.recordGoalDriftAuditEscalation(ctx, job, payload, "quality_policy"); escalationErr != nil {
			return nil, errors.Join(cause, escalationErr)
		}
		return nil, cause
	}

	var planRevision int64
	var planDigest, planSpecificationRevisionID string
	err = r.pool.QueryRow(ctx, `
		SELECT revision, plan_digest, specification_revision_id::text
		FROM autonomous_project_plan
		WHERE id=$1::uuid AND workspace_id=$2 AND project_id=$3
		  AND status IN ('active','blocked','completed')
	`, payload.PlanID, job.WorkspaceID, job.ProjectID).Scan(&planRevision, &planDigest, &planSpecificationRevisionID)
	if err != nil {
		return nil, fmt.Errorf("load goal drift audit plan: %w", err)
	}
	if planRevision != payload.PlanRevision || planDigest != payload.PlanDigest || planSpecificationRevisionID != payload.SpecificationRevisionID {
		return nil, errors.New("goal drift audit plan binding is stale")
	}
	specification, found, err := r.projectStore.LoadLatestSpecification(ctx, job.WorkspaceID, job.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("load canonical goal drift specification: %w", err)
	}
	if !found || specification.ID != payload.SpecificationRevisionID {
		return nil, errors.New("goal drift audit specification is no longer the canonical plan revision")
	}
	canonical, err := canonicalGoalReviewContext(specification.Specification)
	if err != nil {
		return nil, err
	}
	var completed, total int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='completed')::int,
		       count(*) FILTER (WHERE status<>'cancelled')::int
		FROM autonomous_project_plan_node
		WHERE plan_id=$1::uuid AND workspace_id=$2 AND project_id=$3 AND is_epic=FALSE
	`, payload.PlanID, job.WorkspaceID, job.ProjectID).Scan(&completed, &total); err != nil {
		return nil, fmt.Errorf("read goal drift audit progress: %w", err)
	}
	if total <= 0 || completed < quarterProgressThreshold(total, payload.CheckpointPercent) {
		return nil, errors.New("goal drift audit checkpoint has not been reached")
	}
	evidence, err := r.completedGoalDriftEvidence(ctx, job, payload.PlanID)
	if err != nil {
		return nil, err
	}
	request := projectorchestration.GoalDriftAuditRequest{
		AuditID:        jobID,
		WorkspaceID:    util.UUIDToString(job.WorkspaceID),
		ProjectID:      util.UUIDToString(job.ProjectID),
		PlanID:         payload.PlanID,
		PlanRevision:   planRevision,
		PlanDigest:     planDigest,
		Checkpoint:     payload.CheckpointPercent,
		CompletedNodes: completed,
		TotalNodes:     total,
		Specification:  specification.Specification,
		CompletedWork:  evidence,
	}
	result, err := r.goalDriftAuditor.Audit(ctx, request)
	if err != nil {
		if escalationErr := r.recordGoalDriftAuditEscalation(ctx, job, payload, "quality_policy"); escalationErr != nil {
			return nil, errors.Join(err, escalationErr)
		}
		return nil, err
	}
	out, err := goalDriftReviewVerdict(result)
	if err != nil {
		if escalationErr := r.recordGoalDriftAuditEscalation(ctx, job, payload, "quality_policy"); escalationErr != nil {
			return nil, errors.Join(err, escalationErr)
		}
		return nil, err
	}
	if err := validateGoalDriftReview(out, canonical.requirementIDs()); err != nil {
		if escalationErr := r.recordGoalDriftAuditEscalation(ctx, job, payload, "quality_policy"); escalationErr != nil {
			return nil, errors.Join(err, escalationErr)
		}
		return nil, err
	}
	content := map[string]any{
		"goal_drift_audit":          true,
		"audit_job_id":              jobID,
		"plan_id":                   payload.PlanID,
		"plan_revision":             planRevision,
		"plan_digest":               planDigest,
		"specification_revision_id": specification.ID,
		"checkpoint_percent":        payload.CheckpointPercent,
		"completed_nodes":           completed,
		"total_nodes":               total,
		"verdict":                   out.Verdict,
		"summary":                   out.Summary,
		"goal_alignment_score":      out.GoalAlignmentScore,
		"goal_alignment_rationale":  out.GoalAlignmentRationale,
		"requirement_attestations":  out.RequirementAttestations,
		"findings":                  out.Findings,
		"completed_work":            evidence,
	}
	if err := r.persistGoalDriftAudit(ctx, job, payload, planRevision, content, out.Verdict); err != nil {
		return nil, err
	}
	return map[string]any{
		"checkpoint_percent":   payload.CheckpointPercent,
		"verdict":              out.Verdict,
		"goal_alignment_score": *out.GoalAlignmentScore,
	}, nil
}

func (r *Runtime) completedGoalDriftEvidence(
	ctx context.Context,
	job controlPlaneJob,
	planID string,
) ([]projectorchestration.GoalDriftWorkEvidence, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT n.node_key, n.title, n.description, n.requirement_ids, n.acceptance_criteria,
		       COALESCE(cs.commit_sha,''), COALESCE(cs.changed_files,'[]'::jsonb)
		FROM autonomous_project_plan_node n
		LEFT JOIN LATERAL (
			SELECT c.commit_sha, c.changed_files
			FROM autonomous_project_change_set c
			WHERE c.plan_id=n.plan_id AND c.node_id=n.id
			ORDER BY c.created_at DESC
			LIMIT 1
		) cs ON TRUE
		WHERE n.plan_id=$1::uuid AND n.workspace_id=$2 AND n.project_id=$3
		  AND n.is_epic=FALSE AND n.status='completed'
		ORDER BY n.completed_at ASC, n.node_key ASC
		LIMIT 200
	`, planID, job.WorkspaceID, job.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("load completed goal drift evidence: %w", err)
	}
	defer rows.Close()
	evidence := make([]projectorchestration.GoalDriftWorkEvidence, 0, 32)
	for rows.Next() {
		var item projectorchestration.GoalDriftWorkEvidence
		var requirementIDs, criteria, changedFiles []byte
		if err := rows.Scan(&item.NodeKey, &item.Title, &item.Description, &requirementIDs, &criteria, &item.CommitSHA, &changedFiles); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(requirementIDs, &item.RequirementIDs); err != nil {
			return nil, fmt.Errorf("decode goal drift requirement evidence: %w", err)
		}
		if err := json.Unmarshal(criteria, &item.AcceptanceCriteria); err != nil {
			return nil, fmt.Errorf("decode goal drift acceptance evidence: %w", err)
		}
		if err := json.Unmarshal(changedFiles, &item.ChangedFiles); err != nil {
			return nil, fmt.Errorf("decode goal drift changed-file evidence: %w", err)
		}
		evidence = append(evidence, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return evidence, nil
}

func goalDriftReviewVerdict(result projectorchestration.GoalDriftAuditResult) (reviewVerdictOutput, error) {
	encoded, err := json.Marshal(map[string]any{
		"verdict":                  result.Verdict,
		"summary":                  result.Summary,
		"goal_alignment_score":     result.GoalAlignmentScore,
		"goal_alignment_rationale": result.GoalAlignmentRationale,
		"requirement_attestations": result.RequirementAttestations,
		"findings":                 result.Findings,
	})
	if err != nil {
		return reviewVerdictOutput{}, err
	}
	return parseReviewVerdict(encoded)
}

func (r *Runtime) persistGoalDriftAudit(
	ctx context.Context,
	job controlPlaneJob,
	payload goalDriftAuditJobPayload,
	planRevision int64,
	content map[string]any,
	verdict string,
) error {
	encoded, err := json.Marshal(content)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentDigest, currentSpec, headSpec string
	var currentRevision int64
	err = tx.QueryRow(ctx, `
		SELECT p.revision, p.plan_digest, p.specification_revision_id::text, h.specification_revision_id::text
		FROM autonomous_project_plan p
		JOIN autonomous_project_specification_head h
		  ON h.workspace_id=p.workspace_id AND h.project_id=p.project_id
		WHERE p.id=$1::uuid AND p.workspace_id=$2 AND p.project_id=$3
		  AND p.status IN ('active','blocked','completed')
		FOR SHARE OF p,h
	`, payload.PlanID, job.WorkspaceID, job.ProjectID).Scan(&currentRevision, &currentDigest, &currentSpec, &headSpec)
	if err != nil {
		return fmt.Errorf("recheck goal drift audit binding before persist: %w", err)
	}
	if currentRevision != planRevision || currentDigest != payload.PlanDigest ||
		currentSpec != payload.SpecificationRevisionID || headSpec != payload.SpecificationRevisionID {
		return errors.New("goal drift audit became stale before persistence")
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id, project_id, plan_id, artifact_type, name,
			content, status, valid, artifact_revision
		)
		SELECT $1,$2,$3::uuid,'review',$4,$5,'active',TRUE,$6
		WHERE NOT EXISTS (
			SELECT 1 FROM autonomous_project_artifact
			WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3::uuid
			  AND artifact_type='review' AND content->>'audit_job_id'=$7
		)
	`, job.WorkspaceID, job.ProjectID, payload.PlanID,
		fmt.Sprintf("goal-drift-checkpoint-%d", payload.CheckpointPercent), encoded, planRevision, util.UUIDToString(job.ID))
	if err != nil {
		return fmt.Errorf("persist goal drift audit artifact: %w", err)
	}
	if verdict == "changes_requested" {
		contextJSON, marshalErr := json.Marshal(map[string]any{
			"audit_job_id":       util.UUIDToString(job.ID),
			"checkpoint_percent": payload.CheckpointPercent,
			"plan_digest":        payload.PlanDigest,
			"verdict":            verdict,
		})
		if marshalErr != nil {
			return marshalErr
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO autonomous_project_escalation (
				workspace_id, project_id, plan_id, category, severity, summary, context
			)
			SELECT $1,$2,$3::uuid,'quality_policy','high',$4,$5
			WHERE NOT EXISTS (
				SELECT 1 FROM autonomous_project_escalation
				WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3::uuid
				  AND category='quality_policy' AND status IN ('open','acknowledged')
				  AND context->>'audit_job_id'=$6
			)
		`, job.WorkspaceID, job.ProjectID, payload.PlanID,
			fmt.Sprintf("Goal alignment review requested changes at %d%% progress.", payload.CheckpointPercent),
			contextJSON, util.UUIDToString(job.ID))
		if err != nil {
			return fmt.Errorf("open goal drift policy escalation: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *Runtime) recordGoalDriftAuditEscalation(
	ctx context.Context,
	job controlPlaneJob,
	payload goalDriftAuditJobPayload,
	category string,
) error {
	contextJSON, err := json.Marshal(map[string]any{
		"audit_job_id":       util.UUIDToString(job.ID),
		"checkpoint_percent": payload.CheckpointPercent,
		"plan_digest":        payload.PlanDigest,
		"review_required":    true,
	})
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, plan_id, category, severity, summary, context
		)
		SELECT $1,$2,NULLIF($3,'')::uuid,$4,'high',$5,$6
		WHERE NOT EXISTS (
			SELECT 1 FROM autonomous_project_escalation
			WHERE workspace_id=$1 AND project_id=$2 AND plan_id=NULLIF($3,'')::uuid
			  AND category=$4 AND status IN ('open','acknowledged')
			  AND context->>'audit_job_id'=$7
		)
	`, job.WorkspaceID, job.ProjectID, payload.PlanID, category,
		"Goal alignment audit is unavailable or returned an invalid result; review is required.",
		contextJSON, util.UUIDToString(job.ID))
	return err
}
