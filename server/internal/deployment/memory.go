package deployment

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryRepository is a thread-safe in-memory implementation of Repository
// and OperationLister for integration testing and local simulation.
type MemoryRepository struct {
	mu               sync.RWMutex
	connections      map[string]ProviderConnection
	targets          map[string]Target
	operations       map[string]Operation
	events           []Event
	eventsByDedupe   map[string]Event
	opsByIdempotency map[string]Operation
}

var (
	_ Repository      = (*MemoryRepository)(nil)
	_ OperationLister = (*MemoryRepository)(nil)
)

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		connections:      make(map[string]ProviderConnection),
		targets:          make(map[string]Target),
		operations:       make(map[string]Operation),
		eventsByDedupe:   make(map[string]Event),
		opsByIdempotency: make(map[string]Operation),
	}
}

func connKey(wsID, id string) string   { return wsID + ":" + id }
func targetKey(wsID, id string) string { return wsID + ":" + id }
func opKey(wsID, id string) string     { return wsID + ":" + id }

func (m *MemoryRepository) CreateProviderConnection(ctx context.Context, in CreateProviderConnectionInput) (ProviderConnection, error) {
	if err := ctx.Err(); err != nil {
		return ProviderConnection{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}
	now := time.Now().UTC()
	conn := ProviderConnection{
		ID:               in.ID,
		WorkspaceID:      in.WorkspaceID,
		Provider:         in.Provider,
		Name:             in.Name,
		SecretRef:        in.SecretRef,
		WebhookSecretRef: in.WebhookSecretRef,
		Config:           cloneJSON(in.Config),
		Enabled:          in.Enabled,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	m.connections[connKey(in.WorkspaceID.String(), in.ID.String())] = conn
	return conn, nil
}

func (m *MemoryRepository) ListProviderConnections(ctx context.Context, workspaceID string) ([]ProviderConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ProviderConnection
	for _, c := range m.connections {
		if c.WorkspaceID.String() == workspaceID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *MemoryRepository) GetProviderConnection(ctx context.Context, workspaceID, id string) (ProviderConnection, error) {
	if err := ctx.Err(); err != nil {
		return ProviderConnection{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.connections[connKey(workspaceID, id)]
	if !ok {
		return ProviderConnection{}, ErrNotFound
	}
	return c, nil
}

func (m *MemoryRepository) UpdateProviderConnection(ctx context.Context, in UpdateProviderConnectionInput) (ProviderConnection, error) {
	if err := ctx.Err(); err != nil {
		return ProviderConnection{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := connKey(in.WorkspaceID.String(), in.ID.String())
	c, ok := m.connections[k]
	if !ok {
		return ProviderConnection{}, ErrNotFound
	}
	c.Name = in.Name
	c.SecretRef = in.SecretRef
	c.WebhookSecretRef = in.WebhookSecretRef
	c.Config = cloneJSON(in.Config)
	c.Enabled = in.Enabled
	c.UpdatedAt = time.Now().UTC()
	m.connections[k] = c
	return c, nil
}

func (m *MemoryRepository) DeleteProviderConnection(ctx context.Context, workspaceID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := connKey(workspaceID, id)
	if _, ok := m.connections[k]; !ok {
		return ErrNotFound
	}
	delete(m.connections, k)
	return nil
}

func (m *MemoryRepository) CreateTarget(ctx context.Context, in CreateTargetInput) (Target, error) {
	if err := ctx.Err(); err != nil {
		return Target{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}
	now := time.Now().UTC()
	target := Target{
		ID:           in.ID,
		WorkspaceID:  in.WorkspaceID,
		ConnectionID: in.ConnectionID,
		Name:         in.Name,
		Environment:  in.Environment,
		ExternalID:   in.ExternalID,
		Config:       cloneJSON(in.Config),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.targets[targetKey(in.WorkspaceID.String(), in.ID.String())] = target
	return target, nil
}

func (m *MemoryRepository) ListTargets(ctx context.Context, workspaceID string) ([]Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Target
	for _, t := range m.targets {
		if t.WorkspaceID.String() == workspaceID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *MemoryRepository) GetTarget(ctx context.Context, workspaceID, id string) (Target, error) {
	if err := ctx.Err(); err != nil {
		return Target{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.targets[targetKey(workspaceID, id)]
	if !ok {
		return Target{}, ErrNotFound
	}
	return t, nil
}

func (m *MemoryRepository) UpdateTarget(ctx context.Context, in UpdateTargetInput) (Target, error) {
	if err := ctx.Err(); err != nil {
		return Target{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := targetKey(in.WorkspaceID.String(), in.ID.String())
	t, ok := m.targets[k]
	if !ok {
		return Target{}, ErrNotFound
	}
	t.Name = in.Name
	t.Environment = in.Environment
	t.ExternalID = in.ExternalID
	t.Config = cloneJSON(in.Config)
	t.UpdatedAt = time.Now().UTC()
	m.targets[k] = t
	return t, nil
}

func (m *MemoryRepository) DeleteTarget(ctx context.Context, workspaceID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := targetKey(workspaceID, id)
	if _, ok := m.targets[k]; !ok {
		return ErrNotFound
	}
	delete(m.targets, k)
	return nil
}

func (m *MemoryRepository) CreateOperation(ctx context.Context, in CreateOperationInput) (Operation, bool, error) {
	if err := ctx.Err(); err != nil {
		return Operation{}, false, err
	}
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

	m.mu.Lock()
	defer m.mu.Unlock()

	idempotencyKey := in.WorkspaceID.String() + ":" + in.IdempotencyKey
	if existing, ok := m.opsByIdempotency[idempotencyKey]; ok {
		return existing, false, nil
	}

	semanticKey := fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		in.WorkspaceID.String(), in.TargetID.String(), in.SourceRevision, in.Environment, in.OperationKind, in.ApprovalReference)
	if existing, ok := m.opsByIdempotency[semanticKey]; ok {
		return existing, false, nil
	}

	now := time.Now().UTC()
	op := Operation{
		ID:                in.ID,
		WorkspaceID:       in.WorkspaceID,
		TargetID:          in.TargetID,
		Action:            in.Action,
		IdempotencyKey:    in.IdempotencyKey,
		SourceRevision:    in.SourceRevision,
		Environment:       in.Environment,
		OperationKind:     in.OperationKind,
		ApprovalReference: in.ApprovalReference,
		Status:            StatusQueued,
		AttemptCount:      0,
		MaxAttempts:       in.MaxAttempts,
		Request:           cloneJSON(in.Request),
		Revision:          1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	m.operations[opKey(in.WorkspaceID.String(), in.ID.String())] = op
	m.opsByIdempotency[idempotencyKey] = op
	m.opsByIdempotency[semanticKey] = op
	return op, true, nil
}

func (m *MemoryRepository) ListOperations(ctx context.Context, workspaceID, targetID string) ([]Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Operation
	for _, op := range m.operations {
		if op.WorkspaceID.String() == workspaceID && op.TargetID.String() == targetID {
			out = append(out, op)
		}
	}
	return out, nil
}

func (m *MemoryRepository) GetOperation(ctx context.Context, workspaceID, id string) (Operation, error) {
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	op, ok := m.operations[opKey(workspaceID, id)]
	if !ok {
		return Operation{}, ErrNotFound
	}
	return op, nil
}

func (m *MemoryRepository) GetOperationByProviderOperation(ctx context.Context, workspaceID, providerOperationID string) (Operation, error) {
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, op := range m.operations {
		if op.WorkspaceID.String() == workspaceID && op.ProviderOperationID != nil && *op.ProviderOperationID == providerOperationID {
			return op, nil
		}
	}
	return Operation{}, ErrNotFound
}

func (m *MemoryRepository) ListNonTerminalOperations(ctx context.Context, workspaceID string) ([]Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Operation
	for _, op := range m.operations {
		if (workspaceID == "" || op.WorkspaceID.String() == workspaceID) && !op.Status.IsTerminal() {
			out = append(out, op)
		}
	}
	return out, nil
}

func (m *MemoryRepository) UpdateOperation(ctx context.Context, next Operation, expectedRevision int64) (Operation, error) {
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	k := opKey(next.WorkspaceID.String(), next.ID.String())
	current, ok := m.operations[k]
	if !ok {
		return Operation{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Operation{}, ErrRevisionConflict
	}
	if err := ValidateTransition(current.Status, next.Status); err != nil {
		return Operation{}, err
	}
	next.Revision = expectedRevision + 1
	next.UpdatedAt = time.Now().UTC()
	m.operations[k] = next

	idempotencyKey := next.WorkspaceID.String() + ":" + next.IdempotencyKey
	m.opsByIdempotency[idempotencyKey] = next

	return next, nil
}

func (m *MemoryRepository) AppendEvent(ctx context.Context, in AppendEventInput) (Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return Event{}, false, err
	}
	if strings.TrimSpace(in.DedupeKey) == "" {
		return Event{}, false, ErrEventDedupeKeyRequired
	}
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dedupeKey := in.WorkspaceID.String() + ":" + in.DedupeKey
	if existing, ok := m.eventsByDedupe[dedupeKey]; ok {
		return existing, false, nil
	}
	if in.ProviderEventID != nil && strings.TrimSpace(in.Provider) != "" {
		providerKey := in.Provider + ":" + *in.ProviderEventID
		if existing, ok := m.eventsByDedupe[providerKey]; ok {
			return existing, false, nil
		}
	}

	now := time.Now().UTC()
	event := Event{
		ID:              in.ID,
		WorkspaceID:     in.WorkspaceID,
		OperationID:     in.OperationID,
		Provider:        in.Provider,
		ProviderEventID: in.ProviderEventID,
		DedupeKey:       in.DedupeKey,
		Kind:            in.Kind,
		ObservedStatus:  in.ObservedStatus,
		Payload:         cloneJSON(in.Payload),
		Applied:         in.Applied,
		CreatedAt:       now,
	}

	m.events = append(m.events, event)
	m.eventsByDedupe[dedupeKey] = event
	if in.ProviderEventID != nil && strings.TrimSpace(in.Provider) != "" {
		m.eventsByDedupe[in.Provider+":"+*in.ProviderEventID] = event
	}
	return event, true, nil
}

func (m *MemoryRepository) ListEvents(ctx context.Context, workspaceID, operationID string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Event
	for _, e := range m.events {
		if e.WorkspaceID.String() == workspaceID && e.OperationID.String() == operationID {
			out = append(out, e)
		}
	}
	return out, nil
}
