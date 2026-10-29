package projectorchestration

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	var sourceKind, sourceRevision, storedSpecificationRevisionID string
	if err := tx.QueryRow(ctx, `
		SELECT r.source_kind, p.source_revision, p.specification_revision_id::text
		FROM autonomous_project_plan p
		JOIN autonomous_project_specification_revision r
		  ON r.id = p.specification_revision_id
		 AND r.workspace_id = p.workspace_id
		 AND r.project_id = p.project_id
		WHERE p.id = $1
	`, planID).Scan(&sourceKind, &sourceRevision, &storedSpecificationRevisionID); err != nil {
		return PlanLifecycle{}, err
	}
	if strings.TrimSpace(plan.SpecificationRevisionID) != storedSpecificationRevisionID {
		return PlanLifecycle{}, errors.New("stored project plan specification revision does not match the planned revision")
	}
	var digest string
	if err := tx.QueryRow(ctx, `SELECT autonomous_project_plan_digest($1)`, planID).Scan(&digest); err != nil {
		return PlanLifecycle{}, err
	}
	lifecycle := PlanLifecycle{Status: "active", Digest: digest, ApprovedBy: "system_policy_engine"}
	autoApproved := !requiresHumanPlanApproval(plan, sourceKind, sourceRevision)
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
	return lifecycle, nil
}

func ApprovePlanTx(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, digest, approvedBy string) (bool, error) {
	if tx == nil || !workspaceID.Valid || !projectID.Valid || digest == "" || approvedBy == "" {
		return false, errors.New("plan approval requires a transaction, project, digest and approver")
	}
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
	err := tx.QueryRow(ctx, `
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
		RETURNING p.id
	`, workspaceID, projectID, digest, approvedBy).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := refreshReadyTx(ctx, tx, planID); err != nil {
		return false, err
	}
	return true, nil
}

func requiresHumanPlanApproval(plan Plan, sourceKind, sourceRevision string) bool {
	if sourceKind == "change_request" || strings.HasPrefix(sourceRevision, "change-request:") {
		return true
	}
	for _, node := range plan.Nodes {
		if node.Risk != RiskLow || node.Kind == NodeMigration || node.Kind == NodeDeploy {
			return true
		}
	}
	return false
}
