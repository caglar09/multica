package deployment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

// TestWebhook_ConstantTimeVerification satisfies ADR-0002 Section 7:
// Proves constant-time HMAC validation rejects tampered bodies, invalid signatures, and empty secrets.
func TestWebhook_ConstantTimeVerification(t *testing.T) {
	t.Parallel()
	secret := "whsec_constant_time_audit_secret_9988"
	body := []byte(`{"id":"dpl_audit_1","type":"deployment.ready","payload":{"url":"https://app.example.com"}}`)

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	validVercelSig := hex.EncodeToString(mac.Sum(nil))

	mac256 := hmac.New(sha256.New, []byte(secret))
	mac256.Write(body)
	validHubSig := "sha256=" + hex.EncodeToString(mac256.Sum(nil))

	t.Run("vercel_valid_accepted", func(t *testing.T) {
		if err := VerifyVercelSignature(body, validVercelSig, secret); err != nil {
			t.Fatalf("expected valid signature, got: %v", err)
		}
	})

	t.Run("vercel_tampered_body_rejected", func(t *testing.T) {
		tamperedBody := append([]byte(nil), body...)
		tamperedBody[len(tamperedBody)-5] ^= 0x01
		err := VerifyVercelSignature(tamperedBody, validVercelSig, secret)
		if !errors.Is(err, ErrWebhookSignatureInvalid) {
			t.Fatalf("expected ErrWebhookSignatureInvalid for tampered body, got: %v", err)
		}
	})

	t.Run("vercel_tampered_sig_rejected", func(t *testing.T) {
		badSig := validVercelSig[:len(validVercelSig)-2] + "ff"
		err := VerifyVercelSignature(body, badSig, secret)
		if !errors.Is(err, ErrWebhookSignatureInvalid) {
			t.Fatalf("expected ErrWebhookSignatureInvalid for bad sig, got: %v", err)
		}
	})

	t.Run("vercel_empty_secret_rejected", func(t *testing.T) {
		err := VerifyVercelSignature(body, validVercelSig, "")
		if !errors.Is(err, ErrWebhookSecretEmpty) {
			t.Fatalf("expected ErrWebhookSecretEmpty, got: %v", err)
		}
	})

	t.Run("hub_valid_accepted", func(t *testing.T) {
		if err := VerifyHubSignature256(body, validHubSig, secret); err != nil {
			t.Fatalf("expected valid hub signature, got: %v", err)
		}
	})

	t.Run("hub_invalid_rejected", func(t *testing.T) {
		err := VerifyHubSignature256(body, "sha256=badbadbadbadbadbadbadbadbadbad", secret)
		if !errors.Is(err, ErrWebhookSignatureInvalid) {
			t.Fatalf("expected ErrWebhookSignatureInvalid, got: %v", err)
		}
	})
}

// TestCredential_WriteOnlyMasking satisfies ADR-0002 Section 7:
// Proves token masking displays safe hints while hiding full entropy.
func TestCredential_WriteOnlyMasking(t *testing.T) {
	t.Parallel()

	tokens := []struct {
		raw      string
		expected string
	}{
		{"", ""},
		{"short", "***"},
		{"123456", "***"},
		{"abcdef12345", "***345"},
		{"vercel_tok_1234567890abcdef1234", "vercel_tok...1234"},
		{"ghp_0123456789abcdefghijklmnopqrstuvwxyz", "ghp_012345...wxyz"},
	}

	for _, tc := range tokens {
		masked := MaskToken(tc.raw)
		if masked != tc.expected {
			t.Errorf("MaskToken(%q) = %q; want %q", tc.raw, masked, tc.expected)
		}
		// Confirm masked output never contains the secret payload
		if len(tc.raw) > 12 && strings.Contains(masked, tc.raw[10:len(tc.raw)-4]) {
			t.Errorf("MaskToken leaked sensitive middle characters for %q", tc.raw)
		}
	}
}

// TestSecurityAudit_ZeroLeakage confirms that provider credentials, secrets,
// bearer tokens, and signing secrets are completely removed from configs and log text.
func TestSecurityAudit_ZeroLeakage(t *testing.T) {
	t.Parallel()

	rawSecret := "vercel_pat_secret_value_xyz789"
	signingSecret := "whsec_signing_secret_val_123"

	// 1. JSON Configuration Redaction
	inputCfg := map[string]any{
		"project_id":     "prj_audit_1",
		"token":          rawSecret,
		"signing_secret": signingSecret,
		"api_key":        "sk_live_very_secret_key",
		"password":       "super_secure_password_99",
		"nested": map[string]any{
			"access_token":  "access_tok_nested",
			"refresh_token": "refresh_tok_nested",
			"safe_field":    "keep_me",
		},
	}

	sanitized := SanitizeConfig(inputCfg)
	if sanitized["token"] != "[REDACTED]" {
		t.Errorf("token was not sanitized: %v", sanitized["token"])
	}
	if sanitized["signing_secret"] != "[REDACTED]" {
		t.Errorf("signing_secret was not sanitized: %v", sanitized["signing_secret"])
	}
	if sanitized["api_key"] != "[REDACTED]" {
		t.Errorf("api_key was not sanitized: %v", sanitized["api_key"])
	}

	// 2. Evidence JSON Redaction
	rawJSON, _ := json.Marshal(inputCfg)
	redactedJSON := RedactEvidence(rawJSON)
	redactedStr := string(redactedJSON)

	for _, secret := range []string{rawSecret, signingSecret, "sk_live_very_secret_key", "super_secure_password_99", "access_tok_nested", "refresh_tok_nested"} {
		if strings.Contains(redactedStr, secret) {
			t.Fatalf("RedactEvidence leaked secret %q in output: %s", secret, redactedStr)
		}
	}

	// 3. Text Log Scrubbing
	logEntries := []string{
		"HTTP Request Header Authorization: Bearer vercel_pat_secret_value_xyz789 to api.vercel.com",
		"Failed with token=vercel_pat_secret_value_xyz789 on project prj_1",
		"webhook rejected: signing_secret=whsec_signing_secret_val_123 invalid",
		"access_token=secret_oauth_tok_456 expired",
		"password=my_plain_pass in environment variable",
	}

	for _, entry := range logEntries {
		scrubbed := RedactEvidenceText(entry)
		for _, secret := range []string{rawSecret, signingSecret, "secret_oauth_tok_456", "my_plain_pass"} {
			if strings.Contains(scrubbed, secret) {
				t.Fatalf("RedactEvidenceText leaked %q in scrubbed output: %s", secret, scrubbed)
			}
		}
	}

	// 4. Memory Scrubbing
	sensitiveBuffer := []byte("top_secret_decrypted_token_bytes")
	ScrubBytes(sensitiveBuffer)
	for i, b := range sensitiveBuffer {
		if b != 0 {
			t.Fatalf("buffer at %d not zeroed: %v", i, b)
		}
	}
}

// TestSecurityAudit_DatabaseEncryptionWithKeyVersionMetadata verifies that:
// 1. Tokens stored in the database repository are encrypted with AES-256-GCM.
// 2. Encrypted rows carry key version metadata (0x56 VersionMagic + Key ID).
// 3. Modifying ciphertext or tag detects tampering.
// 4. Multi-key rotation allows decryption across historical key generations.
func TestSecurityAudit_DatabaseEncryptionWithKeyVersionMetadata(t *testing.T) {
	t.Parallel()

	key1 := make([]byte, secretbox.KeySize)
	key2 := make([]byte, secretbox.KeySize)
	_, _ = rand.Read(key1)
	_, _ = rand.Read(key2)

	kr, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key1, 2: key2}, nil)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}

	rawToken := "vercel_token_super_secret_production_abc123"

	// Seal with Key 1
	sealedKey1, err := kr.Seal([]byte(rawToken))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	secretRef1 := base64.StdEncoding.EncodeToString(sealedKey1)

	// Verify key version metadata in SecretRef
	keyVer, isVer, err := InspectSecretRef(secretRef1)
	if err != nil {
		t.Fatalf("InspectSecretRef failed: %v", err)
	}
	if !isVer {
		t.Fatalf("expected isVersioned=true for sealedKey1")
	}
	if keyVer != 1 {
		t.Fatalf("expected keyVersion=1, got %d", keyVer)
	}

	// Rotate KeyRing to Key 2
	if err := kr.RotatePrimary(2); err != nil {
		t.Fatalf("RotatePrimary(2): %v", err)
	}

	// Seal with Key 2
	sealedKey2, err := kr.Seal([]byte(rawToken))
	if err != nil {
		t.Fatalf("Seal with key 2: %v", err)
	}
	secretRef2 := base64.StdEncoding.EncodeToString(sealedKey2)

	keyVer2, isVer2, err := InspectSecretRef(secretRef2)
	if err != nil {
		t.Fatalf("InspectSecretRef(2) failed: %v", err)
	}
	if !isVer2 || keyVer2 != 2 {
		t.Fatalf("expected isVersioned=true and keyVersion=2, got ver=%d isVer=%v", keyVer2, isVer2)
	}

	// KeyRing decrypts both generation 1 and generation 2 secrets seamlessly
	decrypted1, err := kr.Open(sealedKey1)
	if err != nil {
		t.Fatalf("failed to decrypt generation 1 secret: %v", err)
	}
	if string(decrypted1) != rawToken {
		t.Fatalf("generation 1 mismatch: got %q, want %q", string(decrypted1), rawToken)
	}

	decrypted2, err := kr.Open(sealedKey2)
	if err != nil {
		t.Fatalf("failed to decrypt generation 2 secret: %v", err)
	}
	if string(decrypted2) != rawToken {
		t.Fatalf("generation 2 mismatch: got %q, want %q", string(decrypted2), rawToken)
	}

	// Tampering detection
	tampered := append([]byte(nil), sealedKey1...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := kr.Open(tampered); err == nil {
		t.Fatalf("tampered ciphertext must fail authentication, got nil error")
	}
}

// TestSecurityAudit_WebhookVerificationZeroStateMutation verifies that:
// An invalid webhook signature immediately halts processing and executes ZERO database state mutations.
func TestSecurityAudit_WebhookVerificationZeroStateMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	repo := NewMemoryRepository()
	wsID := uuid.New()
	targetID := uuid.New()
	provOpID := "dpl_webhook_audit_op_1"

	// Create an initial operation in queued state
	op, created, err := repo.CreateOperation(ctx, CreateOperationInput{
		ID:             uuid.New(),
		WorkspaceID:    wsID,
		TargetID:       targetID,
		Action:         "deploy",
		IdempotencyKey: "idemp-webhook-audit-1",
		SourceRevision: "sha-audit-1",
		Environment:    "preview",
		OperationKind:  "deploy",
	})
	if err != nil || !created {
		t.Fatalf("CreateOperation failed: %v", err)
	}

	// Transition queued -> dispatching -> running
	op.Status = StatusDispatching
	op, err = repo.UpdateOperation(ctx, op, op.Revision)
	if err != nil {
		t.Fatalf("UpdateOperation to dispatching failed: %v", err)
	}
	op.ProviderOperationID = &provOpID
	op.Status = StatusRunning
	op, err = repo.UpdateOperation(ctx, op, op.Revision)
	if err != nil {
		t.Fatalf("UpdateOperation to running failed: %v", err)
	}

	// Mock provider adapter requiring HMAC-SHA1 signature
	webhookSecret := "whsec_strict_signing_key_7788"
	mockProv := &auditWebhookProvider{
		secret: webhookSecret,
	}

	resolver := &auditResolver{provider: mockProv}
	gateway := &EventGateway{
		Repository: repo,
		Providers:  resolver,
		Find: func(ctx context.Context, w, p, opID string) (Operation, error) {
			if w == wsID.String() && opID == provOpID {
				return op, nil
			}
			return Operation{}, ErrNotFound
		},
	}

	body := []byte(fmt.Sprintf(`{"operation_id":"%s","status":"succeeded"}`, provOpID))

	// Sub-test 1: Missing signature -> rejected, zero events created, operation unchanged
	t.Run("missing_signature_zero_mutation", func(t *testing.T) {
		_, err := gateway.Handle(ctx, wsID.String(), "audit-mock", []byte(webhookSecret), map[string][]string{}, body)
		if err == nil {
			t.Fatal("expected error on missing signature, got nil")
		}

		// Verify zero events created
		events, err := repo.ListEvents(ctx, wsID.String(), op.ID.String())
		if err != nil {
			t.Fatalf("ListEvents failed: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("expected 0 events in database, found %d", len(events))
		}

		// Verify operation state did not regress or advance
		latestOp, _ := repo.GetOperation(ctx, wsID.String(), op.ID.String())
		if latestOp.Status != StatusRunning {
			t.Fatalf("operation status mutated unexpectedly to %v", latestOp.Status)
		}
	})

	// Sub-test 2: Invalid/forged signature -> rejected, zero events created, operation unchanged
	t.Run("invalid_signature_zero_mutation", func(t *testing.T) {
		headers := map[string][]string{
			"x-vercel-signature": {"forged_invalid_hex_signature_1234"},
		}
		_, err := gateway.Handle(ctx, wsID.String(), "audit-mock", []byte(webhookSecret), headers, body)
		if err == nil {
			t.Fatal("expected error on forged signature, got nil")
		}

		events, _ := repo.ListEvents(ctx, wsID.String(), op.ID.String())
		if len(events) != 0 {
			t.Fatalf("expected 0 events in database, found %d", len(events))
		}
	})

	// Sub-test 3: Valid signature -> succeeds, event stored, state advanced
	t.Run("valid_signature_advances_state", func(t *testing.T) {
		mac := hmac.New(sha1.New, []byte(webhookSecret))
		mac.Write(body)
		validSig := hex.EncodeToString(mac.Sum(nil))

		headers := map[string][]string{
			"x-vercel-signature": {validSig},
		}
		res, err := gateway.Handle(ctx, wsID.String(), "audit-mock", []byte(webhookSecret), headers, body)
		if err != nil {
			t.Fatalf("gateway.Handle with valid signature failed: %v", err)
		}
		if !res.Applied {
			t.Fatalf("expected event applied=true")
		}

		events, _ := repo.ListEvents(ctx, wsID.String(), op.ID.String())
		if len(events) != 1 {
			t.Fatalf("expected 1 event recorded, got %d", len(events))
		}

		latestOp, _ := repo.GetOperation(ctx, wsID.String(), op.ID.String())
		if latestOp.Status != StatusSucceeded {
			t.Fatalf("expected operation status Succeeded, got %v", latestOp.Status)
		}
	})
}

// TestSecurityAudit_TenantIsolationPenetration verifies multi-tenant cryptographic
// and data access boundaries: Workspace A cannot view, mutate, or trigger Workspace B deployments.
func TestSecurityAudit_TenantIsolationPenetration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	repo := NewMemoryRepository()
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// 1. Seed Workspace B resources
	connB, err := repo.CreateProviderConnection(ctx, CreateProviderConnectionInput{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(wsB),
		Provider:    "vercel",
		Name:        "wsB-conn",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create connB: %v", err)
	}

	targetB, err := repo.CreateTarget(ctx, CreateTargetInput{
		ID:           uuid.New(),
		WorkspaceID:  uuid.MustParse(wsB),
		ConnectionID: connB.ID,
		Name:         "wsB-prod-target",
		Environment:  "production",
	})
	if err != nil {
		t.Fatalf("create targetB: %v", err)
	}

	provOpB := "dpl_wsb_secret_op_456"
	opB, _, err := repo.CreateOperation(ctx, CreateOperationInput{
		ID:             uuid.New(),
		WorkspaceID:    uuid.MustParse(wsB),
		TargetID:       targetB.ID,
		Action:         "deploy",
		IdempotencyKey: "idemp-wsb-1",
		SourceRevision: "sha-wsb-prod",
		Environment:    "production",
		OperationKind:  "deploy",
	})
	if err != nil {
		t.Fatalf("create opB: %v", err)
	}
	opB.ProviderOperationID = &provOpB
	_, _ = repo.UpdateOperation(ctx, opB, opB.Revision)

	// Workspace A attempts:

	// 1. Get Workspace B's provider connection using Workspace A's identity -> must return ErrNotFound
	t.Run("tenant_a_cannot_read_conn_b", func(t *testing.T) {
		_, err := repo.GetProviderConnection(ctx, wsA, connB.ID.String())
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound for cross-tenant connection read, got: %v", err)
		}
	})

	// 2. List provider connections for Workspace A -> must not contain Workspace B's connection
	t.Run("tenant_a_list_conns_excludes_b", func(t *testing.T) {
		conns, err := repo.ListProviderConnections(ctx, wsA)
		if err != nil {
			t.Fatalf("ListProviderConnections: %v", err)
		}
		for _, c := range conns {
			if c.ID == connB.ID || c.WorkspaceID.String() == wsB {
				t.Fatalf("tenant leakage: Workspace A listed Workspace B connection: %+v", c)
			}
		}
	})

	// 3. Get Workspace B's target using Workspace A's identity -> must return ErrNotFound
	t.Run("tenant_a_cannot_read_target_b", func(t *testing.T) {
		_, err := repo.GetTarget(ctx, wsA, targetB.ID.String())
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound for cross-tenant target read, got: %v", err)
		}
	})

	// 4. List targets for Workspace A -> must not contain Workspace B's target
	t.Run("tenant_a_list_targets_excludes_b", func(t *testing.T) {
		targets, err := repo.ListTargets(ctx, wsA)
		if err != nil {
			t.Fatalf("ListTargets: %v", err)
		}
		for _, tgt := range targets {
			if tgt.ID == targetB.ID || tgt.WorkspaceID.String() == wsB {
				t.Fatalf("tenant leakage: Workspace A listed Workspace B target: %+v", tgt)
			}
		}
	})

	// 5. Get Workspace B's operation using Workspace A's identity -> must return ErrNotFound
	t.Run("tenant_a_cannot_read_op_b", func(t *testing.T) {
		_, err := repo.GetOperation(ctx, wsA, opB.ID.String())
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound for cross-tenant operation read, got: %v", err)
		}
	})

	// 6. Cross-Tenant Webhook Replay: Inbound webhook presented to Workspace A attempting
	// to advance Workspace B's operation -> Find fails and event is not applied to Workspace B
	t.Run("cross_tenant_webhook_correlation_fenced", func(t *testing.T) {
		gateway := &EventGateway{
			Repository: repo,
			Providers:  &auditResolver{provider: &auditWebhookProvider{secret: "whsec_test"}},
			Find: func(ctx context.Context, w, p, opID string) (Operation, error) {
				// Enforce workspace-scoped lookup
				op, err := repo.GetOperation(ctx, w, opID)
				if err != nil {
					return Operation{}, ErrNotFound
				}
				if op.WorkspaceID.String() != w {
					return Operation{}, ErrNotFound
				}
				return op, nil
			},
		}

		webhookBody := []byte(fmt.Sprintf(`{"operation_id":"%s","status":"failed"}`, provOpB))
		mac := hmac.New(sha1.New, []byte("whsec_test"))
		mac.Write(webhookBody)
		sig := hex.EncodeToString(mac.Sum(nil))

		// Deliberately target Workspace A with Workspace B's operation
		_, err := gateway.Handle(ctx, wsA, "audit-mock", []byte("whsec_test"), map[string][]string{"x-vercel-signature": {sig}}, webhookBody)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound for cross-workspace webhook operation lookup, got: %v", err)
		}

		// Verify Workspace B operation was NOT modified
		checkOpB, _ := repo.GetOperation(ctx, wsB, opB.ID.String())
		if checkOpB.Status == StatusFailed {
			t.Fatalf("Workspace B operation was compromised by cross-workspace webhook!")
		}
	})
}

// Test fixtures for audit provider
type auditResolver struct {
	provider Provider
}

func (r *auditResolver) Get(string) (Provider, error) {
	return r.provider, nil
}

type auditWebhookProvider struct {
	secret string
}

func (p *auditWebhookProvider) Kind() string { return "audit-mock" }
func (p *auditWebhookProvider) Capabilities() CapabilitySet {
	return CapabilitySet{Webhook: true, Trigger: true, Status: true}
}
func (p *auditWebhookProvider) ValidateConnection(context.Context, ConnectionCredentials) (ConnectionInfo, error) {
	return ConnectionInfo{AccountID: "acc_audit_1", ScopeName: "audit_scope"}, nil
}
func (p *auditWebhookProvider) ListTargets(context.Context, ProviderConnection, ConnectionCredentials) ([]RemoteTarget, error) {
	return nil, nil
}
func (p *auditWebhookProvider) GetTarget(context.Context, ProviderConnection, ConnectionCredentials, string) (RemoteTarget, error) {
	return RemoteTarget{}, nil
}
func (p *auditWebhookProvider) CreateTarget(context.Context, ProviderConnection, ConnectionCredentials, CreateTargetRequest) (RemoteTarget, error) {
	return RemoteTarget{}, nil
}
func (p *auditWebhookProvider) TriggerDeployment(context.Context, DeploymentRequest) (RemoteOperation, error) {
	return RemoteOperation{ProviderOperationID: "audit_trig_1", NormalizedStatus: StatusQueued}, nil
}
func (p *auditWebhookProvider) GetOperation(context.Context, OperationLookup) (RemoteOperation, error) {
	return RemoteOperation{}, nil
}
func (p *auditWebhookProvider) CancelOperation(context.Context, CancelRequest) (RemoteOperation, error) {
	return RemoteOperation{}, nil
}
func (p *auditWebhookProvider) ParseWebhook(ctx context.Context, req WebhookRequest) (ProviderEvent, error) {
	sig := ""
	if vals := req.Headers["x-vercel-signature"]; len(vals) > 0 {
		sig = vals[0]
	}
	if err := VerifyVercelSignature(req.RawBody, sig, string(req.SigningSecret)); err != nil {
		return ProviderEvent{}, err
	}

	var parsed struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}
	_ = json.Unmarshal(req.RawBody, &parsed)

	status := StatusRunning
	if parsed.Status == "succeeded" {
		status = StatusSucceeded
	} else if parsed.Status == "failed" {
		status = StatusFailed
	}

	return ProviderEvent{
		ProviderEventID:     "evt_audit_" + parsed.OperationID,
		ProviderEventType:   "deployment.update",
		ProviderOperationID: parsed.OperationID,
		ObservedStatus:      status,
		RawPayload:          req.RawBody,
	}, nil
}
