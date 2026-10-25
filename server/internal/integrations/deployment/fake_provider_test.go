package deployment

import (
	"context"
	"errors"
	"sync"
	"testing"

	core "github.com/multica-ai/multica/server/internal/deployment"
)

func TestFakeProviderLifecycleIsControllable(t *testing.T) {
	p := NewFakeProvider()
	op, err := p.TriggerDeployment(context.Background(), core.DeploymentRequest{CommitSHA: "abc123"})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []FakeStatus{FakeStatusBuilding, FakeStatusDeploying, FakeStatusReady} {
		if err := p.Advance(op.ProviderOperationID); err != nil {
			t.Fatalf("advance to %q: %v", want, err)
		}
		got, err := p.GetOperation(context.Background(), core.OperationLookup{ProviderOperationID: op.ProviderOperationID})
		if err != nil {
			t.Fatal(err)
		}
		if got.ExternalStatus != string(want) {
			t.Fatalf("status = %q, want %q", got.ExternalStatus, want)
		}
	}

	if err := p.Fail(op.ProviderOperationID); !errors.Is(err, core.ErrOutOfOrderTransition) {
		t.Fatalf("terminal regression error = %v, want ErrOutOfOrderTransition", err)
	}
}

func TestFakeProviderFailureAndWebhook(t *testing.T) {
	p := NewFakeProvider()
	op, err := p.TriggerDeployment(context.Background(), core.DeploymentRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Fail(op.ProviderOperationID); err != nil {
		t.Fatal(err)
	}

	event, err := p.ParseWebhook(context.Background(), core.WebhookRequest{RawBody: []byte(`{"event_id":"evt-1","event_type":"deployment.failed","operation_id":"fake-operation-1","status":"failed"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if event.ObservedStatus != core.StatusFailed {
		t.Fatalf("webhook status = %q, want %q", event.ObservedStatus, core.StatusFailed)
	}
}

func TestFakeProviderAdvanceIsAtomic(t *testing.T) {
	p := NewFakeProvider()
	op, err := p.TriggerDeployment(context.Background(), core.DeploymentRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.Advance(op.ProviderOperationID)
		}()
	}
	wg.Wait()

	got, err := p.GetOperation(context.Background(), core.OperationLookup{ProviderOperationID: op.ProviderOperationID})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalStatus != string(FakeStatusDeploying) {
		t.Fatalf("status = %q, want %q", got.ExternalStatus, FakeStatusDeploying)
	}
}
