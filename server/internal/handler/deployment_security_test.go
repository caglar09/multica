package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func TestDeploymentConnectionAllowsPollingOnlyWithoutWebhookSecret(t *testing.T) {
	h, _, _, mockDB := setupDeploymentTestHandler()
	key := bytes.Repeat([]byte("k"), secretbox.KeySize)
	keyRing, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.DeploymentKeyRing = keyRing
	workspaceID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspaceID+"/deployments/connections", bytes.NewBufferString(`{"provider":"fake","name":"Polling only","token":"pat-only"}`))
	req = withChiURLParams(req, map[string]string{"workspaceId": workspaceID})
	req = withMemberAuth(req, workspaceID, "owner", mockDB)
	rec := httptest.NewRecorder()
	h.CreateDeploymentConnection(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		WebhookConfigured bool   `json:"webhook_configured"`
		WebhookStatus     string `json:"webhook_status"`
		WebhookURL        string `json:"webhook_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.WebhookConfigured || response.WebhookStatus != "polling_only" {
		t.Fatalf("unexpected webhook state: %+v", response)
	}
	if !strings.HasPrefix(response.WebhookURL, "/api/webhooks/deployments/") {
		t.Fatalf("unexpected webhook URL: %q", response.WebhookURL)
	}
}

func TestDeploymentWebhookSecretUpdateIsAdminOnlyAndRotates(t *testing.T) {
	h, repo, _, mockDB := setupDeploymentTestHandler()
	key := bytes.Repeat([]byte("r"), secretbox.KeySize)
	keyRing, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.DeploymentKeyRing = keyRing
	workspaceID := uuid.New()
	sealed, err := keyRing.Seal([]byte("pat-for-rotation"))
	if err != nil {
		t.Fatal(err)
	}
	patRef := base64.StdEncoding.EncodeToString(sealed)
	connection, err := repo.CreateProviderConnection(context.Background(), deployment.CreateProviderConnectionInput{
		ID: uuid.New(), WorkspaceID: workspaceID, Provider: "fake", Name: "Rotation", SecretRef: &patRef, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	setSecret := func(role, value string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/workspaces/"+workspaceID.String()+"/deployment-providers/connections/"+connection.ID.String()+"/webhook-secret", bytes.NewBufferString(`{"webhook_secret":"`+value+`"}`))
		req = withChiURLParams(req, map[string]string{"id": workspaceID.String(), "connectionId": connection.ID.String()})
		req = withMemberAuth(req, workspaceID.String(), role, mockDB)
		rec := httptest.NewRecorder()
		h.SetDeploymentWebhookSecret(rec, req)
		return rec
	}
	if rec := setSecret("member", "webhook-one"); rec.Code != http.StatusForbidden {
		t.Fatalf("member status=%d body=%s", rec.Code, rec.Body.String())
	}
	first := setSecret("owner", "webhook-one")
	if first.Code != http.StatusOK || strings.Contains(first.Body.String(), "webhook-one") {
		t.Fatalf("first rotation status=%d body=%s", first.Code, first.Body.String())
	}
	stored, err := repo.GetProviderConnection(context.Background(), workspaceID.String(), connection.ID.String())
	if err != nil || stored.WebhookSecretRef == nil {
		t.Fatalf("first secret was not stored: %v %+v", err, stored)
	}
	oldRef := *stored.WebhookSecretRef
	second := setSecret("admin", "webhook-two")
	if second.Code != http.StatusOK || strings.Contains(second.Body.String(), "webhook-two") {
		t.Fatalf("second rotation status=%d body=%s", second.Code, second.Body.String())
	}
	rotated, err := repo.GetProviderConnection(context.Background(), workspaceID.String(), connection.ID.String())
	if err != nil || rotated.WebhookSecretRef == nil || *rotated.WebhookSecretRef == oldRef {
		t.Fatalf("rotation did not replace the sealed secret: %v %+v", err, rotated)
	}
}

// TestHandlerSecurity_ZeroCredentialExposureInResponses verifies that HTTP responses
// for connections, targets, and operations never expose raw secret tokens or sensitive keys.
func TestHandlerSecurity_ZeroCredentialExposureInResponses(t *testing.T) {
	ctx := context.Background()
	h, repo, _, mockDB := setupDeploymentTestHandler()

	// Configure versioned keyring
	key := make([]byte, secretbox.KeySize)
	_, _ = rand.Read(key)
	kr, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	h.DeploymentKeyRing = kr

	wsID := uuid.New().String()
	rawSecret := "vercel_super_secret_pat_998877"

	// 1. Create a connection via handler
	createBody := []byte(`{
		"provider": "fake",
		"name": "Audit Conn",
		"token": "` + rawSecret + `",
		"webhook_secret": "whsec_dont_leak_me",
		"config": {
			"team_id": "team_secure",
			"api_key": "` + rawSecret + `"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/deployments/connections", bytes.NewReader(createBody))
	req = withChiURLParams(req, map[string]string{"workspaceId": wsID})
	req = withMemberAuth(req, wsID, "owner", mockDB)
	rec := httptest.NewRecorder()

	h.CreateDeploymentConnection(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateDeploymentConnection returned %d: %s", rec.Code, rec.Body.String())
	}

	createRespBody := rec.Body.String()
	if strings.Contains(createRespBody, rawSecret) {
		t.Fatalf("CreateDeploymentConnection leaked raw secret in response: %s", createRespBody)
	}
	if strings.Contains(createRespBody, "whsec_dont_leak_me") {
		t.Fatalf("CreateDeploymentConnection leaked webhook_secret in response: %s", createRespBody)
	}

	var connResp struct {
		ID         string `json:"id"`
		WebhookURL string `json:"webhook_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &connResp); err != nil {
		t.Fatalf("failed to parse connection create response: %v", err)
	}
	if connResp.WebhookURL != "/api/webhooks/deployments/"+wsID+"/"+connResp.ID {
		t.Fatalf("connection returned non-canonical webhook URL: %q", connResp.WebhookURL)
	}

	// 2. Get connection via handler
	reqGet := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/deployments/connections/"+connResp.ID, nil)
	reqGet = withChiURLParams(reqGet, map[string]string{"workspaceId": wsID, "connectionId": connResp.ID})
	reqGet = withMemberAuth(reqGet, wsID, "member", mockDB)
	recGet := httptest.NewRecorder()

	h.GetDeploymentConnection(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("GetDeploymentConnection returned %d: %s", recGet.Code, recGet.Body.String())
	}
	getRespBody := recGet.Body.String()
	if strings.Contains(getRespBody, rawSecret) {
		t.Fatalf("GetDeploymentConnection leaked raw secret: %s", getRespBody)
	}
	if strings.Contains(getRespBody, "whsec_dont_leak_me") {
		t.Fatalf("GetDeploymentConnection leaked webhook_secret: %s", getRespBody)
	}

	// 3. List connections via handler
	reqList := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/deployments/connections", nil)
	reqList = withChiURLParams(reqList, map[string]string{"workspaceId": wsID})
	reqList = withMemberAuth(reqList, wsID, "member", mockDB)
	recList := httptest.NewRecorder()

	h.ListDeploymentConnections(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("ListDeploymentConnections returned %d: %s", recList.Code, recList.Body.String())
	}
	listRespBody := recList.Body.String()
	if strings.Contains(listRespBody, rawSecret) {
		t.Fatalf("ListDeploymentConnections leaked raw secret: %s", listRespBody)
	}

	// 4. Verify target config redaction
	target, err := repo.CreateTarget(ctx, deployment.CreateTargetInput{
		ID:           uuid.New(),
		WorkspaceID:  uuid.MustParse(wsID),
		ConnectionID: uuid.MustParse(connResp.ID),
		Name:         "Audit Target",
		Environment:  "production",
		Config:       json.RawMessage(`{"token":"sensitive_token_val","safe":"public"}`),
	})
	if err != nil {
		t.Fatalf("CreateTarget failed: %v", err)
	}

	reqTarget := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/deployments/targets/"+target.ID.String(), nil)
	reqTarget = withChiURLParams(reqTarget, map[string]string{"workspaceId": wsID, "targetId": target.ID.String()})
	reqTarget = withMemberAuth(reqTarget, wsID, "member", mockDB)
	recTarget := httptest.NewRecorder()

	h.GetDeploymentTarget(recTarget, reqTarget)
	if recTarget.Code != http.StatusOK {
		t.Fatalf("GetDeploymentTarget returned %d: %s", recTarget.Code, recTarget.Body.String())
	}
	targetRespBody := recTarget.Body.String()
	if strings.Contains(targetRespBody, "sensitive_token_val") {
		t.Fatalf("GetDeploymentTarget leaked sensitive token: %s", targetRespBody)
	}
}

// TestHandlerSecurity_InvalidWebhookReturns401WithoutDBUpdate verifies that
// HTTP webhook requests with missing or invalid signatures return 400/401 immediately
// without creating any database events or updating operations.
func TestHandlerSecurity_InvalidWebhookReturns401WithoutDBUpdate(t *testing.T) {
	ctx := context.Background()
	h, repo, _, _ := setupDeploymentTestHandler()

	key := make([]byte, secretbox.KeySize)
	_, _ = rand.Read(key)
	kr, _ := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	h.DeploymentKeyRing = kr

	wsID := uuid.New()
	webhookSecret := "whsec_strict_handler_secret_123"

	sealedCreds, _ := kr.Seal([]byte(`pat_test`))
	secretRef := base64.StdEncoding.EncodeToString(sealedCreds)
	sealedWebhook, _ := kr.Seal([]byte(webhookSecret))
	webhookSecretRef := base64.StdEncoding.EncodeToString(sealedWebhook)
	h.DeploymentProviders = providers.NewRegistry()
	_ = h.DeploymentProviders.Register(providers.NewVercelProvider(""))

	conn, err := repo.CreateProviderConnection(ctx, deployment.CreateProviderConnectionInput{
		ID:               uuid.New(),
		WorkspaceID:      wsID,
		Provider:         "vercel",
		Name:             "Webhook Conn",
		SecretRef:        &secretRef,
		WebhookSecretRef: &webhookSecretRef,
		Config:           json.RawMessage(`{}`),
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("CreateProviderConnection failed: %v", err)
	}

	target, _ := repo.CreateTarget(ctx, deployment.CreateTargetInput{
		ID:           uuid.New(),
		WorkspaceID:  wsID,
		ConnectionID: conn.ID,
		Name:         "Target",
		Environment:  "preview",
	})

	provOpID := "dpl_prov_handler_test_99"
	op, _, _ := repo.CreateOperation(ctx, deployment.CreateOperationInput{
		ID:             uuid.New(),
		WorkspaceID:    wsID,
		TargetID:       target.ID,
		Action:         "deploy",
		IdempotencyKey: "idemp-webhook-handler-1",
		SourceRevision: "rev1",
		Environment:    "preview",
		OperationKind:  "deploy",
	})
	op.Status = deployment.StatusBuilding
	op.ProviderOperationID = &provOpID
	if _, err := repo.UpdateOperation(ctx, op, op.Revision); err != nil {
		t.Fatalf("set operation status for invalid webhook assertion: %v", err)
	}

	payload := []byte(`{"id":"` + provOpID + `","status":"succeeded"}`)

	// 1. Missing signature -> returns 400 or 401
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/webhooks/deployments/"+wsID.String()+"/"+conn.ID.String(), bytes.NewReader(payload))
	reqMissing = withChiURLParams(reqMissing, map[string]string{
		"workspaceId":  wsID.String(),
		"connectionId": conn.ID.String(),
	})
	recMissing := httptest.NewRecorder()

	h.HandleDeploymentWebhook(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest && recMissing.Code != http.StatusUnauthorized {
		t.Fatalf("expected 400 or 401 for missing webhook signature, got %d", recMissing.Code)
	}

	// Verify zero events created
	events, _ := repo.ListEvents(ctx, wsID.String(), op.ID.String())
	if len(events) != 0 {
		t.Fatalf("expected 0 events on missing signature, got %d", len(events))
	}

	// 2. Forged signature -> returns 401 Unauthorized
	reqForged := httptest.NewRequest(http.MethodPost, "/api/webhooks/deployments/"+wsID.String()+"/"+conn.ID.String(), bytes.NewReader(payload))
	reqForged.Header.Set("X-Hub-Signature-256", "sha256=00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	reqForged.Header.Set("x-vercel-signature", "00112233445566778899aabbccddeeff00112233")
	reqForged = withChiURLParams(reqForged, map[string]string{
		"workspaceId":  wsID.String(),
		"connectionId": conn.ID.String(),
	})
	recForged := httptest.NewRecorder()

	h.HandleDeploymentWebhook(recForged, reqForged)
	if recForged.Code != http.StatusUnauthorized && recForged.Code != http.StatusBadRequest {
		t.Fatalf("expected 401 or 400 for forged signature, got %d", recForged.Code)
	}

	// Verify database was untouched
	events, _ = repo.ListEvents(ctx, wsID.String(), op.ID.String())
	if len(events) != 0 {
		t.Fatalf("expected 0 events on forged signature, got %d", len(events))
	}
	latestOp, _ := repo.GetOperation(ctx, wsID.String(), op.ID.String())
	if latestOp.Status != deployment.StatusBuilding {
		t.Fatalf("operation status mutated on invalid webhook: %v", latestOp.Status)
	}
}

// TestHandlerSecurity_DatabaseEncryptionAtRest verifies that stored connection credentials
// carry the 0x56 VersionMagic header and Key ID metadata when saved in the repository.
func TestHandlerSecurity_DatabaseEncryptionAtRest(t *testing.T) {
	ctx := context.Background()
	h, repo, _, mockDB := setupDeploymentTestHandler()

	key := make([]byte, secretbox.KeySize)
	_, _ = rand.Read(key)
	kr, _ := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	h.DeploymentKeyRing = kr

	wsID := uuid.New().String()
	rawSecret := "vercel_token_super_secret_rest_val"

	createBody := []byte(`{
		"provider": "fake",
		"name": "Rest Encryption Conn",
		"token": "` + rawSecret + `",
		"webhook_secret": "rest_webhook_secret",
		"config": {"team_id":"team_1"}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/deployments/connections", bytes.NewReader(createBody))
	req = withChiURLParams(req, map[string]string{"workspaceId": wsID})
	req = withMemberAuth(req, wsID, "owner", mockDB)
	rec := httptest.NewRecorder()

	h.CreateDeploymentConnection(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateDeploymentConnection failed: %d", rec.Code)
	}

	var connResp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &connResp)

	// Fetch directly from storage repository to inspect wire format
	storedConn, err := repo.GetProviderConnection(ctx, wsID, connResp.ID)
	if err != nil {
		t.Fatalf("GetProviderConnection: %v", err)
	}
	if storedConn.SecretRef == nil {
		t.Fatalf("stored SecretRef is nil")
	}

	rawCiphertext, err := base64.StdEncoding.DecodeString(*storedConn.SecretRef)
	if err != nil {
		t.Fatalf("SecretRef is not valid base64: %v", err)
	}

	// Verify wire format: [0x56][KeyID: 1][12B Nonce][Ciphertext + Tag]
	// Minimum length is 2 (version+keyID) + 12 (nonce) + 16 (tag) = 30 bytes
	if len(rawCiphertext) < 30 {
		t.Fatalf("ciphertext too short: %d bytes", len(rawCiphertext))
	}
	if rawCiphertext[0] != 0x56 {
		t.Fatalf("expected VersionMagic 0x56, got 0x%x", rawCiphertext[0])
	}
	if rawCiphertext[1] != 1 {
		t.Fatalf("expected Key ID 1, got %d", rawCiphertext[1])
	}
	if storedConn.WebhookSecretRef == nil {
		t.Fatalf("stored WebhookSecretRef is nil")
	}
	webhookCiphertext, err := base64.StdEncoding.DecodeString(*storedConn.WebhookSecretRef)
	if err != nil || len(webhookCiphertext) < 30 {
		t.Fatalf("webhook secret is not sealed: %v", err)
	}
	if bytes.Equal(rawCiphertext, webhookCiphertext) {
		t.Fatalf("provider credential and webhook secret share ciphertext")
	}
	openedPAT, err := kr.Open(rawCiphertext)
	if err != nil || string(openedPAT) != rawSecret {
		t.Fatalf("provider credential did not round-trip independently")
	}
	openedWebhook, err := kr.Open(webhookCiphertext)
	if err != nil || string(openedWebhook) != "rest_webhook_secret" {
		t.Fatalf("webhook secret did not round-trip independently")
	}
}

// TestHandlerSecurity_TenantIsolationPenetrationMatrix tests cross-tenant boundary fencing.
// Workspace A user attempts to view, mutate, or delete Workspace B deployment resources.
func TestHandlerSecurity_TenantIsolationPenetrationMatrix(t *testing.T) {
	ctx := context.Background()
	h, repo, _, mockDB := setupDeploymentTestHandler()

	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// Seed Workspace B resources
	connB, err := repo.CreateProviderConnection(ctx, deployment.CreateProviderConnectionInput{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(wsB),
		Provider:    "fake",
		Name:        "wsB-connection",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("CreateProviderConnection wsB: %v", err)
	}

	targetB, err := repo.CreateTarget(ctx, deployment.CreateTargetInput{
		ID:           uuid.New(),
		WorkspaceID:  uuid.MustParse(wsB),
		ConnectionID: connB.ID,
		Name:         "wsB-target",
		Environment:  "production",
	})
	if err != nil {
		t.Fatalf("CreateTarget wsB: %v", err)
	}

	opB, _, err := repo.CreateOperation(ctx, deployment.CreateOperationInput{
		ID:             uuid.New(),
		WorkspaceID:    uuid.MustParse(wsB),
		TargetID:       targetB.ID,
		Action:         "deploy",
		IdempotencyKey: "idemp-wsb-pen-1",
		SourceRevision: "rev-wsb",
		Environment:    "production",
		OperationKind:  "deploy",
	})
	if err != nil {
		t.Fatalf("CreateOperation wsB: %v", err)
	}

	// Scenario 1: Workspace A attempts to GET Workspace B's connection
	t.Run("tenant_a_get_connection_b", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsA+"/deployments/connections/"+connB.ID.String(), nil)
		req = withChiURLParams(req, map[string]string{"workspaceId": wsA, "connectionId": connB.ID.String()})
		req = withMemberAuth(req, wsA, "member", mockDB)
		rec := httptest.NewRecorder()

		h.GetDeploymentConnection(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found for cross-tenant connection read, got %d", rec.Code)
		}
	})

	// Scenario 2: Workspace A attempts to DELETE Workspace B's connection
	t.Run("tenant_a_delete_connection_b", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+wsA+"/deployments/connections/"+connB.ID.String(), nil)
		req = withChiURLParams(req, map[string]string{"workspaceId": wsA, "connectionId": connB.ID.String()})
		req = withMemberAuth(req, wsA, "owner", mockDB)
		rec := httptest.NewRecorder()

		h.DeleteDeploymentConnection(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found for cross-tenant connection delete, got %d", rec.Code)
		}
	})

	// Scenario 3: Workspace A attempts to GET Workspace B's target
	t.Run("tenant_a_get_target_b", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsA+"/deployments/targets/"+targetB.ID.String(), nil)
		req = withChiURLParams(req, map[string]string{"workspaceId": wsA, "targetId": targetB.ID.String()})
		req = withMemberAuth(req, wsA, "member", mockDB)
		rec := httptest.NewRecorder()

		h.GetDeploymentTarget(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found for cross-tenant target read, got %d", rec.Code)
		}
	})

	// Scenario 4: Workspace A attempts to GET Workspace B's operation
	t.Run("tenant_a_get_operation_b", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsA+"/deployments/operations/"+opB.ID.String(), nil)
		req = withChiURLParams(req, map[string]string{"workspaceId": wsA, "operationId": opB.ID.String()})
		req = withMemberAuth(req, wsA, "member", mockDB)
		rec := httptest.NewRecorder()

		h.GetDeploymentOperation(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found for cross-tenant operation read, got %d", rec.Code)
		}
	})

	// Scenario 5: Workspace A attempts to trigger action on Workspace B's target
	t.Run("tenant_a_trigger_action_on_target_b", func(t *testing.T) {
		actionBody := []byte(`{"action":"deploy","commit_sha":"abc1234"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployments/targets/"+targetB.ID.String()+"/actions", bytes.NewReader(actionBody))
		req = withChiURLParams(req, map[string]string{"workspaceId": wsA, "targetId": targetB.ID.String()})
		req = withMemberAuth(req, wsA, "member", mockDB)
		rec := httptest.NewRecorder()

		h.TriggerDeployment(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found for action on foreign target, got %d", rec.Code)
		}
	})
}
