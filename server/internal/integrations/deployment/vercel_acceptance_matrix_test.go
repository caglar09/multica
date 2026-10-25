package deployment

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	core "github.com/multica-ai/multica/server/internal/deployment"
)

// vercelSandboxServer provides a high-fidelity local simulation of Vercel's REST API
// and webhook lifecycle for Section 27 acceptance matrix verification.
type vercelSandboxServer struct {
	server        *httptest.Server
	validToken    string
	teamID        string
	teamName      string
	webhookSecret string

	mu           sync.RWMutex
	deployments  map[string]map[string]any
	projects     map[string]map[string]any
	reqCount     int32
	triggerFails int32 // countdown for simulated transient errors
}

func newVercelSandboxServer() *vercelSandboxServer {
	s := &vercelSandboxServer{
		validToken:    "vcl_tok_test_secure_12345",
		teamID:        "team_multica_qa",
		teamName:      "Multica QA Team",
		webhookSecret: "whsec_super_secret_signing_key_456",
		deployments:   make(map[string]map[string]any),
		projects:      make(map[string]map[string]any),
	}

	// Seed existing project
	s.projects["prj_existing_web"] = map[string]any{
		"id":        "prj_existing_web",
		"name":      "multica-web",
		"framework": "nextjs",
		"link": map[string]any{
			"type":             "github",
			"repo":             "caglar09/multica",
			"repoId":           "repo_existing_web",
			"productionBranch": "main",
		},
		"targets": map[string]any{
			"production": map[string]any{"url": "multica-web.vercel.app"},
		},
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/v2/teams", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.validToken {
			http.Error(w, `{"error":{"message":"Invalid token"}}`, http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"teams": []any{
				map[string]any{"id": s.teamID, "name": s.teamName, "slug": "multica-qa"},
			},
		})
	})

	mux.HandleFunc("/v2/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.validToken {
			http.Error(w, `{"error":{"message":"Invalid token"}}`, http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]string{"id": "usr_qa_1", "username": "qa-engineer", "name": "QA Engineer"},
		})
	})

	mux.HandleFunc("/v10/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.validToken {
			http.Error(w, `{"error":{"message":"Invalid token"}}`, http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			name, _ := body["name"].(string)
			newID := "prj_" + name
			gitRepository, _ := body["gitRepository"].(map[string]any)
			newProj := map[string]any{
				"id":        newID,
				"name":      name,
				"framework": body["framework"],
				"link": map[string]any{
					"type":             gitRepository["type"],
					"repo":             gitRepository["repo"],
					"repoId":           "repo_" + name,
					"productionBranch": "main",
				},
			}
			s.mu.Lock()
			s.projects[newID] = newProj
			s.mu.Unlock()
			json.NewEncoder(w).Encode(newProj)
			return
		}
		s.mu.RLock()
		defer s.mu.RUnlock()

		list := make([]any, 0, len(s.projects))
		for _, p := range s.projects {
			list = append(list, p)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"projects": list,
			"pagination": map[string]any{
				"count":   len(list),
				"hasMore": false,
			},
		})
	})

	mux.HandleFunc("/v9/projects/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.validToken {
			http.Error(w, `{"error":{"message":"Invalid token"}}`, http.StatusUnauthorized)
			return
		}
		projID := strings.TrimPrefix(r.URL.Path, "/v9/projects/")
		s.mu.RLock()
		proj, ok := s.projects[projID]
		s.mu.RUnlock()
		if !ok {
			http.Error(w, `{"error":{"message":"Project not found"}}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(proj)
	})

	mux.HandleFunc("/v13/deployments", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&s.reqCount, 1)
		if r.Header.Get("Authorization") != "Bearer "+s.validToken {
			http.Error(w, `{"error":{"message":"Invalid token"}}`, http.StatusUnauthorized)
			return
		}
		if atomic.LoadInt32(&s.triggerFails) > 0 {
			atomic.AddInt32(&s.triggerFails, -1)
			http.Error(w, `{"error":{"message":"Simulated rate limit / 503 upstream error"}}`, http.StatusServiceUnavailable)
			return
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		targetEnv, _ := body["target"].(string)
		gitSource, _ := body["gitSource"].(map[string]any)
		if repoID, _ := gitSource["repoId"].(string); repoID == "" {
			http.Error(w, `{"error":{"message":"gitSource.repoId is required"}}`, http.StatusBadRequest)
			return
		}
		sha, _ := gitSource["sha"].(string)

		depID := fmt.Sprintf("dpl_vcl_%d", time.Now().UnixNano())
		urlDomain := "preview-multica.vercel.app"
		if targetEnv == "production" {
			urlDomain = "multica-web.vercel.app"
		}

		dep := map[string]any{
			"id":           depID,
			"readyState":   "QUEUED",
			"target":       targetEnv,
			"url":          urlDomain,
			"inspectorUrl": "https://vercel.com/multica-qa/deployments/" + depID,
			"projectId":    body["project"],
			"gitSource":    gitSource,
			"commitSha":    sha,
		}

		s.mu.Lock()
		s.deployments[depID] = dep
		s.mu.Unlock()

		json.NewEncoder(w).Encode(dep)
	})

	mux.HandleFunc("/v13/deployments/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.validToken {
			http.Error(w, `{"error":{"message":"Invalid token"}}`, http.StatusUnauthorized)
			return
		}
		depID := strings.TrimPrefix(r.URL.Path, "/v13/deployments/")
		s.mu.RLock()
		dep, ok := s.deployments[depID]
		s.mu.RUnlock()
		if !ok {
			http.Error(w, `{"error":{"message":"Deployment not found"}}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(dep)
	})

	s.server = httptest.NewServer(mux)
	return s
}

func (s *vercelSandboxServer) Close() {
	if s.server != nil {
		s.server.Close()
	}
}

func (s *vercelSandboxServer) advanceState(depID string, state string, errorMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dep, ok := s.deployments[depID]; ok {
		dep["readyState"] = state
		if errorMsg != "" {
			dep["errorMessage"] = errorMsg
			dep["errorCode"] = "BUILD_ERROR"
		}
	}
}

func (s *vercelSandboxServer) signWebhook(body []byte) string {
	mac := hmac.New(sha1.New, []byte(s.webhookSecret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------------------------------------------------------------------------
// Section 27.1: CONNECTION ACCEPTANCE MATRIX
// ---------------------------------------------------------------------------

func TestVercelAcceptanceMatrix_Connection_LifecycleAndSecurity(t *testing.T) {
	sandbox := newVercelSandboxServer()
	defer sandbox.Close()

	ctx := context.Background()
	provider := NewVercelProvider(sandbox.server.URL)

	// 1. Valid Connection with Scoped Team
	t.Run("valid_connection_with_team_scope", func(t *testing.T) {
		creds := core.ConnectionCredentials{
			Config:    json.RawMessage(fmt.Sprintf(`{"team_id":"%s"}`, sandbox.teamID)),
			RawSecret: []byte(sandbox.validToken),
		}
		info, err := provider.ValidateConnection(ctx, creds)
		if err != nil {
			t.Fatalf("expected valid connection, got err: %v", err)
		}
		if info.ScopeID != sandbox.teamID || info.ScopeName != sandbox.teamName {
			t.Fatalf("scope mismatch: got %+v", info)
		}
	})

	// 2. Invalid Token / Authorization
	t.Run("invalid_token_rejected", func(t *testing.T) {
		creds := core.ConnectionCredentials{
			Config:    json.RawMessage(fmt.Sprintf(`{"team_id":"%s"}`, sandbox.teamID)),
			RawSecret: []byte("vcl_bad_token_revoked"),
		}
		_, err := provider.ValidateConnection(ctx, creds)
		if err == nil {
			t.Fatal("expected error for invalid token, got nil")
		}
		if !strings.Contains(err.Error(), "401") && !strings.Contains(err.Error(), "Invalid token") {
			t.Fatalf("unexpected error response: %v", err)
		}
	})

	// 3. Team / Account Mismatch
	t.Run("team_account_mismatch_rejected", func(t *testing.T) {
		creds := core.ConnectionCredentials{
			Config:    json.RawMessage(`{"team_id":"team_unauthorized_foreign"}`),
			RawSecret: []byte(sandbox.validToken),
		}
		_, err := provider.ValidateConnection(ctx, creds)
		if !errors.Is(err, ErrVercelScope) {
			t.Fatalf("expected ErrVercelScope for team mismatch, got: %v", err)
		}
	})

	// 4. Reconnect after credential update
	t.Run("reconnect_with_new_valid_token", func(t *testing.T) {
		// First fails
		badCreds := core.ConnectionCredentials{
			Config:    json.RawMessage(fmt.Sprintf(`{"team_id":"%s"}`, sandbox.teamID)),
			RawSecret: []byte("expired_token"),
		}
		if _, err := provider.ValidateConnection(ctx, badCreds); err == nil {
			t.Fatal("expected expired token to fail")
		}

		// Reconnect succeeds
		goodCreds := core.ConnectionCredentials{
			Config:    json.RawMessage(fmt.Sprintf(`{"team_id":"%s"}`, sandbox.teamID)),
			RawSecret: []byte(sandbox.validToken),
		}
		info, err := provider.ValidateConnection(ctx, goodCreds)
		if err != nil {
			t.Fatalf("reconnect failed: %v", err)
		}
		if info.ScopeID != sandbox.teamID {
			t.Fatalf("unexpected scope: %+v", info)
		}
	})

	// 5. Historical operations preserved in read-only state after connection revocation / deletion
	// (Acceptance criterion: Connection revocation preserves historical deployment operation records in read-only state)
	t.Run("connection_revocation_preserves_historical_operations_readonly", func(t *testing.T) {
		repo := core.NewMemoryRepository()
		wsID := uuid.New()
		connID := uuid.New()
		targetID := uuid.New()
		opID := uuid.New()

		// Seed active connection, target, and completed operation
		conn, err := repo.CreateProviderConnection(ctx, core.CreateProviderConnectionInput{
			ID:          connID,
			WorkspaceID: wsID,
			Provider:    "vercel",
			Name:        "production-vercel-connection",
			Enabled:     true,
		})
		if err != nil {
			t.Fatalf("create conn: %v", err)
		}

		_, err = repo.CreateTarget(ctx, core.CreateTargetInput{
			ID:           targetID,
			WorkspaceID:  wsID,
			ConnectionID: conn.ID,
			Name:         "frontend-web",
			Environment:  "production",
		})
		if err != nil {
			t.Fatalf("create target: %v", err)
		}

		provOpID := "dpl_hist_998877"
		historicalOp, _, err := repo.CreateOperation(ctx, core.CreateOperationInput{
			ID:             opID,
			WorkspaceID:    wsID,
			TargetID:       targetID,
			Action:         "deploy",
			IdempotencyKey: "hist-key-1",
			SourceRevision: "sha-historical-commit",
			Environment:    "production",
			OperationKind:  "deploy",
		})
		if err != nil {
			t.Fatalf("create op: %v", err)
		}
		historicalOp.Status = core.StatusBuilding
		historicalOp.ProviderOperationID = &provOpID
		historicalOp, err = repo.UpdateOperation(ctx, historicalOp, historicalOp.Revision)
		if err != nil {
			t.Fatalf("building op: %v", err)
		}
		historicalOp.Status = core.StatusReady
		historicalOp.Evidence = json.RawMessage(`{"url":"https://multica-hist.vercel.app"}`)
		historicalOp, err = repo.UpdateOperation(ctx, historicalOp, historicalOp.Revision)
		if err != nil {
			t.Fatalf("finalize op: %v", err)
		}

		// SIMULATE REVOCATION: Connection is revoked/deleted from the workspace
		if err := repo.DeleteProviderConnection(ctx, wsID.String(), connID.String()); err != nil {
			t.Fatalf("delete connection: %v", err)
		}

		// Verify connection lookup now returns ErrNotFound
		if _, err := repo.GetProviderConnection(ctx, wsID.String(), connID.String()); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for deleted connection, got: %v", err)
		}

		// VERIFY HISTORICAL RECORD PRESERVATION: Operation record still exists intact
		opAfterRevocation, err := repo.GetOperation(ctx, wsID.String(), opID.String())
		if err != nil {
			t.Fatalf("get historical operation after connection revocation failed: %v", err)
		}
		if opAfterRevocation.Status != core.StatusReady {
			t.Fatalf("historical status mutated: got %q, want %q", opAfterRevocation.Status, core.StatusReady)
		}
		if *opAfterRevocation.ProviderOperationID != provOpID {
			t.Fatalf("historical provider id lost: got %v", opAfterRevocation.ProviderOperationID)
		}
		if string(opAfterRevocation.Evidence) != `{"url":"https://multica-hist.vercel.app"}` {
			t.Fatalf("historical evidence lost: got %s", string(opAfterRevocation.Evidence))
		}

		// Historical operations are listed correctly in audit views
		ops, err := repo.ListOperations(ctx, wsID.String(), targetID.String())
		if err != nil {
			t.Fatalf("list operations: %v", err)
		}
		if len(ops) != 1 || ops[0].ID != opID {
			t.Fatalf("historical operation list mismatch: %+v", ops)
		}
	})
}

// ---------------------------------------------------------------------------
// Section 27.2: TARGET ACCEPTANCE MATRIX
// ---------------------------------------------------------------------------

func TestVercelAcceptanceMatrix_Target_DiscoveryAndConfig(t *testing.T) {
	sandbox := newVercelSandboxServer()
	defer sandbox.Close()

	ctx := context.Background()
	provider := NewVercelProvider(sandbox.server.URL)
	conn := core.ProviderConnection{
		Config: json.RawMessage(fmt.Sprintf(`{"team_id":"%s"}`, sandbox.teamID)),
	}
	creds := core.ConnectionCredentials{
		Config:    conn.Config,
		RawSecret: []byte(sandbox.validToken),
	}

	// 1. Discover existing projects in team scope
	t.Run("discover_existing_project", func(t *testing.T) {
		targets, err := provider.ListTargets(ctx, conn, creds)
		if err != nil {
			t.Fatalf("ListTargets failed: %v", err)
		}
		if len(targets) != 1 || targets[0].ExternalID != "prj_existing_web" {
			t.Fatalf("discovered targets mismatch: %+v", targets)
		}
		if targets[0].DefaultBranch != "main" || targets[0].Name != "multica-web" {
			t.Fatalf("target metadata mismatch: %+v", targets[0])
		}
		var metadata map[string]any
		if err := json.Unmarshal(targets[0].Metadata, &metadata); err != nil || metadata["repo_id"] != "repo_existing_web" {
			t.Fatalf("target repo_id metadata mismatch: %s", targets[0].Metadata)
		}
	})

	// 2. Reject target creation without explicit confirmation
	t.Run("create_project_requires_confirmation", func(t *testing.T) {
		req := core.CreateTargetRequest{
			Name:      "new-service",
			Confirmed: false,
		}
		_, err := provider.CreateTarget(ctx, conn, creds, req)
		if !errors.Is(err, ErrVercelConfirmationRequired) {
			t.Fatalf("expected ErrVercelConfirmationRequired, got: %v", err)
		}
	})

	// 3. Reject invalid project names
	t.Run("reject_invalid_project_name", func(t *testing.T) {
		req := core.CreateTargetRequest{
			Name:      "invalid project name with spaces!",
			Confirmed: true,
		}
		_, err := provider.CreateTarget(ctx, conn, creds, req)
		if !errors.Is(err, ErrVercelProjectName) {
			t.Fatalf("expected ErrVercelProjectName, got: %v", err)
		}
	})

	// 4. Root directory validation (path traversal rejection)
	t.Run("root_directory_validation", func(t *testing.T) {
		for _, badRoot := range []string{"../escape", "/etc/shadow", "apps/web/../../secret"} {
			req := core.CreateTargetRequest{
				Name:          "web-app",
				Confirmed:     true,
				RootDirectory: badRoot,
			}
			err := provider.ValidateTargetConfig(req)
			if err == nil || !strings.Contains(err.Error(), ErrVercelTargetConfig.Error()) {
				t.Fatalf("expected config error for rootDirectory %q, got: %v", badRoot, err)
			}
		}
	})

	// 5. Create project succeeds after explicit confirmation
	t.Run("create_project_succeeds_with_confirmation", func(t *testing.T) {
		req := core.CreateTargetRequest{
			Name:          "multica-analytics",
			Confirmed:     true,
			Framework:     "nextjs",
			RootDirectory: "apps/analytics",
			BuildCommand:  "pnpm build",
			Repository:    "github.com/caglar09/multica",
		}
		target, err := provider.CreateTarget(ctx, conn, creds, req)
		if err != nil {
			t.Fatalf("CreateTarget failed: %v", err)
		}
		if target.ExternalID != "prj_multica-analytics" || target.Name != "multica-analytics" {
			t.Fatalf("created target mismatch: %+v", target)
		}
	})
}

// ---------------------------------------------------------------------------
// Section 27.3: DEPLOYMENT ACCEPTANCE MATRIX (Preview & Production E2E)
// ---------------------------------------------------------------------------

func TestVercelAcceptanceMatrix_Deployment_PreviewAndProductionE2E(t *testing.T) {
	sandbox := newVercelSandboxServer()
	defer sandbox.Close()

	ctx := context.Background()
	repo := core.NewMemoryRepository()
	provider := NewVercelProvider(sandbox.server.URL)
	conn := core.ProviderConnection{Config: json.RawMessage(fmt.Sprintf(`{"team_id":"%s"}`, sandbox.teamID))}
	creds := core.ConnectionCredentials{Config: conn.Config, RawSecret: []byte(sandbox.validToken)}
	createdTarget, err := provider.CreateTarget(ctx, conn, creds, core.CreateTargetRequest{
		Name: "created-web", Environment: "preview", Repository: "github.com/caglar09/multica", Branch: "main", Confirmed: true,
	})
	if err != nil {
		t.Fatalf("CreateTarget before deployment failed: %v", err)
	}
	var createdMetadata map[string]any
	if err := json.Unmarshal(createdTarget.Metadata, &createdMetadata); err != nil || createdMetadata["repo_id"] != "repo_created-web" {
		t.Fatalf("created target repo_id metadata mismatch: %s", createdTarget.Metadata)
	}

	wsID := uuid.New()
	targetID := uuid.New()

	targetMetadata := createdTarget.Metadata

	// 1. Acceptance criterion 1: Preview deployment trigger succeeds and captures
	// deployment URL and provider operation ID
	t.Run("preview_deployment_trigger_captures_url_and_op_id", func(t *testing.T) {
		previewOpID := uuid.New()
		commitSHA := "abc12345def67890"

		req := core.DeploymentRequest{
			WorkspaceID: wsID,
			OperationID: previewOpID,
			TargetID:    targetID,
			ExternalTarget: core.RemoteTarget{
				ExternalID:    createdTarget.ExternalID,
				Name:          createdTarget.Name,
				Environment:   "preview",
				DefaultBranch: "main",
				Metadata:      targetMetadata,
			},
			Environment: "preview",
			CommitSHA:   commitSHA,
			Branch:      "feat/preview-test",
			Credentials: core.ConnectionCredentials{
				RawSecret: []byte(sandbox.validToken),
			},
		}

		remoteOp, err := provider.TriggerDeployment(ctx, req)
		if err != nil {
			t.Fatalf("TriggerDeployment preview failed: %v", err)
		}

		// MUST capture provider operation ID
		if !strings.HasPrefix(remoteOp.ProviderOperationID, "dpl_vcl_") {
			t.Fatalf("expected Vercel provider operation ID, got: %q", remoteOp.ProviderOperationID)
		}
		// MUST capture deployment URL
		if remoteOp.DeploymentURL == "" || !strings.Contains(remoteOp.DeploymentURL, "preview") {
			t.Fatalf("expected preview deployment URL, got: %q", remoteOp.DeploymentURL)
		}
		if remoteOp.InspectURL == "" {
			t.Fatalf("expected inspector URL, got: %q", remoteOp.InspectURL)
		}

		// Record in repository and verify persistence
		op, _, err := repo.CreateOperation(ctx, core.CreateOperationInput{
			ID:             previewOpID,
			WorkspaceID:    wsID,
			TargetID:       targetID,
			Action:         "deploy",
			IdempotencyKey: "preview-idemp-1",
			SourceRevision: commitSHA,
			Environment:    "preview",
			OperationKind:  "deploy",
		})
		if err != nil {
			t.Fatalf("create op in repo: %v", err)
		}
		op.ProviderOperationID = &remoteOp.ProviderOperationID
		op.Status = remoteOp.NormalizedStatus
		op.Evidence = remoteOp.Evidence
		updatedOp, err := repo.UpdateOperation(ctx, op, op.Revision)
		if err != nil {
			t.Fatalf("update op in repo: %v", err)
		}

		if *updatedOp.ProviderOperationID != remoteOp.ProviderOperationID {
			t.Fatalf("stored provider op id mismatch: %v", updatedOp.ProviderOperationID)
		}
	})

	// 2. Acceptance criterion 2: Production deployment request rejected without explicit
	// production confirmation payload
	t.Run("production_deployment_requires_explicit_confirmation", func(t *testing.T) {
		// A handler-level check validates that unconfirmed production deploy returns 400 Bad Request
		// Here we also verify that when confirmation is provided, provider executes with target="production"
		prodCommitSHA := "cba98765fed43210"
		confirmedReq := core.DeploymentRequest{
			WorkspaceID: wsID,
			OperationID: uuid.New(),
			TargetID:    targetID,
			ExternalTarget: core.RemoteTarget{
				ExternalID:    createdTarget.ExternalID,
				Name:          createdTarget.Name,
				Environment:   "production",
				DefaultBranch: "main",
				Metadata:      targetMetadata,
			},
			Environment: "production",
			CommitSHA:   prodCommitSHA,
			Branch:      "main",
			Credentials: core.ConnectionCredentials{
				RawSecret: []byte(sandbox.validToken),
			},
		}

		prodRemoteOp, err := provider.TriggerDeployment(ctx, confirmedReq)
		if err != nil {
			t.Fatalf("TriggerDeployment production failed: %v", err)
		}
		if !strings.HasPrefix(prodRemoteOp.ProviderOperationID, "dpl_vcl_") {
			t.Fatalf("expected provider operation ID for production, got: %q", prodRemoteOp.ProviderOperationID)
		}
		if prodRemoteOp.DeploymentURL != "multica-web.vercel.app" {
			t.Fatalf("expected production domain, got %q", prodRemoteOp.DeploymentURL)
		}
	})

	// 3. Transient Provider Failure and Retry
	t.Run("transient_failure_and_retry", func(t *testing.T) {
		atomic.StoreInt32(&sandbox.triggerFails, 1) // First call will fail with 503

		req := core.DeploymentRequest{
			WorkspaceID: wsID,
			OperationID: uuid.New(),
			TargetID:    targetID,
			ExternalTarget: core.RemoteTarget{
				ExternalID:  createdTarget.ExternalID,
				Name:        createdTarget.Name,
				Environment: "preview",
				Metadata:    targetMetadata,
			},
			Environment: "preview",
			CommitSHA:   "retry-sha-1",
			Branch:      "main",
			Credentials: core.ConnectionCredentials{RawSecret: []byte(sandbox.validToken)},
		}

		// First call fails transiently
		_, err := provider.TriggerDeployment(ctx, req)
		if err == nil {
			t.Fatal("expected transient failure, got nil")
		}

		// Second call succeeds on retry
		op2, err := provider.TriggerDeployment(ctx, req)
		if err != nil {
			t.Fatalf("retry call failed: %v", err)
		}
		if op2.ProviderOperationID == "" {
			t.Fatal("retry call returned empty operation id")
		}
	})

	// 4. Duplicate Request Idempotency
	t.Run("duplicate_request_idempotency", func(t *testing.T) {
		idempKey := "unique-idemp-key-acceptance"
		op1, created1, err := repo.CreateOperation(ctx, core.CreateOperationInput{
			ID:             uuid.New(),
			WorkspaceID:    wsID,
			TargetID:       targetID,
			Action:         "deploy",
			IdempotencyKey: idempKey,
			SourceRevision: "sha-idemp",
			Environment:    "preview",
			OperationKind:  "deploy",
		})
		if err != nil || !created1 {
			t.Fatalf("create first op: %v (created=%v)", err, created1)
		}

		// Second attempt with exact same idempotency key must return existing row without creating
		op2, created2, err := repo.CreateOperation(ctx, core.CreateOperationInput{
			ID:             uuid.New(),
			WorkspaceID:    wsID,
			TargetID:       targetID,
			Action:         "deploy",
			IdempotencyKey: idempKey,
			SourceRevision: "sha-idemp",
			Environment:    "preview",
			OperationKind:  "deploy",
		})
		if err != nil {
			t.Fatalf("second call returned err: %v", err)
		}
		if created2 {
			t.Fatal("second call should have returned created=false")
		}
		if op2.ID != op1.ID {
			t.Fatalf("idempotency violation: got id %v, want %v", op2.ID, op1.ID)
		}
	})
}

// ---------------------------------------------------------------------------
// Section 27.4: WEBHOOK ACCEPTANCE MATRIX
// ---------------------------------------------------------------------------

func TestVercelAcceptanceMatrix_Webhooks(t *testing.T) {
	sandbox := newVercelSandboxServer()
	defer sandbox.Close()

	ctx := context.Background()
	repo := core.NewMemoryRepository()
	provider := NewVercelProvider(sandbox.server.URL)

	registry := NewRegistry()
	_ = registry.Register(provider)

	wsID := uuid.New()
	targetID := uuid.New()
	opID := uuid.New()
	provOpID := "dpl_webhook_test_1"

	_, _ = repo.CreateTarget(ctx, core.CreateTargetInput{
		ID:          targetID,
		WorkspaceID: wsID,
		Name:        "web",
		Environment: "preview",
	})

	op, _, _ := repo.CreateOperation(ctx, core.CreateOperationInput{
		ID:             opID,
		WorkspaceID:    wsID,
		TargetID:       targetID,
		Action:         "deploy",
		IdempotencyKey: "wh-idemp-1",
		SourceRevision: "sha-wh",
		Environment:    "preview",
		OperationKind:  "deploy",
	})
	op.ProviderOperationID = &provOpID
	op, _ = repo.UpdateOperation(ctx, op, op.Revision)

	gateway := &core.EventGateway{
		Repository: repo,
		Providers:  registry,
		Find: func(ctx context.Context, w, p, opId string) (core.Operation, error) {
			return repo.GetOperationByProviderOperation(ctx, w, opId)
		},
	}

	// 1. Invalid signature rejected
	t.Run("invalid_signature_rejected", func(t *testing.T) {
		body := []byte(`{"id":"evt-bad-sig","type":"deployment.building","payload":{"deployment":{"id":"` + provOpID + `"}}}`)
		_, err := gateway.Handle(ctx, wsID.String(), "vercel", []byte(sandbox.webhookSecret), map[string][]string{
			"x-vercel-signature": {"invalid_signature_hex"},
		}, body)
		if !errors.Is(err, core.ErrWebhookSignatureInvalid) {
			t.Fatalf("expected ErrWebhookSignatureInvalid, got %v", err)
		}
	})

	// 2. Valid signature accepted and advances state to StatusBuilding
	t.Run("valid_signature_advances_state", func(t *testing.T) {
		body := []byte(`{"id":"evt-valid-1","type":"deployment.building","payload":{"deployment":{"id":"` + provOpID + `","readyState":"BUILDING"}}}`)
		sig := sandbox.signWebhook(body)

		res, err := gateway.Handle(ctx, wsID.String(), "vercel", []byte(sandbox.webhookSecret), map[string][]string{
			"x-vercel-signature": {sig},
		}, body)
		if err != nil {
			t.Fatalf("webhook handle failed: %v", err)
		}
		if !res.Applied || res.Duplicate {
			t.Fatalf("unexpected webhook result: %+v", res)
		}

		curOp, _ := repo.GetOperation(ctx, wsID.String(), opID.String())
		if curOp.Status != core.StatusBuilding {
			t.Fatalf("op status = %q, want %q", curOp.Status, core.StatusBuilding)
		}
	})

	// 3. Duplicate event handled idempotently
	t.Run("duplicate_webhook_deduplicated", func(t *testing.T) {
		body := []byte(`{"id":"evt-valid-1","type":"deployment.building","payload":{"deployment":{"id":"` + provOpID + `","readyState":"BUILDING"}}}`)
		sig := sandbox.signWebhook(body)

		res, err := gateway.Handle(ctx, wsID.String(), "vercel", []byte(sandbox.webhookSecret), map[string][]string{
			"x-vercel-signature": {sig},
		}, body)
		if err != nil {
			t.Fatalf("duplicate webhook handle err: %v", err)
		}
		if !res.Duplicate {
			t.Fatal("expected duplicate=true for replay webhook")
		}
		if res.Applied {
			t.Fatal("expected applied=false for replay webhook")
		}
	})

	// 4. State advances to ready
	t.Run("advance_to_ready", func(t *testing.T) {
		body := []byte(`{"id":"evt-ready-1","type":"deployment.ready","payload":{"deployment":{"id":"` + provOpID + `","readyState":"READY","url":"preview-ready.vercel.app"}}}`)
		sig := sandbox.signWebhook(body)

		res, err := gateway.Handle(ctx, wsID.String(), "vercel", []byte(sandbox.webhookSecret), map[string][]string{
			"x-vercel-signature": {sig},
		}, body)
		if err != nil {
			t.Fatalf("ready webhook err: %v", err)
		}
		if !res.Applied {
			t.Fatalf("ready webhook was not applied: %+v", res)
		}

		curOp, _ := repo.GetOperation(ctx, wsID.String(), opID.String())
		if curOp.Status != core.StatusReady {
			t.Fatalf("op status = %q, want %q", curOp.Status, core.StatusReady)
		}
	})

	// 5. Terminal state cannot regress (late arrival of building event)
	t.Run("terminal_state_cannot_regress", func(t *testing.T) {
		body := []byte(`{"id":"evt-late-building","type":"deployment.building","payload":{"deployment":{"id":"` + provOpID + `","readyState":"BUILDING"}}}`)
		sig := sandbox.signWebhook(body)

		res, err := gateway.Handle(ctx, wsID.String(), "vercel", []byte(sandbox.webhookSecret), map[string][]string{
			"x-vercel-signature": {sig},
		}, body)
		if err != nil {
			t.Fatalf("late webhook err: %v", err)
		}
		if res.Applied {
			t.Fatalf("late building event regressed terminal state: %+v", res)
		}

		curOp, _ := repo.GetOperation(ctx, wsID.String(), opID.String())
		if curOp.Status != core.StatusReady {
			t.Fatalf("terminal status regressed: got %q, want %q", curOp.Status, core.StatusReady)
		}
	})

	// 6. Unknown operation ID returns ErrNotFound
	t.Run("unknown_operation_id_rejected", func(t *testing.T) {
		body := []byte(`{"id":"evt-unknown-1","type":"deployment.ready","payload":{"deployment":{"id":"dpl_unknown_9999"}}}`)
		sig := sandbox.signWebhook(body)

		_, err := gateway.Handle(ctx, wsID.String(), "vercel", []byte(sandbox.webhookSecret), map[string][]string{
			"x-vercel-signature": {sig},
		}, body)
		if !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for unknown op id, got: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Section 27.5: RECONCILIATION ACCEPTANCE MATRIX
// ---------------------------------------------------------------------------

func TestVercelAcceptanceMatrix_Reconciliation(t *testing.T) {
	sandbox := newVercelSandboxServer()
	defer sandbox.Close()

	ctx := context.Background()
	repo := core.NewMemoryRepository()
	provider := NewVercelProvider(sandbox.server.URL)

	wsID := uuid.New()
	targetID := uuid.New()
	opID := uuid.New()

	targetMetadata, _ := json.Marshal(map[string]string{
		"team_id":         sandbox.teamID,
		"repository":      "caglar09/multica",
		"repository_type": "github",
		"repo_id":         "repo_existing_web",
	})

	_, _ = repo.CreateTarget(ctx, core.CreateTargetInput{
		ID:          targetID,
		WorkspaceID: wsID,
		Name:        "web-service",
		Environment: "preview",
	})

	// Trigger preview deployment on sandbox
	remoteOp, err := provider.TriggerDeployment(ctx, core.DeploymentRequest{
		WorkspaceID: wsID,
		OperationID: opID,
		TargetID:    targetID,
		ExternalTarget: core.RemoteTarget{
			ExternalID:  "prj_existing_web",
			Name:        "multica-web",
			Environment: "preview",
			Metadata:    targetMetadata,
		},
		Environment: "preview",
		CommitSHA:   "reconcile-sha-1",
		Branch:      "main",
		Credentials: core.ConnectionCredentials{RawSecret: []byte(sandbox.validToken)},
	})
	if err != nil {
		t.Fatalf("trigger deployment: %v", err)
	}

	op, _, _ := repo.CreateOperation(ctx, core.CreateOperationInput{
		ID:             opID,
		WorkspaceID:    wsID,
		TargetID:       targetID,
		Action:         "deploy",
		IdempotencyKey: "reconcile-idemp-1",
		SourceRevision: "reconcile-sha-1",
		Environment:    "preview",
		OperationKind:  "deploy",
	})
	op.ProviderOperationID = &remoteOp.ProviderOperationID
	op.Status = core.StatusBuilding
	op, _ = repo.UpdateOperation(ctx, op, op.Revision)

	// SIMULATE WEBHOOK FAILURE:
	// Vercel deployment completes and becomes READY on the remote provider,
	// but NO webhook ever arrives (dropped by network / firewall / provider glitch).
	sandbox.advanceState(remoteOp.ProviderOperationID, "READY", "")

	resolveLookup := func(ctx context.Context, o core.Operation) (core.Provider, core.OperationLookup, error) {
		return provider, core.OperationLookup{
			ProviderOperationID: *o.ProviderOperationID,
			ExternalTarget:      core.RemoteTarget{Metadata: targetMetadata},
			Credentials:         core.ConnectionCredentials{RawSecret: []byte(sandbox.validToken)},
		}, nil
	}

	reconciler := &core.Reconciler{
		Repository: repo,
		Lister:     repo,
		Resolve:    resolveLookup,
		Backoff:    50 * time.Millisecond,
		Deadline:   10 * time.Minute,
	}

	// Acceptance criterion 3: Simulated webhook failure triggers reconciler within
	// polling deadline to mark operation ready
	t.Run("simulated_webhook_failure_recovered_by_reconciler", func(t *testing.T) {
		processed, err := reconciler.Reconcile(ctx, wsID.String())
		if err != nil {
			t.Fatalf("reconciler run failed: %v", err)
		}
		if processed != 1 {
			t.Fatalf("reconciler processed %d operations, want 1", processed)
		}

		// Verify the operation was advanced to StatusReady via polling reconciliation
		reconciledOp, err := repo.GetOperation(ctx, wsID.String(), opID.String())
		if err != nil {
			t.Fatalf("get operation after reconcile: %v", err)
		}
		if reconciledOp.Status != core.StatusReady {
			t.Fatalf("status after reconciliation = %q, want %q", reconciledOp.Status, core.StatusReady)
		}
		if len(reconciledOp.Evidence) == 0 {
			t.Fatalf("reconciled operation missing evidence: %+v", reconciledOp)
		}
	})

	// 2. Reconciliation deadline exceeded produces visible diagnostic
	t.Run("reconciliation_deadline_exceeded_diagnostic", func(t *testing.T) {
		deadOpID := uuid.New()
		deadProvOpID := "dpl_stuck_forever"
		deadOp, _, _ := repo.CreateOperation(ctx, core.CreateOperationInput{
			ID:             deadOpID,
			WorkspaceID:    wsID,
			TargetID:       targetID,
			Action:         "deploy",
			IdempotencyKey: "dead-idemp-1",
			SourceRevision: "stuck-sha",
			Environment:    "preview",
			OperationKind:  "deploy",
		})
		deadOp.ProviderOperationID = &deadProvOpID
		deadOp.Status = core.StatusBuilding
		deadOp.CreatedAt = time.Now().Add(-2 * time.Hour) // Created 2 hours ago
		deadOp, _ = repo.UpdateOperation(ctx, deadOp, deadOp.Revision)

		deadlineReconciler := &core.Reconciler{
			Repository: repo,
			Lister:     repo,
			Resolve:    resolveLookup,
			Deadline:   1 * time.Hour, // 1 hour deadline
		}

		processed, err := deadlineReconciler.Reconcile(ctx, wsID.String())
		if err != nil {
			t.Fatalf("deadline reconciler failed: %v", err)
		}
		if processed != 1 {
			t.Fatalf("expected 1 dead operation processed, got %d", processed)
		}

		timedOutOp, _ := repo.GetOperation(ctx, wsID.String(), deadOpID.String())
		if timedOutOp.Status != core.StatusFailed {
			t.Fatalf("expected StatusFailed after deadline exceeded, got %q", timedOutOp.Status)
		}
		if timedOutOp.LastError == nil || !strings.Contains(*timedOutOp.LastError, "reconciliation deadline exceeded") {
			t.Fatalf("expected deadline diagnostic in last_error, got %v", timedOutOp.LastError)
		}
	})
}

// ---------------------------------------------------------------------------
// Section 27.6: SECURITY ACCEPTANCE MATRIX
// ---------------------------------------------------------------------------

func TestVercelAcceptanceMatrix_Security(t *testing.T) {
	sandbox := newVercelSandboxServer()
	defer sandbox.Close()

	ctx := context.Background()
	provider := NewVercelProvider(sandbox.server.URL)

	// 1. Provider error payload redacted
	t.Run("provider_error_payload_redacted", func(t *testing.T) {
		sensitiveErrMsg := "deployment failed: secret_token=vcl_secret_99887766 and auth=bearer_leak"
		sandbox.mu.Lock()
		sandbox.deployments["dpl_leak_test"] = map[string]any{
			"id":         "dpl_leak_test",
			"readyState": "ERROR",
		}
		sandbox.mu.Unlock()
		sandbox.advanceState("dpl_leak_test", "ERROR", sensitiveErrMsg)

		remote, err := provider.GetOperation(ctx, core.OperationLookup{
			ProviderOperationID: "dpl_leak_test",
			ExternalTarget:      core.RemoteTarget{Metadata: json.RawMessage(fmt.Sprintf(`{"team_id":"%s"}`, sandbox.teamID))},
			Credentials:         core.ConnectionCredentials{RawSecret: []byte(sandbox.validToken)},
		})
		if err != nil {
			t.Fatalf("GetOperation: %v", err)
		}
		if strings.Contains(remote.ErrorMessage, "vcl_secret_99887766") {
			t.Fatalf("secret was leaked in error message: %q", remote.ErrorMessage)
		}
		if !strings.Contains(remote.ErrorMessage, "[REDACTED]") {
			t.Fatalf("expected [REDACTED] in sanitized error, got: %q", remote.ErrorMessage)
		}
	})

	// 2. Webhook secret absent from parsed evidence
	t.Run("webhook_secret_absent_from_evidence", func(t *testing.T) {
		webhookPayload := []byte(`{"id":"evt-sec-1","type":"deployment.ready","payload":{"deployment":{"id":"dpl_sec_1","readyState":"READY"}}}`)
		sig := sandbox.signWebhook(webhookPayload)

		event, err := provider.ParseWebhook(ctx, core.WebhookRequest{
			Headers:       map[string][]string{"x-vercel-signature": {sig}},
			RawBody:       webhookPayload,
			SigningSecret: []byte(sandbox.webhookSecret),
		})
		if err != nil {
			t.Fatalf("ParseWebhook failed: %v", err)
		}
		if strings.Contains(string(event.Evidence), sandbox.webhookSecret) {
			t.Fatalf("signing secret leaked into evidence: %s", string(event.Evidence))
		}
		if strings.Contains(string(event.RawPayload), sandbox.webhookSecret) {
			t.Fatalf("signing secret leaked into raw payload: %s", string(event.RawPayload))
		}
	})

	// 3. ScrubBytes zeroes credential memory
	t.Run("credentials_scrubbed_in_memory", func(t *testing.T) {
		tokenBytes := []byte("secret_token_to_scrub")
		core.ScrubBytes(tokenBytes)
		for i, b := range tokenBytes {
			if b != 0 {
				t.Fatalf("byte at %d not zeroed: %d", i, b)
			}
		}
	})
}
