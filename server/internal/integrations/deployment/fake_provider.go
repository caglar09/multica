package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	core "github.com/multica-ai/multica/server/internal/deployment"
)

// Provider and CapabilitySet are re-exported from the provider-neutral core so
// integration consumers do not need to know where the contract is stored.
type Provider = core.Provider
type CapabilitySet = core.CapabilitySet

var (
	ErrFakeOperationNotFound = errors.New("fake deployment operation not found")
	ErrFakeTargetNotFound    = errors.New("fake deployment target not found")
	ErrInvalidFakeStatus     = errors.New("invalid fake deployment status")
)

// FakeStatus is the provider-facing lifecycle used by FakeProvider.
type FakeStatus string

const (
	FakeStatusQueued    FakeStatus = "queued"
	FakeStatusBuilding  FakeStatus = "building"
	FakeStatusDeploying FakeStatus = "deploying"
	FakeStatusReady     FakeStatus = "ready"
	FakeStatusFailed    FakeStatus = "failed"
	FakeStatusCancelled FakeStatus = "cancelled"
)

// FakeProvider is a deterministic, in-memory provider for integration tests.
// It never stores or returns credentials.
type FakeProvider struct {
	mu         sync.RWMutex
	sequence   atomic.Uint64
	operations map[string]fakeOperation
	targets    map[string]core.RemoteTarget
}

type fakeOperation struct {
	request core.DeploymentRequest
	status  FakeStatus
}

// NewFakeProvider creates an empty fake provider.
func NewFakeProvider() *FakeProvider {
	return &FakeProvider{
		operations: make(map[string]fakeOperation),
		targets:    make(map[string]core.RemoteTarget),
	}
}

func (p *FakeProvider) Kind() string { return "fake" }

func (p *FakeProvider) Capabilities() core.CapabilitySet {
	return core.CapabilitySet{
		Trigger:         true,
		Status:          true,
		Cancel:          true,
		Webhook:         true,
		Evidence:        true,
		Logs:            true,
		Promote:         true,
		Rollback:        true,
		TargetDiscovery: true,
		TargetCreate:    true,
	}
}

func (p *FakeProvider) ValidateConnection(ctx context.Context, _ core.ConnectionCredentials) (core.ConnectionInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.ConnectionInfo{}, err
	}
	return core.ConnectionInfo{
		AccountID:   "fake-account",
		ScopeID:     "fake-scope",
		ScopeName:   "Fake",
		DisplayName: "Fake Provider",
	}, nil
}

func (p *FakeProvider) ListTargets(ctx context.Context, _ core.ProviderConnection, _ core.ConnectionCredentials) ([]core.RemoteTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	targets := make([]core.RemoteTarget, 0, len(p.targets))
	for _, target := range p.targets {
		target.Metadata = cloneJSON(target.Metadata)
		targets = append(targets, target)
	}
	return targets, nil
}

func (p *FakeProvider) GetTarget(ctx context.Context, _ core.ProviderConnection, _ core.ConnectionCredentials, externalID string) (core.RemoteTarget, error) {
	if err := ctx.Err(); err != nil {
		return core.RemoteTarget{}, err
	}

	p.mu.RLock()
	target, ok := p.targets[externalID]
	p.mu.RUnlock()
	if !ok {
		return core.RemoteTarget{}, fmt.Errorf("%w: %q", ErrFakeTargetNotFound, externalID)
	}
	target.Metadata = cloneJSON(target.Metadata)
	return target, nil
}

func (p *FakeProvider) CreateTarget(ctx context.Context, _ core.ProviderConnection, _ core.ConnectionCredentials, req core.CreateTargetRequest) (core.RemoteTarget, error) {
	if err := ctx.Err(); err != nil {
		return core.RemoteTarget{}, err
	}
	if !req.Confirmed && !req.Confirm {
		return core.RemoteTarget{}, core.ErrTargetCreationConfirmationRequired
	}

	id := fmt.Sprintf("fake-target-%d", p.sequence.Add(1))
	target := core.RemoteTarget{
		ExternalID:    id,
		Name:          req.Name,
		Environment:   req.Environment,
		DefaultBranch: firstNonEmpty(req.Branch, "main"),
		WebURL:        "https://fake.example.test/" + id,
		Metadata:      cloneJSON(req.Config),
	}
	p.mu.Lock()
	if p.targets == nil {
		p.targets = make(map[string]core.RemoteTarget)
	}
	p.targets[id] = target
	p.mu.Unlock()
	target.Metadata = cloneJSON(target.Metadata)
	return target, nil
}

func (p *FakeProvider) DeleteTarget(ctx context.Context, _ core.ProviderConnection, _ core.ConnectionCredentials, externalID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.targets[externalID]; !ok {
		return ErrFakeTargetNotFound
	}
	delete(p.targets, externalID)
	return nil
}

func (p *FakeProvider) TriggerDeployment(ctx context.Context, req core.DeploymentRequest) (core.RemoteOperation, error) {
	if err := ctx.Err(); err != nil {
		return core.RemoteOperation{}, err
	}

	id := fmt.Sprintf("fake-operation-%d", p.sequence.Add(1))
	p.mu.Lock()
	if p.operations == nil {
		p.operations = make(map[string]fakeOperation)
	}
	p.operations[id] = fakeOperation{request: req, status: FakeStatusQueued}
	p.mu.Unlock()
	return p.operation(id, FakeStatusQueued), nil
}

func (p *FakeProvider) GetOperation(ctx context.Context, req core.OperationLookup) (core.RemoteOperation, error) {
	if err := ctx.Err(); err != nil {
		return core.RemoteOperation{}, err
	}
	return p.currentOperation(req.ProviderOperationID)
}

func (p *FakeProvider) CancelOperation(ctx context.Context, req core.CancelRequest) (core.RemoteOperation, error) {
	if err := ctx.Err(); err != nil {
		return core.RemoteOperation{}, err
	}
	if err := p.SetStatus(req.ProviderOperationID, FakeStatusCancelled); err != nil {
		return core.RemoteOperation{}, err
	}
	return p.currentOperation(req.ProviderOperationID)
}

// SetStatus moves an operation through the fake provider lifecycle. Repeating
// the current status is idempotent; backward and skipped transitions fail.
func (p *FakeProvider) SetStatus(operationID string, status FakeStatus) error {
	if !validFakeStatus(status) {
		return fmt.Errorf("%w: %q", ErrInvalidFakeStatus, status)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	op, ok := p.operations[operationID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrFakeOperationNotFound, operationID)
	}
	if op.status == status {
		return nil
	}
	if !canFakeTransition(op.status, status) {
		return fmt.Errorf("%w: %q -> %q", core.ErrOutOfOrderTransition, op.status, status)
	}
	op.status = status
	p.operations[operationID] = op
	return nil
}

// Advance performs the next successful lifecycle transition.
func (p *FakeProvider) Advance(operationID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	op, ok := p.operations[operationID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrFakeOperationNotFound, operationID)
	}
	next := map[FakeStatus]FakeStatus{
		FakeStatusQueued:    FakeStatusBuilding,
		FakeStatusBuilding:  FakeStatusDeploying,
		FakeStatusDeploying: FakeStatusReady,
	}[op.status]
	if next == "" {
		return fmt.Errorf("%w: %q cannot advance", core.ErrOutOfOrderTransition, op.status)
	}
	op.status = next
	p.operations[operationID] = op
	return nil
}

// Fail marks an operation as failed from any non-terminal lifecycle state.
func (p *FakeProvider) Fail(operationID string) error {
	return p.SetStatus(operationID, FakeStatusFailed)
}

func (p *FakeProvider) ParseWebhook(ctx context.Context, req core.WebhookRequest) (core.ProviderEvent, error) {
	if err := ctx.Err(); err != nil {
		return core.ProviderEvent{}, err
	}
	var payload struct {
		EventID     string     `json:"event_id"`
		EventType   string     `json:"event_type"`
		OperationID string     `json:"operation_id"`
		Status      FakeStatus `json:"status"`
	}
	if err := json.Unmarshal(req.RawBody, &payload); err != nil {
		return core.ProviderEvent{}, fmt.Errorf("decode fake webhook: %w", err)
	}
	if !validFakeStatus(payload.Status) {
		return core.ProviderEvent{}, fmt.Errorf("%w: %q", ErrInvalidFakeStatus, payload.Status)
	}
	return core.ProviderEvent{
		ProviderEventID:     payload.EventID,
		ProviderEventType:   payload.EventType,
		ProviderOperationID: payload.OperationID,
		ObservedStatus:      normalizeFakeStatus(payload.Status),
		RawPayload:          cloneJSON(req.RawBody),
	}, nil
}

func (p *FakeProvider) Promote(ctx context.Context, req core.PromotionRequest) (core.RemoteOperation, error) {
	return p.transitionOperation(ctx, req.ProviderOperationID, FakeStatusReady)
}

func (p *FakeProvider) Rollback(ctx context.Context, req core.RollbackRequest) (core.RemoteOperation, error) {
	if err := ctx.Err(); err != nil {
		return core.RemoteOperation{}, err
	}
	return p.currentOperation(req.ProviderOperationID)
}

func (p *FakeProvider) GetLogs(ctx context.Context, req core.LogRequest) (core.LogResult, error) {
	if err := ctx.Err(); err != nil {
		return core.LogResult{}, err
	}
	if _, err := p.currentOperation(req.ProviderOperationID); err != nil {
		return core.LogResult{}, err
	}
	return core.LogResult{Logs: []string{"fake deployment started", "fake deployment completed"}, Completed: true}, nil
}

func (p *FakeProvider) transitionOperation(ctx context.Context, operationID string, status FakeStatus) (core.RemoteOperation, error) {
	if err := ctx.Err(); err != nil {
		return core.RemoteOperation{}, err
	}
	if err := p.SetStatus(operationID, status); err != nil {
		return core.RemoteOperation{}, err
	}
	return p.currentOperation(operationID)
}

func (p *FakeProvider) currentOperation(operationID string) (core.RemoteOperation, error) {
	p.mu.RLock()
	op, ok := p.operations[operationID]
	p.mu.RUnlock()
	if !ok {
		return core.RemoteOperation{}, fmt.Errorf("%w: %q", ErrFakeOperationNotFound, operationID)
	}
	return p.operation(operationID, op.status), nil
}

func (p *FakeProvider) operation(operationID string, status FakeStatus) core.RemoteOperation {
	p.mu.RLock()
	op := p.operations[operationID]
	p.mu.RUnlock()
	return core.RemoteOperation{
		ProviderOperationID: operationID,
		ExternalStatus:      string(status),
		NormalizedStatus:    normalizeFakeStatus(status),
		DeploymentURL:       fmt.Sprintf("https://fake.example.test/deployments/%s", operationID),
		InspectURL:          fmt.Sprintf("https://fake.example.test/operations/%s", operationID),
		Evidence:            json.RawMessage(fmt.Sprintf(`{"commit_sha":%q}`, op.request.CommitSHA)),
	}
}

func validFakeStatus(status FakeStatus) bool {
	switch status {
	case FakeStatusQueued, FakeStatusBuilding, FakeStatusDeploying, FakeStatusReady, FakeStatusFailed, FakeStatusCancelled:
		return true
	default:
		return false
	}
}

func canFakeTransition(from, to FakeStatus) bool {
	switch from {
	case FakeStatusQueued:
		return to == FakeStatusBuilding || to == FakeStatusFailed || to == FakeStatusCancelled
	case FakeStatusBuilding:
		return to == FakeStatusDeploying || to == FakeStatusFailed || to == FakeStatusCancelled
	case FakeStatusDeploying:
		return to == FakeStatusReady || to == FakeStatusFailed || to == FakeStatusCancelled
	case FakeStatusReady, FakeStatusFailed, FakeStatusCancelled:
		return false
	default:
		return false
	}
}

func normalizeFakeStatus(status FakeStatus) core.OperationStatus {
	switch status {
	case FakeStatusQueued:
		return core.StatusQueued
	case FakeStatusBuilding:
		return core.StatusBuilding
	case FakeStatusDeploying:
		return core.StatusDeploying
	case FakeStatusReady:
		return core.StatusReady
	case FakeStatusFailed:
		return core.StatusFailed
	case FakeStatusCancelled:
		return core.StatusCancelled
	default:
		return ""
	}
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
