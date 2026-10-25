package deployment

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	core "github.com/multica-ai/multica/server/internal/deployment"
)

// TestDuplicateWebhookDeliveryResultsInSingleStateMutation verifies that
// receiving the exact same webhook payload multiple times (webhook replay / duplicate delivery)
// results in only a single state mutation on the deployment operation, and does not create
// duplicate events in the audit event store.
func TestDuplicateWebhookDeliveryResultsInSingleStateMutation(t *testing.T) {
	ctx := context.Background()
	repo := core.NewMemoryRepository()
	fake := NewFakeProvider()

	registry := NewRegistry()
	if err := registry.Register(fake); err != nil {
		t.Fatalf("register fake provider: %v", err)
	}

	workspaceID := uuid.New()
	targetID := uuid.New()
	operationID := uuid.New()

	// 1. Seed target in workspace
	_, err := repo.CreateTarget(ctx, core.CreateTargetInput{
		ID:          targetID,
		WorkspaceID: workspaceID,
		Name:        "production-web",
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	// 2. Trigger deployment on fake provider
	remoteOp, err := fake.TriggerDeployment(ctx, core.DeploymentRequest{
		CommitSHA: "abc12345",
	})
	if err != nil {
		t.Fatalf("trigger fake deployment: %v", err)
	}

	// 3. Persist initial deployment operation in repository (StatusQueued)
	op, created, err := repo.CreateOperation(ctx, core.CreateOperationInput{
		ID:             operationID,
		WorkspaceID:    workspaceID,
		TargetID:       targetID,
		Action:         "deploy",
		IdempotencyKey: "test-idemp-1",
		SourceRevision: "abc12345",
		Environment:    "production",
		OperationKind:  "deploy",
	})
	if err != nil || !created {
		t.Fatalf("create operation: %v (created=%v)", err, created)
	}

	// Attach the provider operation ID to the operation
	op.ProviderOperationID = &remoteOp.ProviderOperationID
	op, err = repo.UpdateOperation(ctx, op, op.Revision)
	if err != nil {
		t.Fatalf("update operation with provider id: %v", err)
	}
	initialRevision := op.Revision

	// Setup EventGateway
	gateway := &core.EventGateway{
		Repository: repo,
		Providers:  registry,
		Find: func(ctx context.Context, wsID, provName, provOpID string) (core.Operation, error) {
			return repo.GetOperationByProviderOperation(ctx, wsID, provOpID)
		},
	}

	// 4. Construct a webhook payload moving operation from queued to building
	webhookBody := []byte(`{
		"event_id": "evt-dup-test-001",
		"event_type": "deployment.building",
		"operation_id": "` + remoteOp.ProviderOperationID + `",
		"status": "building"
	}`)

	// Delivery 1: Initial delivery
	res1, err := gateway.Handle(ctx, workspaceID.String(), "fake", nil, nil, webhookBody)
	if err != nil {
		t.Fatalf("first webhook delivery failed: %v", err)
	}
	if res1.Duplicate {
		t.Fatalf("first delivery marked as duplicate unexpectedly: %+v", res1)
	}
	if !res1.Applied {
		t.Fatalf("first delivery should have been applied: %+v", res1)
	}

	// Verify operation mutated to StatusBuilding and revision incremented exactly once
	opAfterFirst, err := repo.GetOperation(ctx, workspaceID.String(), operationID.String())
	if err != nil {
		t.Fatalf("get operation after first delivery: %v", err)
	}
	if opAfterFirst.Status != core.StatusBuilding {
		t.Fatalf("status after first delivery = %q, want %q", opAfterFirst.Status, core.StatusBuilding)
	}
	if opAfterFirst.Revision != initialRevision+1 {
		t.Fatalf("revision after first delivery = %d, want %d", opAfterFirst.Revision, initialRevision+1)
	}

	// Verify exactly 1 event recorded
	eventsFirst, err := repo.ListEvents(ctx, workspaceID.String(), operationID.String())
	if err != nil {
		t.Fatalf("list events after first delivery: %v", err)
	}
	if len(eventsFirst) != 1 {
		t.Fatalf("events count after first delivery = %d, want 1", len(eventsFirst))
	}

	// Delivery 2: Duplicate delivery (exact replay)
	res2, err := gateway.Handle(ctx, workspaceID.String(), "fake", nil, nil, webhookBody)
	if err != nil {
		t.Fatalf("second webhook delivery returned error: %v", err)
	}
	if !res2.Duplicate {
		t.Fatalf("second delivery should have been flagged as duplicate: %+v", res2)
	}
	if res2.Applied {
		t.Fatalf("second delivery should NOT have been applied: %+v", res2)
	}

	// Delivery 3: Third duplicate delivery
	res3, err := gateway.Handle(ctx, workspaceID.String(), "fake", nil, nil, webhookBody)
	if err != nil {
		t.Fatalf("third webhook delivery returned error: %v", err)
	}
	if !res3.Duplicate {
		t.Fatalf("third delivery should have been flagged as duplicate: %+v", res3)
	}

	// Verify operation state was NOT mutated again
	opAfterReplays, err := repo.GetOperation(ctx, workspaceID.String(), operationID.String())
	if err != nil {
		t.Fatalf("get operation after replays: %v", err)
	}
	if opAfterReplays.Status != core.StatusBuilding {
		t.Fatalf("status changed after duplicate deliveries: got %q, want %q", opAfterReplays.Status, core.StatusBuilding)
	}
	if opAfterReplays.Revision != opAfterFirst.Revision {
		t.Fatalf("revision changed after duplicate deliveries: got %d, want %d", opAfterReplays.Revision, opAfterFirst.Revision)
	}

	// Verify audit log has NO duplicate events
	eventsAfterReplays, err := repo.ListEvents(ctx, workspaceID.String(), operationID.String())
	if err != nil {
		t.Fatalf("list events after replays: %v", err)
	}
	if len(eventsAfterReplays) != 1 {
		t.Fatalf("duplicate delivery resulted in %d events in audit store, want exactly 1", len(eventsAfterReplays))
	}
}

// TestLateArrivalBuildingEventAfterSucceededDoesNotRegressTerminalState verifies
// that out-of-order webhook delivery (e.g. late arrival of a "building" event after
// the operation has already reached terminal "ready" / "succeeded") is safely ignored and does
// not regress or mutate the terminal state.
func TestLateArrivalBuildingEventAfterSucceededDoesNotRegressTerminalState(t *testing.T) {
	ctx := context.Background()
	repo := core.NewMemoryRepository()
	fake := NewFakeProvider()

	registry := NewRegistry()
	if err := registry.Register(fake); err != nil {
		t.Fatalf("register fake provider: %v", err)
	}

	workspaceID := uuid.New()
	targetID := uuid.New()
	operationID := uuid.New()

	// 1. Seed target & operation
	_, _ = repo.CreateTarget(ctx, core.CreateTargetInput{
		ID:          targetID,
		WorkspaceID: workspaceID,
		Name:        "web-service",
		Environment: "preview",
	})

	remoteOp, err := fake.TriggerDeployment(ctx, core.DeploymentRequest{CommitSHA: "sha999"})
	if err != nil {
		t.Fatalf("trigger fake: %v", err)
	}

	op, _, err := repo.CreateOperation(ctx, core.CreateOperationInput{
		ID:             operationID,
		WorkspaceID:    workspaceID,
		TargetID:       targetID,
		Action:         "deploy",
		IdempotencyKey: "test-idemp-2",
		SourceRevision: "sha999",
		Environment:    "preview",
		OperationKind:  "deploy",
	})
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	op.ProviderOperationID = &remoteOp.ProviderOperationID
	op, _ = repo.UpdateOperation(ctx, op, op.Revision)

	gateway := &core.EventGateway{
		Repository: repo,
		Providers:  registry,
		Find: func(ctx context.Context, wsID, provName, provOpID string) (core.Operation, error) {
			return repo.GetOperationByProviderOperation(ctx, wsID, provOpID)
		},
	}

	// 2. Transition through valid lifecycle: queued -> building -> ready
	buildingWebhook := []byte(`{
		"event_id": "evt-normal-building",
		"event_type": "deployment.building",
		"operation_id": "` + remoteOp.ProviderOperationID + `",
		"status": "building"
	}`)
	resBuilding, err := gateway.Handle(ctx, workspaceID.String(), "fake", nil, nil, buildingWebhook)
	if err != nil {
		t.Fatalf("building webhook failed: %v", err)
	}
	if !resBuilding.Applied {
		t.Fatalf("building webhook was not applied: %+v", resBuilding)
	}

	readyWebhook := []byte(`{
		"event_id": "evt-terminal-ready",
		"event_type": "deployment.ready",
		"operation_id": "` + remoteOp.ProviderOperationID + `",
		"status": "ready"
	}`)
	resReady, err := gateway.Handle(ctx, workspaceID.String(), "fake", nil, nil, readyWebhook)
	if err != nil {
		t.Fatalf("ready webhook failed: %v", err)
	}
	if !resReady.Applied {
		t.Fatalf("ready webhook was not applied: %+v", resReady)
	}

	// Verify operation reached terminal StatusReady
	opTerminal, err := repo.GetOperation(ctx, workspaceID.String(), operationID.String())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if opTerminal.Status != core.StatusReady {
		t.Fatalf("terminal status = %q, want %q", opTerminal.Status, core.StatusReady)
	}
	if !opTerminal.Status.IsTerminal() {
		t.Fatalf("status %q should report IsTerminal() == true", opTerminal.Status)
	}
	terminalRevision := opTerminal.Revision

	// 3. Simulate late arrival of "building" webhook (network delay / retry from provider)
	lateBuildingWebhook := []byte(`{
		"event_id": "evt-late-building-002",
		"event_type": "deployment.building",
		"operation_id": "` + remoteOp.ProviderOperationID + `",
		"status": "building"
	}`)

	resLate, err := gateway.Handle(ctx, workspaceID.String(), "fake", nil, nil, lateBuildingWebhook)
	if err != nil {
		t.Fatalf("late building webhook returned error: %v", err)
	}
	// The late event should be recorded in audit log with Applied=false, but NOT applied to operation
	if resLate.Applied {
		t.Fatalf("late building event was applied to terminal state: %+v", resLate)
	}

	// 4. Also simulate late arrival of "queued" webhook
	lateQueuedWebhook := []byte(`{
		"event_id": "evt-late-queued-003",
		"event_type": "deployment.queued",
		"operation_id": "` + remoteOp.ProviderOperationID + `",
		"status": "queued"
	}`)
	resQueued, err := gateway.Handle(ctx, workspaceID.String(), "fake", nil, nil, lateQueuedWebhook)
	if err != nil {
		t.Fatalf("late queued webhook returned error: %v", err)
	}
	if resQueued.Applied {
		t.Fatalf("late queued event was applied to terminal state: %+v", resQueued)
	}

	// 5. Verify the operation's terminal state was completely preserved
	opAfterLateEvents, err := repo.GetOperation(ctx, workspaceID.String(), operationID.String())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if opAfterLateEvents.Status != core.StatusReady {
		t.Fatalf("terminal state regressed: got %q, want %q", opAfterLateEvents.Status, core.StatusReady)
	}
	if opAfterLateEvents.Revision != terminalRevision {
		t.Fatalf("operation was mutated by late events: revision got %d, want %d", opAfterLateEvents.Revision, terminalRevision)
	}
}

// TestSimulatedBackendRestartResumedAndReconciledByBackgroundWorker simulates
// a scenario where the backend process crashes/restarts while a deployment operation
// is actively building/deploying. After restart, the background Reconciler worker
// polls due non-terminal operations, queries the remote provider, and successfully
// reconciles the operation to completion.
func TestSimulatedBackendRestartResumedAndReconciledByBackgroundWorker(t *testing.T) {
	ctx := context.Background()
	repo := core.NewMemoryRepository()
	fake := NewFakeProvider()

	workspaceID := uuid.New()
	targetID := uuid.New()
	operationID := uuid.New()

	// 1. Initial setup before crash
	_, _ = repo.CreateTarget(ctx, core.CreateTargetInput{
		ID:          targetID,
		WorkspaceID: workspaceID,
		Name:        "api-service",
		Environment: "production",
	})

	// Trigger deployment on FakeProvider
	remoteOp, err := fake.TriggerDeployment(ctx, core.DeploymentRequest{CommitSHA: "restart-commit-1"})
	if err != nil {
		t.Fatalf("trigger fake: %v", err)
	}

	// Operation created and placed in StatusBuilding
	op, _, err := repo.CreateOperation(ctx, core.CreateOperationInput{
		ID:             operationID,
		WorkspaceID:    workspaceID,
		TargetID:       targetID,
		Action:         "deploy",
		IdempotencyKey: "test-idemp-restart",
		SourceRevision: "restart-commit-1",
		Environment:    "production",
		OperationKind:  "deploy",
	})
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}

	op.ProviderOperationID = &remoteOp.ProviderOperationID
	op.Status = core.StatusBuilding
	// A delayed reconciliation must still accept a terminal provider result.
	op.CreatedAt = time.Now().Add(-2 * time.Hour)
	op, err = repo.UpdateOperation(ctx, op, op.Revision)
	if err != nil {
		t.Fatalf("update operation: %v", err)
	}

	// 2. SIMULATE BACKEND RESTART:
	// The in-memory process is terminated. No in-flight goroutines survive.
	// Meanwhile, on the remote provider side, the build progresses to completion (Ready):
	if err := fake.Advance(remoteOp.ProviderOperationID); err != nil { // queued -> building
		t.Fatalf("fake advance 1: %v", err)
	}
	if err := fake.Advance(remoteOp.ProviderOperationID); err != nil { // building -> deploying
		t.Fatalf("fake advance 2: %v", err)
	}
	if err := fake.Advance(remoteOp.ProviderOperationID); err != nil { // deploying -> ready
		t.Fatalf("fake advance 3: %v", err)
	}

	// 3. New backend instance boots up. Background Reconciler starts:
	resolveLookup := func(ctx context.Context, o core.Operation) (core.Provider, core.OperationLookup, error) {
		return fake, core.OperationLookup{
			ProviderOperationID: *o.ProviderOperationID,
		}, nil
	}

	reconciler := &core.Reconciler{
		Repository: repo,
		Lister:     repo,
		Resolve:    resolveLookup,
		Backoff:    time.Millisecond * 100,
		Deadline:   time.Hour,
	}

	// 4. Background worker runs reconciliation pass
	processed, err := reconciler.Reconcile(ctx, workspaceID.String())
	if err != nil {
		t.Fatalf("reconciler failed: %v", err)
	}
	if processed != 1 {
		t.Fatalf("reconciler processed %d operations, want 1", processed)
	}

	// 5. Verify the operation was successfully resumed and updated to StatusReady
	opAfterReconcile, err := repo.GetOperation(ctx, workspaceID.String(), operationID.String())
	if err != nil {
		t.Fatalf("get operation after reconcile: %v", err)
	}
	if opAfterReconcile.Status != core.StatusReady {
		t.Fatalf("status after reconciliation = %q, want %q", opAfterReconcile.Status, core.StatusReady)
	}
	if len(opAfterReconcile.Evidence) == 0 {
		t.Fatalf("reconciled operation has empty evidence: %+v", opAfterReconcile)
	}

	// 6. A subsequent reconciliation pass should find 0 non-terminal operations
	processedSecond, err := reconciler.Reconcile(ctx, workspaceID.String())
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}
	if processedSecond != 0 {
		t.Fatalf("second reconcile processed %d operations, want 0", processedSecond)
	}
}

// TestEndToEndAsyncDispatcherWithFakeProvider tests the full async dispatch flow:
// Dispatcher creates durable intent row, launches async goroutine, executes TriggerDeployment,
// and updates the repository operation with remote provider operation details.
func TestEndToEndAsyncDispatcherWithFakeProvider(t *testing.T) {
	ctx := context.Background()
	repo := core.NewMemoryRepository()
	fake := NewFakeProvider()

	workspaceID := uuid.New()
	targetID := uuid.New()
	opID := uuid.New()

	dispatcher := &core.AsyncDispatcher{
		Repository: repo,
		Workers:    2,
		Timeout:    5 * time.Second,
	}

	req := core.DeploymentRequest{
		CommitSHA: "sha-full-e2e",
		Credentials: core.ConnectionCredentials{
			RawSecret: []byte("sensitive-secret-token"),
		},
	}

	input := core.CreateOperationInput{
		ID:             opID,
		WorkspaceID:    workspaceID,
		TargetID:       targetID,
		Action:         "deploy",
		IdempotencyKey: "e2e-dispatch-key",
		SourceRevision: "sha-full-e2e",
		Environment:    "staging",
		OperationKind:  "deploy",
	}

	initialOp, created, err := dispatcher.Dispatch(ctx, input, req, fake)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if !created {
		t.Fatalf("operation not created")
	}
	if initialOp.Status != core.StatusQueued {
		t.Fatalf("initial status = %q, want %q", initialOp.Status, core.StatusQueued)
	}

	// Wait for async dispatch worker to finish
	var finalOp core.Operation
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		finalOp, _ = repo.GetOperation(ctx, workspaceID.String(), opID.String())
		if finalOp.ProviderOperationID != nil {
			break
		}
	}

	if finalOp.ProviderOperationID == nil {
		t.Fatalf("timed out waiting for async dispatch worker to update provider operation id")
	}

	// Verify remote operation exists on fake provider
	remote, err := fake.GetOperation(ctx, core.OperationLookup{ProviderOperationID: *finalOp.ProviderOperationID})
	if err != nil {
		t.Fatalf("get operation from fake: %v", err)
	}
	if remote.ExternalStatus != string(FakeStatusQueued) {
		t.Fatalf("fake status = %q, want %q", remote.ExternalStatus, FakeStatusQueued)
	}

	// Verify credentials were scrubbed in-place
	for _, b := range req.Credentials.RawSecret {
		if b != 0 {
			t.Fatalf("credentials were not zero-scrubbed after dispatch")
		}
	}
}

// TestCrossWorkspaceTenantFencingInRepository verifies that repository operations
// strictly enforce workspace scoping and reject cross-workspace lookups.
func TestCrossWorkspaceTenantFencingInRepository(t *testing.T) {
	ctx := context.Background()
	repo := core.NewMemoryRepository()

	ws1 := uuid.New().String()
	ws2 := uuid.New().String()

	// Seed resource in ws1
	conn1, err := repo.CreateProviderConnection(ctx, core.CreateProviderConnectionInput{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(ws1),
		Provider:    "fake",
		Name:        "ws1-conn",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create conn: %v", err)
	}

	target1, err := repo.CreateTarget(ctx, core.CreateTargetInput{
		ID:           uuid.New(),
		WorkspaceID:  uuid.MustParse(ws1),
		ConnectionID: conn1.ID,
		Name:         "ws1-target",
		Environment:  "production",
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	op1, _, err := repo.CreateOperation(ctx, core.CreateOperationInput{
		ID:             uuid.New(),
		WorkspaceID:    uuid.MustParse(ws1),
		TargetID:       target1.ID,
		Action:         "deploy",
		IdempotencyKey: "ws1-idemp",
		SourceRevision: "rev-1",
		Environment:    "production",
		OperationKind:  "deploy",
	})
	if err != nil {
		t.Fatalf("create op: %v", err)
	}

	// Attempting to access ws1 resources using ws2 must return ErrNotFound
	if _, err := repo.GetProviderConnection(ctx, ws2, conn1.ID.String()); err != core.ErrNotFound {
		t.Fatalf("cross-workspace GetProviderConnection: got err %v, want ErrNotFound", err)
	}
	if _, err := repo.GetTarget(ctx, ws2, target1.ID.String()); err != core.ErrNotFound {
		t.Fatalf("cross-workspace GetTarget: got err %v, want ErrNotFound", err)
	}
	if _, err := repo.GetOperation(ctx, ws2, op1.ID.String()); err != core.ErrNotFound {
		t.Fatalf("cross-workspace GetOperation: got err %v, want ErrNotFound", err)
	}

	// Attempting to update or delete ws1 resources from ws2 must fail
	if err := repo.DeleteTarget(ctx, ws2, target1.ID.String()); err != core.ErrNotFound {
		t.Fatalf("cross-workspace DeleteTarget: got err %v, want ErrNotFound", err)
	}
	if err := repo.DeleteProviderConnection(ctx, ws2, conn1.ID.String()); err != core.ErrNotFound {
		t.Fatalf("cross-workspace DeleteProviderConnection: got err %v, want ErrNotFound", err)
	}

	// Cross-workspace update operation must fail
	tamperedOp := op1
	tamperedOp.WorkspaceID = uuid.MustParse(ws2)
	if _, err := repo.UpdateOperation(ctx, tamperedOp, op1.Revision); err != core.ErrNotFound {
		t.Fatalf("cross-workspace UpdateOperation: got err %v, want ErrNotFound", err)
	}
}

// TestParseWebhookWithFakeProvider tests webhook parsing and error cases
func TestParseWebhookWithFakeProvider(t *testing.T) {
	fake := NewFakeProvider()

	// Invalid JSON
	_, err := fake.ParseWebhook(context.Background(), core.WebhookRequest{
		RawBody: []byte("not-json"),
	})
	if err == nil {
		t.Fatalf("expected error for invalid json webhook")
	}

	// Invalid status
	_, err = fake.ParseWebhook(context.Background(), core.WebhookRequest{
		RawBody: []byte(`{"event_id":"1","operation_id":"op-1","status":"invalid-status"}`),
	})
	if err == nil {
		t.Fatalf("expected error for invalid fake status")
	}

	// Valid status
	event, err := fake.ParseWebhook(context.Background(), core.WebhookRequest{
		RawBody: []byte(`{"event_id":"1","operation_id":"op-1","status":"building"}`),
	})
	if err != nil {
		t.Fatalf("parse webhook failed: %v", err)
	}
	if event.ObservedStatus != core.StatusBuilding {
		t.Fatalf("observed status = %q, want %q", event.ObservedStatus, core.StatusBuilding)
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
