package projectorchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

var ErrPlanApprovalRequired = errors.New("autonomous project plan requires explicit approval before work can start")

type PlanLifecycle struct {
	Status     string
	Digest     string
	ApprovedAt *time.Time
	ApprovedBy string
}

func FinalizePlanLifecycleTx(ctx context.Context, tx pgx.Tx, planID pgtype.UUID, plan Plan) (PlanLifecycle, error) {
	if tx == nil || !planID.Valid {
		return PlanLifecycle{}, errors.New("plan lifecycle requires a transaction and plan id")
	}
	var workspaceID, projectID, specificationRevisionID pgtype.UUID
	var sourceKind, sourceRevision string
	var revision int64
	if err := tx.QueryRow(ctx, `
		SELECT p.workspace_id, p.project_id, p.specification_revision_id,
		       r.source_kind, p.source_revision, p.revision
		FROM autonomous_project_plan p
		JOIN autonomous_project_specification_revision r
		  ON r.id = p.specification_revision_id
		 AND r.workspace_id = p.workspace_id
		 AND r.project_id = p.project_id
		WHERE p.id = $1
	`, planID).Scan(&workspaceID, &projectID, &specificationRevisionID, &sourceKind, &sourceRevision, &revision); err != nil {
		return PlanLifecycle{}, err
	}
	if strings.TrimSpace(plan.SpecificationRevisionID) != util.UUIDToString(specificationRevisionID) {
		return PlanLifecycle{}, errors.New("stored project plan specification revision does not match the planned revision")
	}
	var digest string
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_plan_digest($1)`, planID).Scan(&digest); err != nil {
		return PlanLifecycle{}, err
	}
	lifecycle := PlanLifecycle{Status: "active", Digest: digest, ApprovedBy: "system_policy_engine"}
	autoApproved := !requiresHumanPlanApproval(plan, sourceKind, sourceRevision, revision)
	if autoApproved {
		approvedHead, err := planMatchesHumanApprovedSpecificationHeadTx(ctx, tx, workspaceID, projectID, planID, specificationRevisionID)
		if err != nil {
			return PlanLifecycle{}, err
		}
		autoApproved = approvedHead
	}
	if !autoApproved {
		lifecycle.Status = "awaiting_approval"
		lifecycle.ApprovedBy = ""
	}
	var approvedAt pgtype.Timestamptz
	var approvedBy pgtype.Text
	if err := tx.QueryRow(ctx, `
		UPDATE autonomous_project_plan
		SET plan_digest = $2,
		    status = $3,
		    approved_at = CASE WHEN $4 THEN now() ELSE NULL END,
		    approved_by = CASE WHEN $4 THEN $5 ELSE NULL END,
		    updated_at = now()
		WHERE id = $1
		RETURNING approved_at, approved_by
	`, planID, digest, lifecycle.Status, autoApproved, lifecycle.ApprovedBy).Scan(&approvedAt, &approvedBy); err != nil {
		return PlanLifecycle{}, err
	}
	lifecycle.ApprovedAt = nil
	if approvedAt.Valid {
		value := approvedAt.Time.UTC()
		lifecycle.ApprovedAt = &value
	}
	if approvedBy.Valid {
		lifecycle.ApprovedBy = approvedBy.String
	}
	if autoApproved {
		policyRef := "low_risk_replan_under_approved_spec"
		if err := RecordApprovalTx(ctx, tx, ApprovalEvent{
			WorkspaceID:     workspaceID,
			ProjectID:       projectID,
			IdempotencyKey:  "plan:system-approval:" + util.UUIDToString(planID) + ":" + digest,
			SubjectType:     "plan",
			SubjectID:       util.UUIDToString(planID),
			SubjectRevision: pgtype.Int8{Int64: revision, Valid: true},
			SubjectDigest:   digest,
			Decision:        "approved",
			ActorType:       "system",
			ActorDisplay:    "system_policy_engine",
			PolicyRef:       &policyRef,
			Rationale:       "Low-risk replan under the current human-approved specification",
			SourceEndpoint:  "system:plan_lifecycle",
			Origin:          "local",
		}); err != nil {
			return PlanLifecycle{}, fmt.Errorf("record system plan approval: %w", err)
		}
	}
	return lifecycle, nil
}

func planMatchesHumanApprovedSpecificationHeadTx(ctx context.Context, tx pgx.Tx, workspaceID, projectID, planID, specificationRevisionID pgtype.UUID) (bool, error) {
	var approved bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_plan p
			JOIN autonomous_project_specification_revision r
			  ON r.id=p.specification_revision_id
			 AND r.workspace_id=p.workspace_id
			 AND r.project_id=p.project_id
			JOIN autonomous_project_specification_head h
			  ON h.workspace_id=p.workspace_id
			 AND h.project_id=p.project_id
			 AND h.specification_revision_id=r.id
			 AND h.revision=r.revision
			WHERE p.id=$3
			  AND p.workspace_id=$1
			  AND p.project_id=$2
			  AND p.specification_revision_id=$4
			  AND autonomous_project_plan_matches_bound_specification(p.id)
			  AND COALESCE((
				SELECT e.decision='approved'
				   AND e.actor_type='human'
				   AND e.origin='local'
				   AND e.subject_digest=autonomous_project_specification_digest(r.id)
				FROM autonomous_project_approval_event e
				WHERE e.workspace_id=p.workspace_id
				  AND e.project_id=p.project_id
				  AND e.subject_type='specification_revision'
				  AND e.subject_id=r.id::text
				  AND e.subject_revision=r.revision
				ORDER BY e.acted_at DESC, e.id DESC
				LIMIT 1
			  ), FALSE)
		)
	`, workspaceID, projectID, planID, specificationRevisionID).Scan(&approved)
	if err != nil {
		return false, fmt.Errorf("verify approved specification head: %w", err)
	}
	return approved, nil
}

func ApprovePlanTx(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, digest, approvedBy string) (bool, error) {
	if tx == nil || !workspaceID.Valid || !projectID.Valid || digest == "" || approvedBy == "" {
		return false, errors.New("plan approval requires a transaction, project, digest and approver")
	}
	actorID, err := util.ParseUUID(approvedBy)
	if err != nil || !actorID.Valid {
		return false, errors.New("plan approver must be a valid user UUID")
	}
	approvedBy = util.UUIDToString(actorID)
	// Plan creation uses the same control-row lock, so approval always targets the latest revision.
	var locked bool
	if err := tx.QueryRow(ctx, `
		SELECT TRUE
		FROM autonomous_project_control
		WHERE workspace_id = $1 AND project_id = $2
		FOR UPDATE
	`, workspaceID, projectID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var planID pgtype.UUID
	var revision int64
	err = tx.QueryRow(ctx, `
		WITH latest AS (
			SELECT id
			FROM autonomous_project_plan
			WHERE workspace_id = $1 AND project_id = $2
			ORDER BY revision DESC
			LIMIT 1
		)
		UPDATE autonomous_project_plan p
		SET status = 'active', approved_at = now(), approved_by = $4, updated_at = now()
		FROM latest
		WHERE p.id = latest.id
		  AND p.status = 'awaiting_approval'
		  AND p.plan_digest = $3
		  AND p.plan_digest = autonomous_project_plan_digest(p.id)
		  AND p.specification_revision_id IS NOT NULL
		  AND autonomous_project_specification_approved(p.specification_revision_id)
		RETURNING p.id, p.revision
	`, workspaceID, projectID, digest, approvedBy).Scan(&planID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := RecordApprovalTx(ctx, tx, ApprovalEvent{
		WorkspaceID:     workspaceID,
		ProjectID:       projectID,
		IdempotencyKey:  "plan:human-approval:" + util.UUIDToString(planID) + ":" + digest,
		SubjectType:     "plan",
		SubjectID:       util.UUIDToString(planID),
		SubjectRevision: pgtype.Int8{Int64: revision, Valid: true},
		SubjectDigest:   digest,
		Decision:        "approved",
		ActorType:       "human",
		ActorID:         actorID,
		ActorDisplay:    util.UUIDToString(actorID),
		Rationale:       "Approved the current plan after exact digest validation",
		SourceEndpoint:  "/api/projects/:id/autonomous/plan/approve",
		Origin:          "local",
	}); err != nil {
		return false, fmt.Errorf("record human plan approval: %w", err)
	}
	if err := refreshReadyTx(ctx, tx, planID); err != nil {
		return false, err
	}
	return true, nil
}

func requiresHumanPlanApproval(plan Plan, sourceKind, sourceRevision string, revision int64) bool {
	if revision <= 1 || sourceKind == "change_request" || strings.HasPrefix(sourceRevision, "change-request:") {
		return true
	}
	for _, node := range plan.Nodes {
		if node.Risk != RiskLow || node.Kind == NodeMigration || node.Kind == NodeDeploy {
			return true
		}
	}
	return false
}
