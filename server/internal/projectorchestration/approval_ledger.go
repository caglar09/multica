package projectorchestration

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ApprovalEvent struct {
	WorkspaceID     pgtype.UUID
	ProjectID       pgtype.UUID
	IdempotencyKey  string
	SubjectType     string
	SubjectID       string
	SubjectRevision pgtype.Int8
	SubjectDigest   string
	Decision        string
	ActorType       string
	ActorID         pgtype.UUID
	ActorDisplay    string
	PolicyRef       *string
	Rationale       string
	SourceEndpoint  string
	Origin          string
	OriginRef       []byte
}

const insertApprovalEventSQL = `INSERT INTO autonomous_project_approval_event
	(workspace_id, project_id, idempotency_key, subject_type, subject_id, subject_revision,
	 subject_digest, decision, actor_type, actor_id, actor_display, policy_ref, rationale,
	 source_endpoint, origin, origin_ref)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	ON CONFLICT (workspace_id, project_id, idempotency_key) DO NOTHING
	RETURNING id`

const approvalEventPayloadMatchesSQL = `SELECT
	subject_type IS NOT DISTINCT FROM $4
	AND subject_id IS NOT DISTINCT FROM $5
	AND subject_revision IS NOT DISTINCT FROM $6
	AND subject_digest IS NOT DISTINCT FROM $7
	AND decision IS NOT DISTINCT FROM $8
	AND actor_type IS NOT DISTINCT FROM $9
	AND actor_id IS NOT DISTINCT FROM $10
	AND actor_display IS NOT DISTINCT FROM $11
	AND policy_ref IS NOT DISTINCT FROM $12
	AND rationale IS NOT DISTINCT FROM $13
	AND source_endpoint IS NOT DISTINCT FROM $14
	AND origin IS NOT DISTINCT FROM $15
	AND origin_ref IS NOT DISTINCT FROM $16::jsonb
FROM autonomous_project_approval_event
WHERE workspace_id=$1 AND project_id=$2 AND idempotency_key=$3`

// RecordApprovalTx records an approval in the caller's transaction. Reusing an
// idempotency key is accepted only when the recorded payload is identical.
func RecordApprovalTx(ctx context.Context, tx pgx.Tx, event ApprovalEvent) error {
	if tx == nil {
		return errors.New("approval transaction is required")
	}
	originRef := event.OriginRef
	if len(originRef) == 0 {
		originRef = []byte(`{}`)
	}
	args := []any{
		event.WorkspaceID, event.ProjectID, event.IdempotencyKey, event.SubjectType, event.SubjectID,
		event.SubjectRevision, event.SubjectDigest, event.Decision, event.ActorType, event.ActorID,
		event.ActorDisplay, event.PolicyRef, event.Rationale, event.SourceEndpoint, event.Origin, originRef,
	}
	var insertedID pgtype.UUID
	err := tx.QueryRow(ctx, insertApprovalEventSQL, args...).Scan(&insertedID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("insert approval event: %w", err)
	}
	var samePayload bool
	if err := tx.QueryRow(ctx, approvalEventPayloadMatchesSQL, args...).Scan(&samePayload); err != nil {
		return fmt.Errorf("read approval event idempotency record: %w", err)
	}
	if !samePayload {
		return fmt.Errorf("approval idempotency key %q was already used for a different payload", event.IdempotencyKey)
	}
	return nil
}
