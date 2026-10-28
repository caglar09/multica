package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDeploymentConfigRedactsNestedSecrets(t *testing.T) {
	got := deploymentConfig(json.RawMessage(`{"team_id":"team-1","environment_variables":{"API_KEY":"do-not-return"},"nested":[{"token":"also-do-not-return"}]}`))
	var value map[string]any
	if err := json.Unmarshal(got, &value); err != nil {
		t.Fatal(err)
	}
	if value["team_id"] != "team-1" {
		t.Fatalf("safe config lost non-sensitive value: %#v", value)
	}
	if value["environment_variables"].(map[string]any)["API_KEY"] != "[REDACTED]" {
		t.Fatalf("environment variable was not redacted: %#v", value)
	}
	if value["nested"].([]any)[0].(map[string]any)["token"] != "[REDACTED]" {
		t.Fatalf("nested token was not redacted: %#v", value)
	}
}

type mockDeploymentDB struct {
	db.DBTX
	allowedProjects map[string]string // projectID -> workspaceID
	memberRoles     map[string]string // userID:workspaceID -> role
}

func (m *mockDeploymentDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if (strings.Contains(sql, "GetMemberByUserAndWorkspace") || strings.Contains(strings.ToLower(sql), "member")) && len(args) >= 2 {
		userUUID, ok1 := args[0].(pgtype.UUID)
		wsUUID, ok2 := args[1].(pgtype.UUID)
		if ok1 && ok2 && userUUID.Valid && wsUUID.Valid {
			key := uuid.UUID(userUUID.Bytes).String() + ":" + uuid.UUID(wsUUID.Bytes).String()
			if role, ok := m.memberRoles[key]; ok {
				return &mockDeploymentRow{role: role}
			}
		}
		return &mockDeploymentRow{err: pgx.ErrNoRows}
	}

	if len(args) >= 2 {
		projUUID, ok1 := args[0].(pgtype.UUID)
		wsUUID, ok2 := args[1].(pgtype.UUID)
		if ok1 && ok2 && projUUID.Valid && wsUUID.Valid {
			projID := uuid.UUID(projUUID.Bytes).String()
			wsID := uuid.UUID(wsUUID.Bytes).String()
			if expectedWS, ok := m.allowedProjects[projID]; ok && expectedWS == wsID {
				return &mockDeploymentRow{err: nil}
			}
		}
	}
	return &mockDeploymentRow{err: pgx.ErrNoRows}
}

type mockDeploymentRow struct {
	err  error
	role string
}

func (m *mockDeploymentRow) Scan(dest ...interface{}) error {
	if m.err != nil {
		return m.err
	}
	if len(dest) == 5 { // Member row Scan(&i.ID, &i.WorkspaceID, &i.UserID, &i.Role, &i.CreatedAt)
		if rolePtr, ok := dest[3].(*string); ok {
			*rolePtr = m.role
		}
	}
	return nil
}

func setupDeploymentTestHandler() (*Handler, *deployment.MemoryRepository, *providers.FakeProvider, *mockDeploymentDB) {
	repo := deployment.NewMemoryRepository()
	fake := providers.NewFakeProvider()
	registry := providers.NewRegistry()
	_ = registry.Register(fake)

	mockDB := &mockDeploymentDB{
		allowedProjects: make(map[string]string),
		memberRoles:     make(map[string]string),
	}

	h := &Handler{
		DeploymentRepository: repo,
		DeploymentProviders:  registry,
		Queries:              db.New(mockDB),
	}
	return h, repo, fake, mockDB
}

func withChiURLParams(r *http.Request, params map[string]string) *http.Request {
	chiCtx := chi.NewRouteContext()
	for k, v := range params {
		chiCtx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chiCtx))
}

func withMemberAuth(r *http.Request, wsID string, role string, mockDB *mockDeploymentDB) *http.Request {
	wsUUID := uuid.MustParse(wsID)
	userUUID := uuid.New()
	member := db.Member{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: wsUUID, Valid: true},
		UserID:      pgtype.UUID{Bytes: userUUID, Valid: true},
		Role:        role,
	}
	if mockDB != nil {
		mockDB.memberRoles[userUUID.String()+":"+wsID] = role
	}
	r.Header.Set("X-User-ID", userUUID.String())
	ctx := middleware.SetMemberContext(r.Context(), wsID, member)
	return r.WithContext(ctx)
}

// TestCrossWorkspaceTargetAttachmentRejected verifies that attempting to create
// or patch a deployment target in Workspace A with a connection or project belonging
// to Workspace B is rejected with 404 (Not Found).
func TestCrossWorkspaceTargetAttachmentRejected(t *testing.T) {
	ctx := context.Background()
	h, repo, _, mockDB := setupDeploymentTestHandler()

	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// 1. Connection A in wsA, Connection B in wsB
	connA, err := repo.CreateProviderConnection(ctx, deployment.CreateProviderConnectionInput{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(wsA),
		Provider:    "fake",
		Name:        "wsA-conn",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create connA: %v", err)
	}

	connB, err := repo.CreateProviderConnection(ctx, deployment.CreateProviderConnectionInput{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(wsB),
		Provider:    "fake",
		Name:        "wsB-conn",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create connB: %v", err)
	}

	// 2. Project A in wsA, Project B in wsB
	projA := uuid.New().String()
	projB := uuid.New().String()
	mockDB.allowedProjects[projA] = wsA
	mockDB.allowedProjects[projB] = wsB

	// Sub-test 1: Attach target in wsA referencing connB (from wsB) -> 404 connection not found
	t.Run("cross_workspace_connection_rejected", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"connection_id": connB.ID.String(),
			"name":          "target-cross-conn",
			"environment":   "production",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployment-targets", bytes.NewReader(body))
		req = withMemberAuth(req, wsA, "admin", mockDB)
		req = withChiURLParams(req, map[string]string{"id": wsA})
		w := httptest.NewRecorder()

		h.CreateDeploymentTarget(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-workspace connection, got %d: %s", w.Code, w.Body.String())
		}
		var errResp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &errResp)
		if errResp["error"] != "deployment connection not found" {
			t.Fatalf("error message = %q, want %q", errResp["error"], "deployment connection not found")
		}
	})

	// Sub-test 2: Attach target in wsA referencing projB (from wsB) -> 404 project not found
	t.Run("cross_workspace_project_rejected", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"connection_id": connA.ID.String(),
			"project_id":    projB,
			"name":          "target-cross-proj",
			"environment":   "production",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployment-targets", bytes.NewReader(body))
		req = withMemberAuth(req, wsA, "admin", mockDB)
		req = withChiURLParams(req, map[string]string{"id": wsA})
		w := httptest.NewRecorder()

		h.CreateDeploymentTarget(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-workspace project, got %d: %s", w.Code, w.Body.String())
		}
		var errResp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &errResp)
		if errResp["error"] != "project not found" {
			t.Fatalf("error message = %q, want %q", errResp["error"], "project not found")
		}
	})

	// Sub-test 3: Patch existing target in wsA referencing projB (from wsB) -> 404 project not found
	t.Run("cross_workspace_project_patch_rejected", func(t *testing.T) {
		targetA, err := repo.CreateTarget(ctx, deployment.CreateTargetInput{
			ID:           uuid.New(),
			WorkspaceID:  uuid.MustParse(wsA),
			ConnectionID: connA.ID,
			Name:         "target-a",
			Environment:  "production",
		})
		if err != nil {
			t.Fatalf("create targetA: %v", err)
		}

		body, _ := json.Marshal(map[string]any{
			"project_id": projB,
		})
		req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+wsA+"/deployment-targets/"+targetA.ID.String(), bytes.NewReader(body))
		req = withMemberAuth(req, wsA, "admin", mockDB)
		req = withChiURLParams(req, map[string]string{"id": wsA, "targetId": targetA.ID.String()})
		w := httptest.NewRecorder()

		h.UpdateDeploymentTarget(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-workspace project patch, got %d: %s", w.Code, w.Body.String())
		}
		var errResp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &errResp)
		if errResp["error"] != "project not found" {
			t.Fatalf("error message = %q, want %q", errResp["error"], "project not found")
		}
	})
}

// TestCrossWorkspaceDeploymentTriggeringRejected verifies that attempting to trigger
// a deployment on a target belonging to Workspace B via Workspace A's endpoint
// is rejected with 404 (Not Found).
func TestCrossWorkspaceDeploymentTriggeringRejected(t *testing.T) {
	ctx := context.Background()
	h, repo, _, mockDB := setupDeploymentTestHandler()

	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// Seed connection and target in Workspace B
	connB, err := repo.CreateProviderConnection(ctx, deployment.CreateProviderConnectionInput{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(wsB),
		Provider:    "fake",
		Name:        "wsB-conn",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("create connB: %v", err)
	}

	targetB, err := repo.CreateTarget(ctx, deployment.CreateTargetInput{
		ID:           uuid.New(),
		WorkspaceID:  uuid.MustParse(wsB),
		ConnectionID: connB.ID,
		Name:         "wsB-target",
		Environment:  "production",
	})
	if err != nil {
		t.Fatalf("create targetB: %v", err)
	}

	// Caller from wsA attempts to trigger deployment on targetB using wsA endpoint
	body, _ := json.Marshal(map[string]any{
		"action":     "deploy",
		"commit_sha": "sha-cross-workspace-trigger",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployment-targets/"+targetB.ID.String()+"/deploy", bytes.NewReader(body))
	req = withMemberAuth(req, wsA, "admin", mockDB)
	req = withChiURLParams(req, map[string]string{"id": wsA, "targetId": targetB.ID.String()})
	w := httptest.NewRecorder()

	h.TriggerDeployment(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when triggering cross-workspace target, got %d: %s", w.Code, w.Body.String())
	}
	var errResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "deployment target not found" {
		t.Fatalf("error message = %q, want %q", errResp["error"], "deployment target not found")
	}
}

// TestUnauthorizedDeploymentActionRejected verifies that:
// 1. Triggering production deployments by non-admin members is rejected with 403 (Forbidden).
// 2. Access by callers without workspace membership is rejected with 404 (Not Found).
func TestUnauthorizedDeploymentActionRejected(t *testing.T) {
	ctx := context.Background()
	h, repo, _, mockDB := setupDeploymentTestHandler()

	wsA := uuid.New().String()

	connA, _ := repo.CreateProviderConnection(ctx, deployment.CreateProviderConnectionInput{
		ID:          uuid.New(),
		WorkspaceID: uuid.MustParse(wsA),
		Provider:    "fake",
		Name:        "wsA-conn",
		Enabled:     true,
	})

	targetA, _ := repo.CreateTarget(ctx, deployment.CreateTargetInput{
		ID:           uuid.New(),
		WorkspaceID:  uuid.MustParse(wsA),
		ConnectionID: connA.ID,
		Name:         "wsA-prod-target",
		Environment:  "production",
		ExternalID:   stringPtr("prod-project"),
		Config:       json.RawMessage(`{"repository":"owner/repo","branch":"main"}`),
	})

	// Sub-test 1: Role "member" attempting production deployment trigger -> 403 Forbidden
	t.Run("member_role_rejected_for_production_deploy", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"action":     "deploy",
			"commit_sha": "sha-prod-unauthorized",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployment-targets/"+targetA.ID.String()+"/deploy", bytes.NewReader(body))
		req = withMemberAuth(req, wsA, "member", mockDB) // regular member, NOT owner or admin
		req = withChiURLParams(req, map[string]string{"id": wsA, "targetId": targetA.ID.String()})
		w := httptest.NewRecorder()

		h.TriggerDeployment(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for member triggering production deploy, got %d: %s", w.Code, w.Body.String())
		}
		var errResp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &errResp)
		if errResp["error"] != "insufficient permissions" {
			t.Fatalf("error message = %q, want %q", errResp["error"], "insufficient permissions")
		}
	})

	// Sub-test 2: Non-member caller without valid workspace session -> 404 workspace not found
	t.Run("non_member_rejected", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"action":     "deploy",
			"commit_sha": "sha-prod-no-member",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployment-targets/"+targetA.ID.String()+"/deploy", bytes.NewReader(body))
		// No member context injected!
		req = withChiURLParams(req, map[string]string{"id": wsA, "targetId": targetA.ID.String()})
		w := httptest.NewRecorder()

		h.TriggerDeployment(w, req)
		// requireWorkspaceMember without user in context returns 401 or 404
		if w.Code != http.StatusNotFound && w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 404 or 401 for unauthenticated request, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Sub-test 3: Admin role attempting production deploy WITHOUT confirmation -> 400 Bad Request
	t.Run("admin_rejected_without_production_confirmation", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"action":     "deploy",
			"commit_sha": "sha-prod-unconfirmed",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployment-targets/"+targetA.ID.String()+"/deploy", bytes.NewReader(body))
		req = withMemberAuth(req, wsA, "admin", mockDB)
		req = withChiURLParams(req, map[string]string{"id": wsA, "targetId": targetA.ID.String()})
		w := httptest.NewRecorder()

		h.TriggerDeployment(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for unconfirmed production deploy, got %d: %s", w.Code, w.Body.String())
		}
		var errResp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &errResp)
		if errResp["error"] != "production deployment requires confirmation" {
			t.Fatalf("error message = %q, want %q", errResp["error"], "production deployment requires confirmation")
		}
	})

	// Sub-test 4: Admin role attempting production deploy WITH confirmation payload -> 202 Accepted
	t.Run("admin_succeeds_with_production_confirmation", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"action":             "deploy",
			"commit_sha":         "sha-prod-confirmed",
			"repository":         "owner/repo",
			"git_ref":            "main",
			"approval_reference": "APR-12345",
			"confirmed":          true,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsA+"/deployment-targets/"+targetA.ID.String()+"/deploy", bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", "idemp-prod-conf-1")
		req = withMemberAuth(req, wsA, "admin", mockDB)
		req = withChiURLParams(req, map[string]string{"id": wsA, "targetId": targetA.ID.String()})
		w := httptest.NewRecorder()

		h.TriggerDeployment(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("expected 202 for confirmed production deploy, got %d: %s", w.Code, w.Body.String())
		}
		var resp deploymentOperationResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode operation response: %v", err)
		}
		if resp.Status != deployment.StatusQueued {
			t.Fatalf("status = %q, want %q", resp.Status, deployment.StatusQueued)
		}
	})
}
