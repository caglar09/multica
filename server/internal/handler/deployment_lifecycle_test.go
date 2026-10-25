package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func newDeploymentLifecycleFixture(t *testing.T) (*Handler, *deployment.MemoryRepository, *providers.FakeProvider, uuid.UUID, deployment.Target) {
	t.Helper()
	h, repo, fake, mockDB := setupDeploymentTestHandler()
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyRing, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.DeploymentKeyRing = keyRing
	h.DeploymentDispatcher = &deployment.AsyncDispatcher{Repository: repo, Workers: 1, Timeout: time.Second}

	workspaceID := uuid.New()
	sealed, err := keyRing.Seal([]byte("fake-provider-token"))
	if err != nil {
		t.Fatal(err)
	}
	secretRef := base64.StdEncoding.EncodeToString(sealed)
	connection, err := repo.CreateProviderConnection(context.Background(), deployment.CreateProviderConnectionInput{
		ID: uuid.New(), WorkspaceID: workspaceID, Provider: "fake", Name: "fake", SecretRef: &secretRef, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := repo.CreateTarget(context.Background(), deployment.CreateTargetInput{
		ID: uuid.New(), WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "preview", Environment: "preview",
		ExternalID: stringPtr("fake-project"), Config: json.RawMessage(`{"repository":"owner/repo","default_branch":"main"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = mockDB
	return h, repo, fake, workspaceID, target
}

func triggerLifecycleDeployment(t *testing.T, h *Handler, workspaceID uuid.UUID, target deployment.Target, mockDB *mockDeploymentDB) deployment.Operation {
	t.Helper()
	body := []byte(`{"action":"deploy","repository":"owner/repo","git_ref":"main","commit_sha":"lifecycle-sha","commit_message":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-targets/"+target.ID.String()+"/deploy", bytes.NewReader(body))
	req = withMemberAuth(req, workspaceID.String(), "member", mockDB)
	req = withChiURLParams(req, map[string]string{"id": workspaceID.String(), "targetId": target.ID.String()})
	req.Header.Set("Idempotency-Key", uuid.NewString())
	rec := httptest.NewRecorder()
	h.TriggerDeployment(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("manual deployment returned %d: %s", rec.Code, rec.Body.String())
	}
	var response deploymentOperationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return waitForLifecycleOperation(t, h.DeploymentRepository, workspaceID, response.ID, func(op deployment.Operation) bool {
		return op.ProviderOperationID != nil
	})
}

func waitForLifecycleOperation(t *testing.T, repo deployment.Repository, workspaceID uuid.UUID, operationID string, ready func(deployment.Operation) bool) deployment.Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, err := repo.GetOperation(context.Background(), workspaceID.String(), operationID)
		if err == nil && ready(op) {
			return op
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("operation %s did not reach expected state", operationID)
	return deployment.Operation{}
}

func TestManualDeploymentAPIReachesFakeProvider(t *testing.T) {
	h, _, fake, workspaceID, target := newDeploymentLifecycleFixture(t)
	op := triggerLifecycleDeployment(t, h, workspaceID, target, &mockDeploymentDB{memberRoles: map[string]string{}})
	if op.ProviderOperationID == nil || *op.ProviderOperationID == "" {
		t.Fatal("manual API operation did not persist provider operation id")
	}
	var evidence map[string]any
	if err := json.Unmarshal(op.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence["deployment_url"] == nil || evidence["inspect_url"] == nil {
		t.Fatalf("provider URLs missing from redacted evidence: %s", op.Evidence)
	}
	if fake == nil {
		t.Fatal("fake provider was not configured")
	}
}

func TestDeploymentReconcilerProcessesNonTerminalOperation(t *testing.T) {
	h, repo, fake, workspaceID, target := newDeploymentLifecycleFixture(t)
	op := triggerLifecycleDeployment(t, h, workspaceID, target, &mockDeploymentDB{memberRoles: map[string]string{}})
	for i := 0; i < 3; i++ {
		if err := fake.Advance(*op.ProviderOperationID); err != nil {
			t.Fatal(err)
		}
	}
	processed, err := h.ReconcileDeployments(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("reconcile processed=%d err=%v, want one operation", processed, err)
	}
	updated, err := repo.GetOperation(context.Background(), workspaceID.String(), op.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != deployment.StatusReady {
		t.Fatalf("status=%q, want ready", updated.Status)
	}
}

func TestDeploymentReconcilerRecoversAfterHandlerRestart(t *testing.T) {
	h, repo, fake, workspaceID, target := newDeploymentLifecycleFixture(t)
	op := triggerLifecycleDeployment(t, h, workspaceID, target, &mockDeploymentDB{memberRoles: map[string]string{}})
	for i := 0; i < 3; i++ {
		if err := fake.Advance(*op.ProviderOperationID); err != nil {
			t.Fatal(err)
		}
	}

	// A new handler shares only durable repository state and the provider; the
	// replacement reconciler must complete the non-terminal operation.
	h2, _, _, _, _ := newDeploymentLifecycleFixture(t)
	h2.DeploymentRepository = repo
	h2.DeploymentProviders = h.DeploymentProviders
	h2.DeploymentKeyRing = h.DeploymentKeyRing
	h2.DeploymentDispatcher = &deployment.AsyncDispatcher{Repository: repo, Workers: 1, Timeout: time.Second}
	processed, err := h2.ReconcileDeployments(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("restart reconcile processed=%d err=%v, want one operation", processed, err)
	}
	updated, err := repo.GetOperation(context.Background(), workspaceID.String(), op.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != deployment.StatusReady {
		t.Fatalf("restart status=%q, want ready", updated.Status)
	}
}

func stringPtr(value string) *string { return &value }
