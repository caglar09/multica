package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrCapabilityUnsupported              = errors.New("provider capability unsupported")
	ErrTargetCreationConfirmationRequired = errors.New("target creation requires confirmation")
	ErrProviderNotFound                   = errors.New("deployment provider not found")
	ErrTerminalStateImmutable             = errors.New("terminal deployment state cannot be modified")
	ErrOutOfOrderTransition               = errors.New("out-of-order provider event ignored")
)

// RetryableProviderError and FinalProviderError let the generic engine persist
// transient versus terminal provider failures without inspecting vendor
// payloads. Adapters wrap errors with the appropriate classification.
type RetryableProviderError struct{ Err error }

func (e *RetryableProviderError) Error() string { return fmt.Sprint(e.Err) }
func (e *RetryableProviderError) Unwrap() error { return e.Err }

type FinalProviderError struct{ Err error }

func (e *FinalProviderError) Error() string { return fmt.Sprint(e.Err) }
func (e *FinalProviderError) Unwrap() error { return e.Err }

// ProviderAPIError carries only the safe message and HTTP status returned by a
// provider API. Adapters must redact the message before constructing it.
type ProviderAPIError struct {
	StatusCode int
	Message    string
}

func (e *ProviderAPIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("provider request returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("provider request returned status %d: %s", e.StatusCode, e.Message)
}

// CapabilitySet defines the granular feature set supported by a deployment provider.
type CapabilitySet struct {
	Trigger         bool `json:"trigger"`
	Status          bool `json:"status"`
	Cancel          bool `json:"cancel"`
	Webhook         bool `json:"webhook"`
	Evidence        bool `json:"evidence"`
	Logs            bool `json:"logs"`
	Promote         bool `json:"promote"`
	Rollback        bool `json:"rollback"`
	TargetDiscovery bool `json:"target_discovery"`
	TargetCreate    bool `json:"target_create"`
}

// RemoteTarget represents a deployment target discovered or provisioned on the remote provider.
type RemoteTarget struct {
	ExternalID    string          `json:"external_id"`
	Name          string          `json:"name"`
	Environment   string          `json:"environment"`
	DefaultBranch string          `json:"default_branch,omitempty"`
	WebURL        string          `json:"web_url,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

// RemoteOperation represents the normalized result of triggering or inspecting an operation on the provider.
type RemoteOperation struct {
	ProviderOperationID string          `json:"provider_operation_id"`
	ExternalStatus      string          `json:"external_status"`
	NormalizedStatus    OperationStatus `json:"normalized_status"`
	DeploymentURL       string          `json:"deployment_url,omitempty"`
	InspectURL          string          `json:"inspect_url,omitempty"`
	Evidence            json.RawMessage `json:"evidence,omitempty"`
	ErrorMessage        string          `json:"error_message,omitempty"`
}

// ProviderEvent represents a normalized external event received via webhook or polling reconciliation.
type ProviderEvent struct {
	ProviderEventID     string          `json:"provider_event_id"`
	ProviderEventType   string          `json:"provider_event_type"`
	ProviderOperationID string          `json:"provider_operation_id"`
	ObservedStatus      OperationStatus `json:"observed_status"`
	Evidence            json.RawMessage `json:"evidence,omitempty"`
	RawPayload          json.RawMessage `json:"raw_payload,omitempty"`
}

// ConnectionCredentials provides decrypted secrets to provider adapters for outbound API calls.
type ConnectionCredentials struct {
	SecretRef string          `json:"secret_ref,omitempty"`
	Config    json.RawMessage `json:"-"`
	RawSecret []byte          `json:"-"`
}

// ConnectionInfo contains verified metadata about a remote provider account or workspace scope.
type ConnectionInfo struct {
	AccountID   string `json:"account_id"`
	ScopeID     string `json:"scope_id"`
	ScopeName   string `json:"scope_name"`
	DisplayName string `json:"display_name"`
}

// CreateTargetRequest specifies parameters for provisioning a new remote project or target.
type CreateTargetRequest struct {
	Name          string          `json:"name"`
	Environment   string          `json:"environment"`
	Repository    string          `json:"repository"`
	Branch        string          `json:"branch,omitempty"`
	Framework     string          `json:"framework,omitempty"`
	RootDirectory string          `json:"root_directory,omitempty"`
	BuildCommand  string          `json:"build_command,omitempty"`
	Confirmed     bool            `json:"confirmed"`
	Confirm       bool            `json:"confirm,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
}

// TargetDeleter is implemented by providers that can compensate a remote
// target when local persistence fails after creation.
type TargetDeleter interface {
	DeleteTarget(ctx context.Context, conn ProviderConnection, creds ConnectionCredentials, externalID string) error
}

// DeploymentRequest carries instructions to trigger a source revision deployment.
type DeploymentRequest struct {
	WorkspaceID    uuid.UUID             `json:"workspace_id"`
	OperationID    uuid.UUID             `json:"operation_id"`
	TargetID       uuid.UUID             `json:"target_id"`
	ExternalTarget RemoteTarget          `json:"external_target"`
	IdempotencyKey string                `json:"idempotency_key"`
	Environment    string                `json:"environment"`
	CommitSHA      string                `json:"commit_sha"`
	Branch         string                `json:"branch,omitempty"`
	CommitMessage  string                `json:"commit_message,omitempty"`
	Credentials    ConnectionCredentials `json:"-"`
	Config         json.RawMessage       `json:"config,omitempty"`
}

// OperationLookup queries the current status of an external operation.
type OperationLookup struct {
	WorkspaceID         uuid.UUID             `json:"workspace_id"`
	OperationID         uuid.UUID             `json:"operation_id"`
	ProviderOperationID string                `json:"provider_operation_id"`
	ExternalTarget      RemoteTarget          `json:"external_target"`
	Credentials         ConnectionCredentials `json:"-"`
}

// CancelRequest aborts an active remote operation.
type CancelRequest struct {
	WorkspaceID         uuid.UUID             `json:"workspace_id"`
	OperationID         uuid.UUID             `json:"operation_id"`
	ProviderOperationID string                `json:"provider_operation_id"`
	ExternalTarget      RemoteTarget          `json:"external_target"`
	Credentials         ConnectionCredentials `json:"-"`
}

// WebhookRequest contains inbound HTTP webhook request components for signature verification and normalization.
type WebhookRequest struct {
	Headers       map[string][]string `json:"headers"`
	RawBody       []byte              `json:"raw_body"`
	SigningSecret []byte              `json:"-"`
}

// PromotionRequest requests promoting a completed preview deployment to production.
type PromotionRequest struct {
	WorkspaceID          uuid.UUID             `json:"workspace_id"`
	CandidateOperationID uuid.UUID             `json:"candidate_operation_id"`
	ProviderOperationID  string                `json:"provider_operation_id"`
	ExternalTarget       RemoteTarget          `json:"external_target"`
	ApprovalReference    string                `json:"approval_reference"`
	Credentials          ConnectionCredentials `json:"-"`
}

// RollbackRequest requests rolling back production traffic to an earlier deployment.
type RollbackRequest struct {
	WorkspaceID         uuid.UUID             `json:"workspace_id"`
	TargetOperationID   uuid.UUID             `json:"target_operation_id"`
	ProviderOperationID string                `json:"provider_operation_id"`
	ExternalTarget      RemoteTarget          `json:"external_target"`
	ApprovalReference   string                `json:"approval_reference"`
	Credentials         ConnectionCredentials `json:"-"`
}

// LogRequest specifies boundaries for fetching deployment build/runtime logs.
type LogRequest struct {
	WorkspaceID         uuid.UUID             `json:"workspace_id"`
	OperationID         uuid.UUID             `json:"operation_id"`
	ProviderOperationID string                `json:"provider_operation_id"`
	ExternalTarget      RemoteTarget          `json:"external_target"`
	Credentials         ConnectionCredentials `json:"-"`
	Since               *time.Time            `json:"since,omitempty"`
	Limit               int                   `json:"limit,omitempty"`
}

// LogResult returns captured build or runtime logs.
type LogResult struct {
	Logs      []string `json:"logs"`
	NextToken string   `json:"next_token,omitempty"`
	Completed bool     `json:"completed"`
}

// RestartRequest requests restarting a deployment runtime or container.
type RestartRequest struct {
	WorkspaceID         uuid.UUID             `json:"workspace_id"`
	OperationID         uuid.UUID             `json:"operation_id"`
	ProviderOperationID string                `json:"provider_operation_id"`
	ExternalTarget      RemoteTarget          `json:"external_target"`
	Credentials         ConnectionCredentials `json:"-"`
}

// Provider defines the core contract that every deployment provider adapter must satisfy.
type Provider interface {
	Kind() string
	Capabilities() CapabilitySet

	ValidateConnection(ctx context.Context, creds ConnectionCredentials) (ConnectionInfo, error)
	ListTargets(ctx context.Context, conn ProviderConnection, creds ConnectionCredentials) ([]RemoteTarget, error)
	GetTarget(ctx context.Context, conn ProviderConnection, creds ConnectionCredentials, externalID string) (RemoteTarget, error)
	CreateTarget(ctx context.Context, conn ProviderConnection, creds ConnectionCredentials, req CreateTargetRequest) (RemoteTarget, error)

	TriggerDeployment(ctx context.Context, req DeploymentRequest) (RemoteOperation, error)
	GetOperation(ctx context.Context, req OperationLookup) (RemoteOperation, error)
	CancelOperation(ctx context.Context, req CancelRequest) (RemoteOperation, error)
	ParseWebhook(ctx context.Context, req WebhookRequest) (ProviderEvent, error)
}

// PromotionProvider is an optional interface implemented by providers supporting preview-to-production promotion.
type PromotionProvider interface {
	Promote(ctx context.Context, req PromotionRequest) (RemoteOperation, error)
}

// RollbackProvider is an optional interface implemented by providers supporting instant rollbacks.
type RollbackProvider interface {
	Rollback(ctx context.Context, req RollbackRequest) (RemoteOperation, error)
}

// LogProvider is an optional interface implemented by providers supporting direct log retrieval.
type LogProvider interface {
	GetLogs(ctx context.Context, req LogRequest) (LogResult, error)
}

// RestartProvider is an optional interface implemented by providers supporting runtime restarts.
type RestartProvider interface {
	Restart(ctx context.Context, req RestartRequest) (RemoteOperation, error)
}
