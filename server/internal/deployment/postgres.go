package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DBTX is the executor accepted by sqlc-generated queries.
type DBTX = db.DBTX

type PostgresRepository struct {
	queries *db.Queries
	conn    DBTX
}

func NewPostgresRepository(conn DBTX) *PostgresRepository {
	return &PostgresRepository{queries: db.New(conn), conn: conn}
}

func (r *PostgresRepository) CreateProviderConnection(ctx context.Context, in CreateProviderConnectionInput) (ProviderConnection, error) {
	row, err := r.queries.CreateDeploymentProviderConnection(ctx, db.CreateDeploymentProviderConnectionParams{
		WorkspaceID: pgUUID(in.WorkspaceID), Provider: in.Provider, Name: in.Name,
		SecretRef: pgText(in.SecretRef), WebhookSecretRef: pgText(in.WebhookSecretRef), Config: jsonBytes(in.Config), Enabled: in.Enabled, ID: pgUUID(in.ID),
	})
	return mapProviderConnection(row), mapQueryError(err)
}

func (r *PostgresRepository) ListProviderConnections(ctx context.Context, workspaceID string) ([]ProviderConnection, error) {
	rows, err := r.queries.ListDeploymentProviderConnections(ctx, parseID(workspaceID))
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]ProviderConnection, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapProviderConnection(row))
	}
	return out, nil
}

func (r *PostgresRepository) GetProviderConnection(ctx context.Context, workspaceID, id string) (ProviderConnection, error) {
	row, err := r.queries.GetDeploymentProviderConnection(ctx, db.GetDeploymentProviderConnectionParams{WorkspaceID: parseID(workspaceID), ID: parseID(id)})
	return mapProviderConnection(row), mapQueryError(err)
}

func (r *PostgresRepository) UpdateProviderConnection(ctx context.Context, in UpdateProviderConnectionInput) (ProviderConnection, error) {
	row, err := r.queries.UpdateDeploymentProviderConnection(ctx, db.UpdateDeploymentProviderConnectionParams{
		WorkspaceID: pgUUID(in.WorkspaceID), ID: pgUUID(in.ID), Name: in.Name, SecretRef: pgText(in.SecretRef), WebhookSecretRef: pgText(in.WebhookSecretRef), Config: jsonBytes(in.Config), Enabled: in.Enabled,
	})
	return mapProviderConnection(row), mapQueryError(err)
}

func (r *PostgresRepository) DeleteProviderConnection(ctx context.Context, workspaceID, id string) error {
	return mapQueryError(r.queries.DeleteDeploymentProviderConnection(ctx, db.DeleteDeploymentProviderConnectionParams{WorkspaceID: parseID(workspaceID), ID: parseID(id)}))
}

func (r *PostgresRepository) CreateTarget(ctx context.Context, in CreateTargetInput) (Target, error) {
	row, err := r.queries.CreateDeploymentTarget(ctx, db.CreateDeploymentTargetParams{
		WorkspaceID: pgUUID(in.WorkspaceID), ConnectionID: pgUUID(in.ConnectionID), Name: in.Name, Environment: in.Environment,
		ExternalID: pgText(in.ExternalID), Config: jsonBytes(in.Config), ID: pgUUID(in.ID),
	})
	return mapTarget(row), mapQueryError(err)
}

func (r *PostgresRepository) ListTargets(ctx context.Context, workspaceID string) ([]Target, error) {
	rows, err := r.queries.ListDeploymentTargets(ctx, parseID(workspaceID))
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]Target, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapTarget(row))
	}
	return out, nil
}

func (r *PostgresRepository) GetTarget(ctx context.Context, workspaceID, id string) (Target, error) {
	row, err := r.queries.GetDeploymentTarget(ctx, db.GetDeploymentTargetParams{WorkspaceID: parseID(workspaceID), ID: parseID(id)})
	return mapTarget(row), mapQueryError(err)
}

func (r *PostgresRepository) UpdateTarget(ctx context.Context, in UpdateTargetInput) (Target, error) {
	row, err := r.queries.UpdateDeploymentTarget(ctx, db.UpdateDeploymentTargetParams{
		WorkspaceID: pgUUID(in.WorkspaceID), ID: pgUUID(in.ID), Name: in.Name, Environment: in.Environment,
		ExternalID: pgText(in.ExternalID), Config: jsonBytes(in.Config),
	})
	return mapTarget(row), mapQueryError(err)
}

func (r *PostgresRepository) DeleteTarget(ctx context.Context, workspaceID, id string) error {
	return mapQueryError(r.queries.DeleteDeploymentTarget(ctx, db.DeleteDeploymentTargetParams{WorkspaceID: parseID(workspaceID), ID: parseID(id)}))
}

func (r *PostgresRepository) CreateOperation(ctx context.Context, in CreateOperationInput) (Operation, bool, error) {
	in.OperationKind = strings.TrimSpace(in.OperationKind)
	if in.OperationKind == "" {
		in.OperationKind = strings.TrimSpace(in.Action)
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		if in.TargetID == uuid.Nil || strings.TrimSpace(in.SourceRevision) == "" || strings.TrimSpace(in.Environment) == "" || in.OperationKind == "" {
			return Operation{}, false, ErrIdempotencyKeyRequired
		}
		in.IdempotencyKey = DeriveIdempotencyKey(in.TargetID, in.SourceRevision, in.Environment, in.OperationKind, in.ApprovalReference)
	}
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 3
	}
	row, err := r.queries.CreateDeploymentOperation(ctx, db.CreateDeploymentOperationParams{
		WorkspaceID: pgUUID(in.WorkspaceID), TargetID: pgUUID(in.TargetID), Action: in.Action, IdempotencyKey: in.IdempotencyKey,
		SourceRevision: in.SourceRevision, Environment: in.Environment, OperationKind: in.OperationKind, ApprovalReference: in.ApprovalReference,
		MaxAttempts: in.MaxAttempts, Request: jsonBytes(in.Request), ID: pgUUID(in.ID),
	})
	if err == nil {
		return mapOperation(row), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, mapQueryError(err)
	}
	row, err = r.queries.GetDeploymentOperationByIdempotency(ctx, db.GetDeploymentOperationByIdempotencyParams{
		WorkspaceID: pgUUID(in.WorkspaceID), TargetID: pgUUID(in.TargetID), SourceRevision: in.SourceRevision, Environment: in.Environment,
		OperationKind: in.OperationKind, ApprovalReference: in.ApprovalReference,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = r.queries.GetDeploymentOperationByKey(ctx, db.GetDeploymentOperationByKeyParams{WorkspaceID: pgUUID(in.WorkspaceID), IdempotencyKey: in.IdempotencyKey})
	}
	return mapOperation(row), false, mapQueryError(err)
}

func (r *PostgresRepository) ListOperations(ctx context.Context, workspaceID, targetID string) ([]Operation, error) {
	rows, err := r.queries.ListDeploymentOperations(ctx, db.ListDeploymentOperationsParams{WorkspaceID: parseID(workspaceID), TargetID: parseID(targetID)})
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]Operation, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapOperation(row))
	}
	return out, nil
}

func (r *PostgresRepository) GetOperation(ctx context.Context, workspaceID, id string) (Operation, error) {
	row, err := r.queries.GetDeploymentOperation(ctx, db.GetDeploymentOperationParams{WorkspaceID: parseID(workspaceID), ID: parseID(id)})
	return mapOperation(row), mapQueryError(err)
}

func (r *PostgresRepository) GetOperationByProviderOperation(ctx context.Context, workspaceID, providerOperationID string) (Operation, error) {
	row, err := r.queries.GetDeploymentOperationByProviderOperation(ctx, db.GetDeploymentOperationByProviderOperationParams{
		WorkspaceID: parseID(workspaceID), ProviderOperationID: pgText(&providerOperationID),
	})
	return mapOperation(row), mapQueryError(err)
}

func (r *PostgresRepository) ListNonTerminalOperations(ctx context.Context, workspaceID string) ([]Operation, error) {
	if strings.TrimSpace(workspaceID) == "" {
		rows, err := r.conn.Query(ctx, `
			SELECT id, workspace_id, target_id, action, idempotency_key, status,
			       attempt_count, max_attempts, next_attempt_at, lease_owner,
			       lease_expires_at, provider_operation_id, request, evidence,
			       last_error, revision, created_at, updated_at, source_revision,
			       environment, operation_kind, approval_reference
			FROM deployment_operation
			WHERE status NOT IN ('ready', 'succeeded', 'failed', 'cancelled', 'canceled')`)
		if err != nil {
			return nil, mapQueryError(err)
		}
		defer rows.Close()
		out := make([]Operation, 0)
		for rows.Next() {
			var row db.DeploymentOperation
			if err := rows.Scan(
				&row.ID, &row.WorkspaceID, &row.TargetID, &row.Action, &row.IdempotencyKey, &row.Status,
				&row.AttemptCount, &row.MaxAttempts, &row.NextAttemptAt, &row.LeaseOwner,
				&row.LeaseExpiresAt, &row.ProviderOperationID, &row.Request, &row.Evidence,
				&row.LastError, &row.Revision, &row.CreatedAt, &row.UpdatedAt, &row.SourceRevision,
				&row.Environment, &row.OperationKind, &row.ApprovalReference,
			); err != nil {
				return nil, mapQueryError(err)
			}
			out = append(out, mapOperation(row))
		}
		if err := rows.Err(); err != nil {
			return nil, mapQueryError(err)
		}
		return out, nil
	}
	rows, err := r.queries.ListNonTerminalDeploymentOperations(ctx, parseID(workspaceID))
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]Operation, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapOperation(row))
	}
	return out, nil
}

func (r *PostgresRepository) UpdateOperation(ctx context.Context, next Operation, expectedRevision int64) (Operation, error) {
	current, err := r.GetOperation(ctx, next.WorkspaceID.String(), next.ID.String())
	if err != nil {
		return Operation{}, err
	}
	if current.Revision != expectedRevision {
		return Operation{}, ErrRevisionConflict
	}
	if err := ValidateTransition(current.Status, next.Status); err != nil {
		return Operation{}, err
	}
	row, err := r.queries.UpdateDeploymentOperation(ctx, db.UpdateDeploymentOperationParams{
		WorkspaceID: pgUUID(next.WorkspaceID), ID: pgUUID(next.ID), Status: string(next.Status), AttemptCount: next.AttemptCount, MaxAttempts: next.MaxAttempts,
		NextAttemptAt: pgTime(next.NextAttemptAt), LeaseOwner: pgText(next.LeaseOwner), LeaseExpiresAt: pgTime(next.LeaseExpiresAt),
		ProviderOperationID: pgText(next.ProviderOperationID), Evidence: jsonBytes(next.Evidence), LastError: pgText(next.LastError), Revision: expectedRevision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, ErrRevisionConflict
	}
	return mapOperation(row), mapQueryError(err)
}

func (r *PostgresRepository) AppendEvent(ctx context.Context, in AppendEventInput) (Event, bool, error) {
	if strings.TrimSpace(in.DedupeKey) == "" {
		return Event{}, false, ErrEventDedupeKeyRequired
	}
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}
	var observed *string
	if in.ObservedStatus != nil {
		value := string(*in.ObservedStatus)
		observed = &value
	}
	row, err := r.queries.CreateDeploymentEvent(ctx, db.CreateDeploymentEventParams{
		WorkspaceID: pgUUID(in.WorkspaceID), OperationID: pgUUID(in.OperationID), Provider: in.Provider, ProviderEventID: pgText(in.ProviderEventID),
		DedupeKey: in.DedupeKey, Kind: in.Kind, ObservedStatus: pgText(observed), Payload: jsonBytes(in.Payload), Applied: in.Applied, ID: pgUUID(in.ID),
	})
	if err == nil {
		return mapEvent(row), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Event{}, false, mapQueryError(err)
	}
	if in.ProviderEventID != nil && strings.TrimSpace(in.Provider) != "" {
		row, err = r.queries.GetDeploymentEventByProviderEvent(ctx, db.GetDeploymentEventByProviderEventParams{Provider: in.Provider, ProviderEventID: pgText(in.ProviderEventID)})
	} else {
		row, err = r.queries.GetDeploymentEventByDedupeKey(ctx, db.GetDeploymentEventByDedupeKeyParams{WorkspaceID: pgUUID(in.WorkspaceID), DedupeKey: in.DedupeKey})
	}
	return mapEvent(row), false, mapQueryError(err)
}

func (r *PostgresRepository) ListEvents(ctx context.Context, workspaceID, operationID string) ([]Event, error) {
	rows, err := r.queries.ListDeploymentEvents(ctx, db.ListDeploymentEventsParams{WorkspaceID: parseID(workspaceID), OperationID: parseID(operationID)})
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapEvent(row))
	}
	return out, nil
}

func mapProviderConnection(row db.DeploymentProviderConnection) ProviderConnection {
	return ProviderConnection{ID: uuidFromPG(row.ID), WorkspaceID: uuidFromPG(row.WorkspaceID), Provider: row.Provider, Name: row.Name, SecretRef: stringFromPG(row.SecretRef), WebhookSecretRef: stringFromPG(row.WebhookSecretRef), Config: cloneJSON(row.Config), Enabled: row.Enabled, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}
func mapTarget(row db.DeploymentTarget) Target {
	return Target{ID: uuidFromPG(row.ID), WorkspaceID: uuidFromPG(row.WorkspaceID), ConnectionID: uuidFromPG(row.ConnectionID), Name: row.Name, Environment: row.Environment, ExternalID: stringFromPG(row.ExternalID), Config: cloneJSON(row.Config), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}
func mapOperation(row db.DeploymentOperation) Operation {
	return Operation{ID: uuidFromPG(row.ID), WorkspaceID: uuidFromPG(row.WorkspaceID), TargetID: uuidFromPG(row.TargetID), Action: row.Action, IdempotencyKey: row.IdempotencyKey, SourceRevision: row.SourceRevision, Environment: row.Environment, OperationKind: row.OperationKind, ApprovalReference: row.ApprovalReference, Status: OperationStatus(row.Status), AttemptCount: row.AttemptCount, MaxAttempts: row.MaxAttempts, NextAttemptAt: timeFromPG(row.NextAttemptAt), LeaseOwner: stringFromPG(row.LeaseOwner), LeaseExpiresAt: timeFromPG(row.LeaseExpiresAt), ProviderOperationID: stringFromPG(row.ProviderOperationID), Request: cloneJSON(row.Request), Evidence: cloneJSON(row.Evidence), LastError: stringFromPG(row.LastError), Revision: row.Revision, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}
func mapEvent(row db.DeploymentEvent) Event {
	var status *OperationStatus
	if value := stringFromPG(row.ObservedStatus); value != nil {
		converted := OperationStatus(*value)
		status = &converted
	}
	return Event{ID: uuidFromPG(row.ID), WorkspaceID: uuidFromPG(row.WorkspaceID), OperationID: uuidFromPG(row.OperationID), Provider: row.Provider, ProviderEventID: stringFromPG(row.ProviderEventID), DedupeKey: row.DedupeKey, Kind: row.Kind, ObservedStatus: status, Payload: cloneJSON(row.Payload), Applied: row.Applied, CreatedAt: row.CreatedAt.Time}
}
func mapQueryError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func parseID(value string) pgtype.UUID {
	id, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgUUID(id)
}
func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil} }
func uuidFromPG(value pgtype.UUID) uuid.UUID {
	if !value.Valid {
		return uuid.Nil
	}
	return uuid.UUID(value.Bytes)
}
func pgText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}
func stringFromPG(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
func pgTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
func timeFromPG(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
func jsonBytes(value json.RawMessage) []byte {
	if len(value) == 0 {
		return []byte("{}")
	}
	return append([]byte(nil), value...)
}
func cloneJSON(value []byte) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("{}")
	}
	return append(json.RawMessage(nil), value...)
}
