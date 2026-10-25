package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func TestDeploymentWebhookCanonicalRouteAndSecretCustody(t *testing.T) {
	ctx := context.Background()
	h, repo, _, _ := setupDeploymentTestHandler()
	registry := providers.NewRegistry()
	if err := registry.Register(providers.NewVercelProvider("")); err != nil {
		t.Fatal(err)
	}
	h.DeploymentProviders = registry

	key := bytes.Repeat([]byte{7}, secretbox.KeySize)
	keyRing, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.DeploymentKeyRing = keyRing

	workspaceID := uuid.New()
	pat := "pat-never-used-for-webhooks"
	webhookSecret := "webhook-secret-only"
	sealedPAT, err := keyRing.Seal([]byte(pat))
	if err != nil {
		t.Fatal(err)
	}
	sealedWebhook, err := keyRing.Seal([]byte(webhookSecret))
	if err != nil {
		t.Fatal(err)
	}
	patRef := base64.StdEncoding.EncodeToString(sealedPAT)
	webhookRef := base64.StdEncoding.EncodeToString(sealedWebhook)
	connection, err := repo.CreateProviderConnection(ctx, deployment.CreateProviderConnectionInput{
		ID: uuid.New(), WorkspaceID: workspaceID, Provider: "vercel", Name: "Vercel",
		SecretRef: &patRef, WebhookSecretRef: &webhookRef, Config: json.RawMessage(`{"team_id":"team-1"}`), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := repo.CreateTarget(ctx, deployment.CreateTargetInput{
		ID: uuid.New(), WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Preview", Environment: "preview", ExternalID: stringPtr("project-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err := repo.CreateOperation(ctx, deployment.CreateOperationInput{
		ID: uuid.New(), WorkspaceID: workspaceID, TargetID: target.ID, Action: "deploy", IdempotencyKey: "webhook-route-test",
		SourceRevision: "sha-1", Environment: "preview", OperationKind: "deploy",
	})
	if err != nil {
		t.Fatal(err)
	}
	providerOperationID := "dpl_webhook_route_1"
	operation.ProviderOperationID = &providerOperationID
	operation.Status = deployment.StatusQueued
	operation, err = repo.UpdateOperation(ctx, operation, operation.Revision)
	if err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Post("/api/webhooks/deployments/{workspaceId}/{connectionId}", h.HandleDeploymentWebhook)
	deliver := func(path string, body []byte, secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		mac := hmac.New(sha1.New, []byte(secret))
		_, _ = mac.Write(body)
		req.Header.Set("x-vercel-signature", hex.EncodeToString(mac.Sum(nil)))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	body := func(eventID, eventType, deploymentID string) []byte {
		return []byte(`{"id":"` + eventID + `","type":"` + eventType + `","token":"` + pat + `","payload":{"deployment":{"id":"` + deploymentID + `","url":"https://preview.example.test","readyState":"READY"},"project":{"id":"project-1"}}}`)
	}
	canonicalPath := "/api/webhooks/deployments/" + workspaceID.String() + "/" + connection.ID.String()

	wrongSecret := deliver(canonicalPath, body("evt-pat", "deployment.building", providerOperationID), pat)
	if wrongSecret.Code != http.StatusUnauthorized {
		t.Fatalf("PAT signature returned %d, want 401: %s", wrongSecret.Code, wrongSecret.Body.String())
	}
	if events, _ := repo.ListEvents(ctx, workspaceID.String(), operation.ID.String()); len(events) != 0 {
		t.Fatalf("PAT signature mutated events: %d", len(events))
	}

	building := deliver(canonicalPath, body("evt-building", "deployment.building", providerOperationID), webhookSecret)
	if building.Code != http.StatusOK {
		t.Fatalf("valid webhook returned %d: %s", building.Code, building.Body.String())
	}
	var buildingResponse map[string]any
	_ = json.Unmarshal(building.Body.Bytes(), &buildingResponse)
	if buildingResponse["applied"] != true || buildingResponse["duplicate"] == true {
		t.Fatalf("valid webhook response = %s", building.Body.String())
	}

	duplicate := deliver(canonicalPath, body("evt-building", "deployment.building", providerOperationID), webhookSecret)
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), `"duplicate":true`) {
		t.Fatalf("duplicate webhook response = %d %s", duplicate.Code, duplicate.Body.String())
	}

	ready := deliver(canonicalPath, body("evt-ready", "deployment.ready", providerOperationID), webhookSecret)
	if ready.Code != http.StatusOK {
		t.Fatalf("ready webhook returned %d: %s", ready.Code, ready.Body.String())
	}
	late := deliver(canonicalPath, body("evt-late", "deployment.building", providerOperationID), webhookSecret)
	if late.Code != http.StatusOK {
		t.Fatalf("late webhook returned %d: %s", late.Code, late.Body.String())
	}
	finalOperation, err := repo.GetOperation(ctx, workspaceID.String(), operation.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if finalOperation.Status != deployment.StatusReady {
		t.Fatalf("late event regressed operation to %q", finalOperation.Status)
	}

	unknown := deliver(canonicalPath, body("evt-unknown", "deployment.ready", "dpl-unknown"), webhookSecret)
	if unknown.Code != http.StatusOK || !strings.Contains(unknown.Body.String(), `"status":"ignored"`) {
		t.Fatalf("unknown operation response = %d %s", unknown.Code, unknown.Body.String())
	}

	wrongScope := deliver("/api/webhooks/deployments/"+uuid.NewString()+"/"+connection.ID.String(), body("evt-scope", "deployment.ready", providerOperationID), webhookSecret)
	if wrongScope.Code != http.StatusNotFound {
		t.Fatalf("wrong-scope webhook returned %d, want 404", wrongScope.Code)
	}

	for _, value := range []string{pat, webhookSecret} {
		if strings.Contains(building.Body.String(), value) || strings.Contains(ready.Body.String(), value) {
			t.Fatalf("webhook response leaked secret %q", value)
		}
	}
	events, err := repo.ListEvents(ctx, workspaceID.String(), operation.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), pat) || strings.Contains(string(event.Payload), webhookSecret) {
			t.Fatalf("event evidence leaked a secret: %s", event.Payload)
		}
	}
}

func TestDeploymentWebhookWithoutSecretFailsClosedForPollingOnlyConnection(t *testing.T) {
	h, repo, _, _ := setupDeploymentTestHandler()
	registry := providers.NewRegistry()
	if err := registry.Register(providers.NewVercelProvider("")); err != nil {
		t.Fatal(err)
	}
	h.DeploymentProviders = registry
	key := bytes.Repeat([]byte{9}, secretbox.KeySize)
	keyRing, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.DeploymentKeyRing = keyRing
	workspaceID := uuid.New()
	sealed, err := keyRing.Seal([]byte("pat-only"))
	if err != nil {
		t.Fatal(err)
	}
	ref := base64.StdEncoding.EncodeToString(sealed)
	connection, err := repo.CreateProviderConnection(context.Background(), deployment.CreateProviderConnectionInput{
		ID: uuid.New(), WorkspaceID: workspaceID, Provider: "vercel", Name: "Polling only", SecretRef: &ref, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/webhooks/deployments/" + workspaceID.String() + "/" + connection.ID.String()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"type":"deployment.ready"}`))
	req = withChiURLParams(req, map[string]string{"workspaceId": workspaceID.String(), "connectionId": connection.ID.String()})
	rec := httptest.NewRecorder()
	h.HandleDeploymentWebhook(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "pat-only") || !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("unsafe or unclear response: %s", rec.Body.String())
	}
}
