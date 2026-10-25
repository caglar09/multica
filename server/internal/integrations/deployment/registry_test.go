package deployment

import (
	"context"
	"errors"
	"sync"
	"testing"

	core "github.com/multica-ai/multica/server/internal/deployment"
)

type mockProvider struct {
	kind string
	caps core.CapabilitySet
}

func (m *mockProvider) Kind() string {
	return m.kind
}

func (m *mockProvider) Capabilities() core.CapabilitySet {
	return m.caps
}

func (m *mockProvider) ValidateConnection(context.Context, core.ConnectionCredentials) (core.ConnectionInfo, error) {
	return core.ConnectionInfo{DisplayName: "Mock Connection"}, nil
}

func (m *mockProvider) ListTargets(context.Context, core.ProviderConnection, core.ConnectionCredentials) ([]core.RemoteTarget, error) {
	return nil, nil
}

func (m *mockProvider) GetTarget(context.Context, core.ProviderConnection, core.ConnectionCredentials, string) (core.RemoteTarget, error) {
	return core.RemoteTarget{Name: "mock-target"}, nil
}

func (m *mockProvider) CreateTarget(context.Context, core.ProviderConnection, core.ConnectionCredentials, core.CreateTargetRequest) (core.RemoteTarget, error) {
	return core.RemoteTarget{Name: "created-target"}, nil
}

func (m *mockProvider) TriggerDeployment(context.Context, core.DeploymentRequest) (core.RemoteOperation, error) {
	return core.RemoteOperation{
		ProviderOperationID: "op_123",
		NormalizedStatus:    core.StatusRunning,
	}, nil
}

func (m *mockProvider) GetOperation(context.Context, core.OperationLookup) (core.RemoteOperation, error) {
	return core.RemoteOperation{
		ProviderOperationID: "op_123",
		NormalizedStatus:    core.StatusSucceeded,
	}, nil
}

func (m *mockProvider) CancelOperation(context.Context, core.CancelRequest) (core.RemoteOperation, error) {
	return core.RemoteOperation{
		ProviderOperationID: "op_123",
		NormalizedStatus:    core.StatusCanceled,
	}, nil
}

func (m *mockProvider) ParseWebhook(context.Context, core.WebhookRequest) (core.ProviderEvent, error) {
	return core.ProviderEvent{
		ProviderEventID: "evt_123",
		ObservedStatus:  core.StatusSucceeded,
	}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	p := &mockProvider{
		kind: "mock",
		caps: core.CapabilitySet{
			Trigger:  true,
			Status:   true,
			Evidence: true,
		},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Registering same kind must fail
	if err := r.Register(p); err == nil {
		t.Fatalf("Register duplicate must fail")
	}

	// Nil provider must fail
	if err := r.Register(nil); err == nil {
		t.Fatalf("Register nil must fail")
	}

	got, err := r.Get("mock")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Kind() != "mock" {
		t.Fatalf("Get returned kind %q, want %q", got.Kind(), "mock")
	}

	// Non-existent kind must return ErrProviderNotFound
	_, err = r.Get("unknown")
	if !errors.Is(err, core.ErrProviderNotFound) {
		t.Fatalf("Get unknown returned %v, want ErrProviderNotFound", err)
	}
}

func TestRegistry_List(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	p1 := &mockProvider{kind: "vercel"}
	p2 := &mockProvider{kind: "render"}

	_ = r.Register(p1)
	_ = r.Register(p2)

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("List length = %d, want 2", len(list))
	}
	if list[0] != "render" || list[1] != "vercel" {
		t.Fatalf("List sorted unexpected: %v", list)
	}
}

func TestRegistry_EnsureCapability(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	p := &mockProvider{
		kind: "mock",
		caps: core.CapabilitySet{
			Trigger: true,
			Cancel:  false,
		},
	}
	_ = r.Register(p)

	// Trigger is supported
	err := r.EnsureCapability("mock", func(c core.CapabilitySet) bool {
		return c.Trigger
	})
	if err != nil {
		t.Fatalf("EnsureCapability(Trigger) returned error: %v", err)
	}

	// Cancel is unsupported
	err = r.EnsureCapability("mock", func(c core.CapabilitySet) bool {
		return c.Cancel
	})
	if !errors.Is(err, core.ErrCapabilityUnsupported) {
		t.Fatalf("EnsureCapability(Cancel) returned %v, want ErrCapabilityUnsupported", err)
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	p := &mockProvider{kind: "concurrent"}
	_ = r.Register(p)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Get("concurrent")
			_ = r.List()
		}()
	}
	wg.Wait()
}
