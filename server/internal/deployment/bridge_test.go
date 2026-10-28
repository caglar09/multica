package deployment

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAutonomousDeployment_UsesGenericEngine(t *testing.T) {
	repository := NewMemoryRepository()
	provider := &genericEngineTestProvider{}
	dispatcher := &AsyncDispatcher{Repository: repository}
	workspaceID, projectID, operationID := uuid.New(), uuid.New(), uuid.New()
	config := json.RawMessage(`{"version":1,"plan_id":"00000000-0000-4000-8000-000000000001","manifest_id":"00000000-0000-4000-8000-000000000002","manifest_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifact_storage_key":"autonomous-project/releases/sha256/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.tar"}`)
	request := DeploymentRequest{
		WorkspaceID: workspaceID, OperationID: operationID, TargetID: projectID,
		IdempotencyKey: "autonomous-test-" + operationID.String(), Environment: "production",
		CommitSHA: strings.Repeat("c", 40), Config: config,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal operation request: %v", err)
	}
	operation, created, err := dispatcher.Dispatch(context.Background(), CreateOperationInput{
		ID: operationID, WorkspaceID: workspaceID, TargetID: projectID,
		Action: "deploy", OperationKind: "autonomous_project_deployment",
		IdempotencyKey: request.IdempotencyKey, SourceRevision: strings.Repeat("a", 64),
		Environment: "production", MaxAttempts: 3, Request: encoded,
	}, request, provider)
	if err != nil {
		t.Fatalf("dispatch generic operation: %v", err)
	}
	if !created || operation.ID != operationID {
		t.Fatalf("generic operation = (%s, created=%v), want %s and created", operation.ID, created, operationID)
	}
	if strings.Contains(strings.ToLower(string(operation.Request)), "artifact_url") || strings.Contains(string(operation.Request), "signature=") {
		t.Fatalf("persisted generic request contains a presigned URL: %s", operation.Request)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err := repository.GetOperation(context.Background(), workspaceID.String(), operationID.String())
		if err != nil {
			t.Fatalf("load generic operation result: %v", err)
		}
		if current.Status.IsTerminal() {
			if current.Status != StatusSucceeded {
				t.Fatalf("generic operation status = %s, want succeeded (error=%v)", current.Status, current.LastError)
			}
			if got := provider.calls.Load(); got != 1 {
				t.Fatalf("provider trigger count = %d, want 1", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("generic operation did not reach a terminal state")
}

type genericEngineTestProvider struct{ calls atomic.Int32 }

func (*genericEngineTestProvider) Kind() string                { return "generic-engine-test" }
func (*genericEngineTestProvider) Capabilities() CapabilitySet { return CapabilitySet{Trigger: true} }
func (*genericEngineTestProvider) ValidateConnection(context.Context, ConnectionCredentials) (ConnectionInfo, error) {
	return ConnectionInfo{}, ErrCapabilityUnsupported
}
func (*genericEngineTestProvider) ListTargets(context.Context, ProviderConnection, ConnectionCredentials) ([]RemoteTarget, error) {
	return nil, ErrCapabilityUnsupported
}
func (*genericEngineTestProvider) GetTarget(context.Context, ProviderConnection, ConnectionCredentials, string) (RemoteTarget, error) {
	return RemoteTarget{}, ErrCapabilityUnsupported
}
func (*genericEngineTestProvider) CreateTarget(context.Context, ProviderConnection, ConnectionCredentials, CreateTargetRequest) (RemoteTarget, error) {
	return RemoteTarget{}, ErrCapabilityUnsupported
}
func (p *genericEngineTestProvider) TriggerDeployment(_ context.Context, request DeploymentRequest) (RemoteOperation, error) {
	p.calls.Add(1)
	if strings.Contains(string(request.Config), "artifact_url") {
		return RemoteOperation{}, &FinalProviderError{Err: ErrInvalidTransition}
	}
	evidence, _ := json.Marshal(map[string]any{"manifest_digest": strings.Repeat("a", 64)})
	return RemoteOperation{
		ProviderOperationID: request.OperationID.String(), ExternalStatus: "succeeded",
		NormalizedStatus: StatusSucceeded, Evidence: evidence,
	}, nil
}
func (*genericEngineTestProvider) GetOperation(context.Context, OperationLookup) (RemoteOperation, error) {
	return RemoteOperation{}, ErrCapabilityUnsupported
}
func (*genericEngineTestProvider) CancelOperation(context.Context, CancelRequest) (RemoteOperation, error) {
	return RemoteOperation{}, ErrCapabilityUnsupported
}
func (*genericEngineTestProvider) ParseWebhook(context.Context, WebhookRequest) (ProviderEvent, error) {
	return ProviderEvent{}, ErrCapabilityUnsupported
}
