package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type DBTX interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type PostgresRepository struct {
	db DBTX
}

func NewPostgresRepository(db DBTX) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateProviderConnection(ctx context.Context, in CreateProviderConnectionInput) (ProviderConnection, error) {
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO deployment_provider_connection (
			id, workspace_id, provider, name, secret_ref, config, enabled
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::jsonb, $7)
		RETURNING id::text, workspace_id::text, provider, name, secret_ref, config, enabled, created_at, updated_at
	`, in.ID.String(), in.WorkspaceID.String(), in.Provider, in.Name, in.SecretRef, jsonText(in.Config), in.Enabled)

	return scanProviderConnection(row)
}

func (r *PostgresRepository) GetProviderConnection(ctx context.Context, workspaceID, id string) (ProviderConnection, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, provider, name, secret_ref, config, enabled, created_at, updated_at
		FROM deployment_provider_connection
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, id)

	return scanProviderConnection(row)
}

func (r *PostgresRepository) CreateTarget(ctx context.Context, in CreateTargetInput) (Target, error) {
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO deployment_target (
			id, workspace_id, connection_id, name, environment, external_id, config
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::jsonb)
		RETURNING id::text, workspace_id::text, connection_id::text, name, environment, external_id, config, created_at, updated_at
	`, in.ID.String(), in.WorkspaceID.String(), in.ConnectionID.String(), in.Name, in.Environment, in.ExternalID, jsonText(in.Config))

	return scanTarget(row)
}

func (r *PostgresRepository) GetTarget(ctx context.Context, workspaceID, id string) (Target, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, connection_id::text, name, environment, external_id, config, created_at, updated_at
		FROM deployment_target
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, id)

	return scanTarget(row)
}

func (r *PostgresRepository) CreateOperation(ctx context.Context, in CreateOperationInput) (Operation, bool, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return Operation{}, false, ErrIdempotencyKeyRequired
	}
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 3
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO deployment_operation (
			id, workspace_id, target_id, action, idempotency_key, status, max_attempts, request
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'queued', $6, $7::jsonb)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
		RETURNING `+operationColumns, in.ID.String(), in.WorkspaceID.String(), in.TargetID.String(), in.Action, in.IdempotencyKey, in.MaxAttempts, jsonText(in.Request))

	op, err := scanOperation(row)
	if err == nil {
		return op, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Operation{}, false, err
	}

	row = r.db.QueryRow(ctx, `
		SELECT `+operationColumns+`
		FROM deployment_operation
		WHERE workspace_id = $1::uuid AND idempotency_key = $2
	`, in.WorkspaceID.String(), in.IdempotencyKey)
	op, err = scanOperation(row)
	if err != nil {
		return Operation{}, false, err
	}
	return op, false, nil
}

func (r *PostgresRepository) GetOperation(ctx context.Context, workspaceID, id string) (Operation, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+operationColumns+`
		FROM deployment_operation
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, id)

	return scanOperation(row)
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

	row := r.db.QueryRow(ctx, `
		UPDATE deployment_operation
		SET status = $1,
			attempt_count = $2,
			max_attempts = $3,
			next_attempt_at = $4,
			lease_owner = $5,
			lease_expires_at = $6,
			provider_operation_id = $7,
			evidence = $8::jsonb,
			last_error = $9,
			revision = revision + 1,
			updated_at = now()
		WHERE workspace_id = $10::uuid AND id = $11::uuid AND revision = $12
		RETURNING `+operationColumns,
		next.Status, next.AttemptCount, next.MaxAttempts, next.NextAttemptAt, next.LeaseOwner,
		next.LeaseExpiresAt, next.ProviderOperationID, jsonText(next.Evidence), next.LastError,
		next.WorkspaceID.String(), next.ID.String(), expectedRevision,
	)

	updated, err := scanOperation(row)
	if errors.Is(err, ErrNotFound) {
		return Operation{}, ErrRevisionConflict
	}
	return updated, err
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

	row := r.db.QueryRow(ctx, `
		INSERT INTO deployment_event (
			id, workspace_id, operation_id, provider_event_id, dedupe_key, kind, observed_status, payload, applied
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::jsonb, $9)
		ON CONFLICT (workspace_id, dedupe_key) DO NOTHING
		RETURNING `+eventColumns,
		in.ID.String(), in.WorkspaceID.String(), in.OperationID.String(), in.ProviderEventID,
		in.DedupeKey, in.Kind, observed, jsonText(in.Payload), in.Applied,
	)

	event, err := scanEvent(row)
	if err == nil {
		return event, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Event{}, false, err
	}

	row = r.db.QueryRow(ctx, `
		SELECT `+eventColumns+`
		FROM deployment_event
		WHERE workspace_id = $1::uuid AND dedupe_key = $2
	`, in.WorkspaceID.String(), in.DedupeKey)
	event, err = scanEvent(row)
	if err != nil {
		return Event{}, false, err
	}
	return event, false, nil
}

const operationColumns = `
	id::text, workspace_id::text, target_id::text, action, idempotency_key, status,
	attempt_count, max_attempts, next_attempt_at, lease_owner, lease_expires_at,
	provider_operation_id, request, evidence, last_error, revision, created_at, updated_at
`

const eventColumns = `
	id::text, workspace_id::text, operation_id::text, provider_event_id, dedupe_key,
	kind, observed_status, payload, applied, created_at
`

func scanProviderConnection(row pgx.Row) (ProviderConnection, error) {
	var out ProviderConnection
	var id, workspaceID string
	var secretRef pgtype.Text
	var config []byte
	if err := row.Scan(&id, &workspaceID, &out.Provider, &out.Name, &secretRef, &config, &out.Enabled, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return ProviderConnection{}, mapScanError(err)
	}

	var err error
	out.ID, err = uuid.Parse(id)
	if err != nil {
		return ProviderConnection{}, fmt.Errorf("parse deployment provider connection id: %w", err)
	}
	out.WorkspaceID, err = uuid.Parse(workspaceID)
	if err != nil {
		return ProviderConnection{}, fmt.Errorf("parse deployment provider connection workspace id: %w", err)
	}
	if secretRef.Valid {
		value := secretRef.String
		out.SecretRef = &value
	}
	out.Config = cloneJSON(config)
	return out, nil
}

func scanTarget(row pgx.Row) (Target, error) {
	var out Target
	var id, workspaceID, connectionID string
	var externalID pgtype.Text
	var config []byte
	if err := row.Scan(&id, &workspaceID, &connectionID, &out.Name, &out.Environment, &externalID, &config, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return Target{}, mapScanError(err)
	}

	var err error
	out.ID, err = uuid.Parse(id)
	if err != nil {
		return Target{}, fmt.Errorf("parse deployment target id: %w", err)
	}
	out.WorkspaceID, err = uuid.Parse(workspaceID)
	if err != nil {
		return Target{}, fmt.Errorf("parse deployment target workspace id: %w", err)
	}
	out.ConnectionID, err = uuid.Parse(connectionID)
	if err != nil {
		return Target{}, fmt.Errorf("parse deployment target connection id: %w", err)
	}
	if externalID.Valid {
		value := externalID.String
		out.ExternalID = &value
	}
	out.Config = cloneJSON(config)
	return out, nil
}

func scanOperation(row pgx.Row) (Operation, error) {
	var out Operation
	var id, workspaceID, targetID, status string
	var nextAttemptAt, leaseExpiresAt pgtype.Timestamptz
	var leaseOwner, providerOperationID, lastError pgtype.Text
	var request, evidence []byte

	if err := row.Scan(
		&id, &workspaceID, &targetID, &out.Action, &out.IdempotencyKey, &status,
		&out.AttemptCount, &out.MaxAttempts, &nextAttemptAt, &leaseOwner, &leaseExpiresAt,
		&providerOperationID, &request, &evidence, &lastError, &out.Revision, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return Operation{}, mapScanError(err)
	}

	var err error
	out.ID, err = uuid.Parse(id)
	if err != nil {
		return Operation{}, fmt.Errorf("parse deployment operation id: %w", err)
	}
	out.WorkspaceID, err = uuid.Parse(workspaceID)
	if err != nil {
		return Operation{}, fmt.Errorf("parse deployment operation workspace id: %w", err)
	}
	out.TargetID, err = uuid.Parse(targetID)
	if err != nil {
		return Operation{}, fmt.Errorf("parse deployment operation target id: %w", err)
	}
	out.Status = OperationStatus(status)
	if !isKnownStatus(out.Status) {
		return Operation{}, fmt.Errorf("%w: %q", ErrInvalidOperationStatus, status)
	}
	out.NextAttemptAt = nullableTime(nextAttemptAt)
	out.LeaseExpiresAt = nullableTime(leaseExpiresAt)
	out.LeaseOwner = nullableString(leaseOwner)
	out.ProviderOperationID = nullableString(providerOperationID)
	out.LastError = nullableString(lastError)
	out.Request = cloneJSON(request)
	out.Evidence = cloneJSON(evidence)
	return out, nil
}

func scanEvent(row pgx.Row) (Event, error) {
	var out Event
	var id, workspaceID, operationID string
	var providerEventID, observedStatus pgtype.Text
	var payload []byte
	if err := row.Scan(
		&id, &workspaceID, &operationID, &providerEventID, &out.DedupeKey,
		&out.Kind, &observedStatus, &payload, &out.Applied, &out.CreatedAt,
	); err != nil {
		return Event{}, mapScanError(err)
	}

	var err error
	out.ID, err = uuid.Parse(id)
	if err != nil {
		return Event{}, fmt.Errorf("parse deployment event id: %w", err)
	}
	out.WorkspaceID, err = uuid.Parse(workspaceID)
	if err != nil {
		return Event{}, fmt.Errorf("parse deployment event workspace id: %w", err)
	}
	out.OperationID, err = uuid.Parse(operationID)
	if err != nil {
		return Event{}, fmt.Errorf("parse deployment event operation id: %w", err)
	}
	out.ProviderEventID = nullableString(providerEventID)
	if observedStatus.Valid {
		status := OperationStatus(observedStatus.String)
		if !isKnownStatus(status) {
			return Event{}, fmt.Errorf("%w: %q", ErrInvalidOperationStatus, status)
		}
		out.ObservedStatus = &status
	}
	out.Payload = cloneJSON(payload)
	return out, nil
}

func mapScanError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func isKnownStatus(status OperationStatus) bool {
	switch status {
	case StatusQueued, StatusDispatching, StatusRunning, StatusRetryWait, StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

func jsonText(value json.RawMessage) string {
	if len(value) == 0 {
		return "{}"
	}
	return string(value)
}

func cloneJSON(value []byte) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("{}")
	}
	out := make([]byte, len(value))
	copy(out, value)
	return json.RawMessage(out)
}

func nullableString(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	out := value.Time
	return &out
}
