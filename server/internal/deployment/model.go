package deployment

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type OperationStatus string

const (
	StatusPending   OperationStatus = "pending"
	StatusQueued    OperationStatus = "queued"
	StatusBuilding  OperationStatus = "building"
	StatusDeploying OperationStatus = "deploying"
	StatusReady     OperationStatus = "ready"
	StatusCancelled OperationStatus = "cancelled"

	// These values are retained for rows written by the first deployment-core
	// migration. New operations use the normalized values above.
	StatusDispatching OperationStatus = "dispatching"
	StatusRunning     OperationStatus = "running"
	StatusRetryWait   OperationStatus = "retry_wait"
	StatusSucceeded   OperationStatus = "succeeded"
	StatusFailed      OperationStatus = "failed"
	StatusCanceled    OperationStatus = "canceled"
)

func (s OperationStatus) IsTerminal() bool {
	switch s {
	case StatusReady, StatusSucceeded, StatusFailed, StatusCanceled, StatusCancelled:
		return true
	default:
		return false
	}
}

type ProviderConnection struct {
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	Provider         string
	Name             string
	SecretRef        *string
	WebhookSecretRef *string
	Config           json.RawMessage
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Target struct {
	ID           uuid.UUID
	WorkspaceID  uuid.UUID
	ConnectionID uuid.UUID
	Name         string
	Environment  string
	ExternalID   *string
	Config       json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Operation struct {
	ID                  uuid.UUID
	WorkspaceID         uuid.UUID
	TargetID            uuid.UUID
	Action              string
	IdempotencyKey      string
	SourceRevision      string
	Environment         string
	OperationKind       string
	ApprovalReference   string
	Status              OperationStatus
	AttemptCount        int32
	MaxAttempts         int32
	NextAttemptAt       *time.Time
	LeaseOwner          *string
	LeaseExpiresAt      *time.Time
	ProviderOperationID *string
	Request             json.RawMessage
	Evidence            json.RawMessage
	LastError           *string
	Revision            int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Event struct {
	ID              uuid.UUID
	WorkspaceID     uuid.UUID
	OperationID     uuid.UUID
	Provider        string
	ProviderEventID *string
	DedupeKey       string
	Kind            string
	ObservedStatus  *OperationStatus
	Payload         json.RawMessage
	Applied         bool
	CreatedAt       time.Time
}

type CreateProviderConnectionInput struct {
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	Provider         string
	Name             string
	SecretRef        *string
	WebhookSecretRef *string
	Config           json.RawMessage
	Enabled          bool
}

type CreateTargetInput struct {
	ID           uuid.UUID
	WorkspaceID  uuid.UUID
	ConnectionID uuid.UUID
	Name         string
	Environment  string
	ExternalID   *string
	Config       json.RawMessage
}

type CreateOperationInput struct {
	ID                uuid.UUID
	WorkspaceID       uuid.UUID
	TargetID          uuid.UUID
	Action            string
	IdempotencyKey    string
	SourceRevision    string
	Environment       string
	OperationKind     string
	ApprovalReference string
	MaxAttempts       int32
	Request           json.RawMessage
}

type UpdateProviderConnectionInput struct {
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	Name             string
	SecretRef        *string
	WebhookSecretRef *string
	Config           json.RawMessage
	Enabled          bool
}

type UpdateTargetInput struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	Name        string
	Environment string
	ExternalID  *string
	Config      json.RawMessage
}

type AppendEventInput struct {
	ID              uuid.UUID
	WorkspaceID     uuid.UUID
	OperationID     uuid.UUID
	Provider        string
	ProviderEventID *string
	DedupeKey       string
	Kind            string
	ObservedStatus  *OperationStatus
	Payload         json.RawMessage
	Applied         bool
}
