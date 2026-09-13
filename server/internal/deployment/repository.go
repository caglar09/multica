package deployment

import (
	"context"
	"errors"
)

var (
	ErrNotFound               = errors.New("deployment resource not found")
	ErrRevisionConflict       = errors.New("deployment operation revision conflict")
	ErrInvalidTransition      = errors.New("invalid deployment operation transition")
	ErrIdempotencyKeyRequired = errors.New("deployment idempotency key is required")
	ErrEventDedupeKeyRequired = errors.New("deployment event dedupe key is required")
	ErrInvalidOperationStatus = errors.New("invalid deployment operation status")
)

type Repository interface {
	CreateProviderConnection(context.Context, CreateProviderConnectionInput) (ProviderConnection, error)
	GetProviderConnection(context.Context, string, string) (ProviderConnection, error)
	CreateTarget(context.Context, CreateTargetInput) (Target, error)
	GetTarget(context.Context, string, string) (Target, error)
	CreateOperation(context.Context, CreateOperationInput) (operation Operation, created bool, err error)
	GetOperation(context.Context, string, string) (Operation, error)
	UpdateOperation(context.Context, Operation, int64) (Operation, error)
	AppendEvent(context.Context, AppendEventInput) (event Event, created bool, err error)
}
