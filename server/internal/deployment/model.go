package deployment

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type OperationStatus string

const (
	StatusQueued      OperationStatus = "queued"
	StatusDispatching OperationStatus = "dispatching"
	StatusRunning     OperationStatus = "running"
	StatusRetryWait   OperationStatus = "retry_wait"
	StatusSucceeded   OperationStatus = "succeeded"
	StatusFailed      OperationStatus = "failed"
	StatusCanceled    OperationStatus = "canceled"
)

func (s OperationStatus) IsTerminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

type ProviderConnection struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	Provider    string
	Name        string
	SecretRef   *string
	Config      json.RawMessage
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
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
	ProviderEventID *string
	DedupeKey       string
	Kind            string
	ObservedStatus  *OperationStatus
	Payload         json.RawMessage
	Applied         bool
	CreatedAt       time.Time
}

type CreateProviderConnectionInput struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	Provider    string
	Name        string
	SecretRef   *string
	Config      json.RawMessage
	Enabled     bool
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
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	TargetID       uuid.UUID
	Action         string
	IdempotencyKey string
	MaxAttempts    int32
	Request        json.RawMessage
}

type AppendEventInput struct {
	ID              uuid.UUID
	WorkspaceID     uuid.UUID
	OperationID     uuid.UUID
	ProviderEventID *string
	DedupeKey       string
	Kind            string
	ObservedStatus  *OperationStatus
	Payload         json.RawMessage
	Applied         bool
}
