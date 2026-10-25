package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

type testVercelOAuthStateStore struct {
	values map[string]VercelOAuthState
}

func (s *testVercelOAuthStateStore) Put(_ context.Context, state string, value VercelOAuthState, _ time.Time) error {
	if s.values == nil {
		s.values = make(map[string]VercelOAuthState)
	}
	s.values[state] = value
	return nil
}

func (s *testVercelOAuthStateStore) Consume(_ context.Context, state string) (VercelOAuthState, error) {
	value, ok := s.values[state]
	if !ok {
		return VercelOAuthState{}, context.Canceled
	}
	delete(s.values, state)
	return value, nil
}

func TestVercelOAuthCallbackUsesStateWorkspaceAndRedactsTokens(t *testing.T) {
	workspaceA := uuid.New()
	workspaceB := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"callback-access","refresh_token":"callback-refresh","expires_in":3600}`))
		case "/v2/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user":{"id":"vercel-user","username":"owner","email":"owner@example.test"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	box, err := secretbox.New(make([]byte, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	repo := deployment.NewMemoryRepository()
	provider := providers.NewVercelProvider(server.URL, server.Client())
	registry := providers.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	oauth := providers.NewVercelOAuthClient("client-id", "client-secret", "https://api.example.test/callback", server.Client())
	oauth.Endpoints.Token = server.URL + "/token"
	oauth.Endpoints.Revoke = server.URL + "/revoke"
	stateStore := &testVercelOAuthStateStore{}
	state := "opaque-state"
	if err := stateStore.Put(context.Background(), state, VercelOAuthState{
		WorkspaceID: workspaceA, UserID: uuid.New(), CodeVerifier: "verifier",
	}, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		DeploymentRepository: repo,
		DeploymentProviders:  registry,
		DeploymentSecretBox:  box,
		VercelOAuthClient:    oauth,
		VercelOAuthStates:    stateStore,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/deployment/vercel/callback?state="+state+"&code=one-time-code&workspace_id="+workspaceB.String(), nil)
	recorder := httptest.NewRecorder()
	h.VercelOAuthCallback(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("callback status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "callback-access") || strings.Contains(recorder.Body.String(), "callback-refresh") {
		t.Fatal("oauth token appeared in callback response")
	}
	connections, err := repo.ListProviderConnections(context.Background(), workspaceA.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].WorkspaceID != workspaceA {
		t.Fatalf("callback did not persist to state workspace: %#v", connections)
	}
	otherConnections, err := repo.ListProviderConnections(context.Background(), workspaceB.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(otherConnections) != 0 {
		t.Fatalf("callback crossed workspace boundary: %#v", otherConnections)
	}
}

func TestVercelOAuthCallbackRejectsInvalidOrReplayedState(t *testing.T) {
	stateStore := &testVercelOAuthStateStore{values: map[string]VercelOAuthState{}}
	h := &Handler{VercelOAuthStates: stateStore}
	for _, state := range []string{"missing-state", "missing-state"} {
		req := httptest.NewRequest(http.MethodGet, "/api/integrations/deployment/vercel/callback?state="+state+"&code=code", nil)
		recorder := httptest.NewRecorder()
		h.VercelOAuthCallback(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid/replayed state status = %d", recorder.Code)
		}
	}
}
