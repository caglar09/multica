package deployment

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	core "github.com/multica-ai/multica/server/internal/deployment"
)

func TestVercelProviderValidatesTeamAndListsScopedProjects(t *testing.T) {
	var projectQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v2/teams":
			json.NewEncoder(w).Encode(map[string]any{"teams": []any{map[string]any{"id": "team_123", "name": "Acme", "slug": "acme"}}})
		case "/v10/projects":
			projectQuery = r.URL.Query()
			json.NewEncoder(w).Encode(map[string]any{
				"projects": []any{map[string]any{
					"id": "prj_1", "name": "web", "framework": "nextjs",
					"link": map[string]any{"repoId": 123456, "productionBranch": "main"},
				}},
				"pagination": map[string]any{"count": 1, "hasMore": false},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewVercelProvider(server.URL)
	creds := core.ConnectionCredentials{Config: json.RawMessage(`{"team_id":"team_123"}`), RawSecret: []byte("test-token")}
	info, err := provider.ValidateConnection(context.Background(), creds)
	if err != nil {
		t.Fatalf("ValidateConnection: %v", err)
	}
	if info.ScopeID != "team_123" || info.ScopeName != "Acme" {
		t.Fatalf("scope info = %+v", info)
	}
	targets, err := provider.ListTargets(context.Background(), core.ProviderConnection{Config: creds.Config}, creds)
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(targets) != 1 || targets[0].ExternalID != "prj_1" || targets[0].DefaultBranch != "main" {
		t.Fatalf("targets = %+v", targets)
	}
	var metadata map[string]string
	if err := json.Unmarshal(targets[0].Metadata, &metadata); err != nil || metadata["repo_id"] != "123456" {
		t.Fatalf("target metadata = %s", targets[0].Metadata)
	}
	if projectQuery.Get("teamId") != "team_123" {
		t.Fatalf("teamId query = %q", projectQuery.Get("teamId"))
	}
}

func TestVercelProviderUsesV10ProjectPagination(t *testing.T) {
	var queries []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/user":
			json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "user_1", "username": "owner"}})
		case "/v10/projects":
			queries = append(queries, r.URL.Query())
			if len(queries) == 1 {
				json.NewEncoder(w).Encode(map[string]any{
					"projects":   []any{map[string]any{"id": "prj_1", "name": "one"}},
					"pagination": map[string]any{"count": 1, "next": 123, "hasMore": true},
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"projects":   []any{map[string]any{"id": "prj_2", "name": "two"}},
				"pagination": map[string]any{"count": 1, "hasMore": false},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	targets, err := NewVercelProvider(server.URL).ListTargets(context.Background(), core.ProviderConnection{}, core.ConnectionCredentials{RawSecret: []byte("test-token")})
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(targets) != 2 || len(queries) != 2 || queries[1].Get("from") != "123" {
		t.Fatalf("targets=%+v queries=%v", targets, queries)
	}
}

func TestVercelProviderCreateTargetReturnsRedactedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/user":
			json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "user_1", "username": "caglar"}})
		case "/v10/projects":
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"message": "GitHub repository access denied: token=vercel-secret",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := NewVercelProvider(server.URL).CreateTarget(context.Background(), core.ProviderConnection{}, core.ConnectionCredentials{RawSecret: []byte("test-token")}, core.CreateTargetRequest{
		Name: "web-app", Environment: "preview", Repository: "github.com/acme/web-app", Branch: "main", Confirmed: true,
	})
	if err == nil {
		t.Fatal("CreateTarget succeeded unexpectedly")
	}
	var apiErr *core.ProviderAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("error = %T %v", err, err)
	}
	if strings.Contains(apiErr.Message, "vercel-secret") || !strings.Contains(apiErr.Message, "[REDACTED]") {
		t.Fatalf("message was not safely redacted: %q", apiErr.Message)
	}
}

func TestVercelProviderCreateRequiresConfirmationAndValidConfig(t *testing.T) {
	var createCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/user":
			json.NewEncoder(w).Encode(map[string]any{"user": map[string]string{"id": "user_1", "username": "owner", "name": "Owner"}})
		case "/v10/projects":
			createCalled = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] == "my-app" && (body["framework"] != "nextjs" || body["rootDirectory"] != "apps/web" || body["buildCommand"] != "pnpm build") {
				t.Fatalf("create body = %#v", body)
			}
			if _, ok := body["gitBranch"]; ok {
				t.Fatalf("create body must not contain top-level gitBranch: %#v", body)
			}
			if body["name"] == "my-app" {
				gitRepository, ok := body["gitRepository"].(map[string]any)
				if !ok || gitRepository["type"] != "github" || gitRepository["repo"] != "owner/repo" {
					t.Fatalf("gitRepository = %#v", body["gitRepository"])
				}
			}
			if body["name"] == "custom-app" {
				if _, ok := body["framework"]; ok {
					t.Fatalf("custom create body must omit framework: %#v", body)
				}
			}
			if body["name"] == "root-app" {
				if _, ok := body["rootDirectory"]; ok {
					t.Fatalf("root create body must omit rootDirectory: %#v", body)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"id": "prj_new", "name": "my-app", "framework": "nextjs",
				"link": map[string]any{"repoId": 987654, "productionBranch": "main"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := NewVercelProvider(server.URL)
	conn := core.ProviderConnection{}
	creds := core.ConnectionCredentials{RawSecret: []byte("test-token")}

	_, err := provider.CreateTarget(context.Background(), conn, creds, core.CreateTargetRequest{Name: "my-app"})
	if err != ErrVercelConfirmationRequired || createCalled {
		t.Fatalf("unconfirmed create err=%v called=%v", err, createCalled)
	}
	_, err = provider.CreateTarget(context.Background(), conn, creds, core.CreateTargetRequest{Name: "my app", Confirmed: true})
	if err != ErrVercelProjectName || createCalled {
		t.Fatalf("invalid-name create err=%v called=%v", err, createCalled)
	}
	_, err = provider.CreateTarget(context.Background(), conn, creds, core.CreateTargetRequest{Name: "my-app", Confirmed: true, Config: json.RawMessage(`{"rootDirectory":"../secret"}`)})
	if !strings.Contains(err.Error(), ErrVercelTargetConfig.Error()) || createCalled {
		t.Fatalf("invalid-config create err=%v called=%v", err, createCalled)
	}
	target, err := provider.CreateTarget(context.Background(), conn, creds, core.CreateTargetRequest{
		Name: "my-app", Confirmed: true, Framework: "nextjs", RootDirectory: "apps/web", BuildCommand: "pnpm build",
		Repository: "owner/repo", Branch: "release",
	})
	if err != nil || target.ExternalID != "prj_new" || !createCalled {
		t.Fatalf("created target=%+v err=%v called=%v", target, err, createCalled)
	}
	var createdMetadata map[string]string
	if err := json.Unmarshal(target.Metadata, &createdMetadata); err != nil || createdMetadata["repo_id"] != "987654" {
		t.Fatalf("created target metadata = %s", target.Metadata)
	}
	_, err = provider.CreateTarget(context.Background(), conn, creds, core.CreateTargetRequest{
		Name: "custom-app", Confirmed: true, Framework: "custom", Repository: "owner/repo", Branch: "release",
	})
	if err != nil {
		t.Fatalf("custom framework create: %v", err)
	}
	for _, rootDirectory := range []string{"", ".", "./"} {
		_, err = provider.CreateTarget(context.Background(), conn, creds, core.CreateTargetRequest{
			Name: "root-app", Confirmed: true, RootDirectory: rootDirectory,
		})
		if err != nil {
			t.Fatalf("root directory %q create: %v", rootDirectory, err)
		}
	}
}

func TestVercelProviderWrapsProjectResponseDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/user":
			json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "user_1", "username": "owner"}})
		case "/v10/projects":
			w.Write([]byte(`{"projects":`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := NewVercelProvider(server.URL).ListTargets(context.Background(), core.ProviderConnection{}, core.ConnectionCredentials{RawSecret: []byte("test-token")})
	if err == nil || !strings.Contains(err.Error(), "decode vercel list_targets response") || !strings.Contains(err.Error(), "unexpected EOF") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("decode error = %v", err)
	}
}

func TestVercelProviderTriggersGitDeploymentWithExplicitRevision(t *testing.T) {
	var request map[string]any
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v13/deployments" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		query = r.URL.Query()
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": "dpl_1", "readyState": "QUEUED", "url": "preview.example.test",
		})
	}))
	defer server.Close()

	provider := NewVercelProvider(server.URL)
	remote, err := provider.TriggerDeployment(context.Background(), core.DeploymentRequest{
		Environment: "production", CommitSHA: "abc123", Branch: "release/1",
		ExternalTarget: core.RemoteTarget{
			ExternalID: "prj_1", Name: "web", DefaultBranch: "main",
			Metadata: json.RawMessage(`{"repository":"acme/web","repository_type":"github","repo_id":"repo_1","team_id":"team_1"}`),
		},
		Credentials: core.ConnectionCredentials{RawSecret: []byte("test-token")},
	})
	if err != nil {
		t.Fatalf("TriggerDeployment: %v", err)
	}
	if remote.ProviderOperationID != "dpl_1" || remote.NormalizedStatus != core.StatusQueued {
		t.Fatalf("remote operation = %+v", remote)
	}
	if query.Get("teamId") != "team_1" || query.Get("forceNew") != "1" {
		t.Fatalf("query = %v", query)
	}
	if request["project"] != "prj_1" || request["target"] != "production" {
		t.Fatalf("request = %#v", request)
	}
	gitSource := request["gitSource"].(map[string]any)
	if gitSource["type"] != "github" || gitSource["repoId"] != "repo_1" || gitSource["repo"] != "acme/web" || gitSource["ref"] != "release/1" || gitSource["sha"] != "abc123" {
		t.Fatalf("gitSource = %#v", gitSource)
	}
}

func TestVercelProviderTriggersLatestBranchWithoutCommitSHA(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "dpl_1", "readyState": "QUEUED"})
	}))
	defer server.Close()

	_, err := NewVercelProvider(server.URL).TriggerDeployment(context.Background(), core.DeploymentRequest{
		Branch: "main",
		ExternalTarget: core.RemoteTarget{
			ExternalID: "prj_1",
			Metadata:   json.RawMessage(`{"repository":"acme/web","repository_type":"github","repo_id":"repo_1"}`),
		},
		Credentials: core.ConnectionCredentials{RawSecret: []byte("test-token")},
	})
	if err != nil {
		t.Fatalf("TriggerDeployment: %v", err)
	}
	gitSource := request["gitSource"].(map[string]any)
	if gitSource["ref"] != "main" {
		t.Fatalf("gitSource ref = %#v", gitSource["ref"])
	}
	if _, ok := gitSource["sha"]; ok {
		t.Fatalf("branch deployment must omit sha: %#v", gitSource)
	}
}

func TestVercelProviderRejectsDeploymentWithoutRepositoryID(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatal("Vercel deployment request must not be sent without repo_id")
	}))
	defer server.Close()

	_, err := NewVercelProvider(server.URL).TriggerDeployment(context.Background(), core.DeploymentRequest{
		CommitSHA: "abc123", Branch: "main",
		ExternalTarget: core.RemoteTarget{
			ExternalID: "prj_1",
			Metadata:   json.RawMessage(`{"repository":"acme/web","repository_type":"github"}`),
		},
		Credentials: core.ConnectionCredentials{RawSecret: []byte("test-token")},
	})
	if !errors.Is(err, ErrVercelRepositoryIDMissing) || called {
		t.Fatalf("missing repo_id error=%v called=%v", err, called)
	}
}

func TestVercelProviderGetsAndMapsDeploymentStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v13/deployments/dpl_1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("teamId") != "team_1" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": "dpl_1", "readyState": "ERROR", "errorMessage": "token=leak",
			"url": "failed.example.test", "inspectorUrl": "https://vercel.com/deployments/dpl_1",
		})
	}))
	defer server.Close()

	remote, err := NewVercelProvider(server.URL).GetOperation(context.Background(), core.OperationLookup{
		ProviderOperationID: "dpl_1",
		ExternalTarget:      core.RemoteTarget{Metadata: json.RawMessage(`{"team_id":"team_1"}`)},
		Credentials:         core.ConnectionCredentials{RawSecret: []byte("test-token")},
	})
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if remote.NormalizedStatus != core.StatusFailed || remote.DeploymentURL != "failed.example.test" || remote.InspectURL == "" {
		t.Fatalf("remote operation = %+v", remote)
	}
	if strings.Contains(remote.ErrorMessage, "leak") || !strings.Contains(remote.ErrorMessage, "[REDACTED]") {
		t.Fatalf("error message was not redacted: %q", remote.ErrorMessage)
	}
}

func TestVercelProviderGetsRedactedDeploymentLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v3/deployments/dpl_1/events" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("teamId") != "team_1" || r.URL.Query().Get("builds") != "1" || r.URL.Query().Get("limit") != "25" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"type": "stdout", "payload": map[string]any{"text": "Building application"}},
			{"type": "stderr", "payload": map[string]any{"text": "token=vercel-secret failed"}},
			{"type": "deployment-state", "payload": map[string]any{"info": map[string]any{"readyState": "READY"}}},
		})
	}))
	defer server.Close()

	result, err := NewVercelProvider(server.URL).GetLogs(context.Background(), core.LogRequest{
		ProviderOperationID: "dpl_1",
		ExternalTarget:      core.RemoteTarget{Metadata: json.RawMessage(`{"team_id":"team_1"}`)},
		Credentials:         core.ConnectionCredentials{RawSecret: []byte("test-token")},
		Limit:               25,
	})
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if len(result.Logs) != 3 || result.Logs[0] != "Building application" || result.Logs[2] != "deployment-state: READY" {
		t.Fatalf("logs = %#v", result.Logs)
	}
	if strings.Contains(result.Logs[1], "vercel-secret") || !strings.Contains(result.Logs[1], "[REDACTED]") {
		t.Fatalf("log was not redacted: %q", result.Logs[1])
	}
}

func TestVercelProviderVerifiesAndMapsWebhookEvents(t *testing.T) {
	secret := "webhook-secret"
	for _, test := range []struct {
		typeName string
		status   core.OperationStatus
	}{
		{typeName: "deployment.created", status: core.StatusQueued},
		{typeName: "deployment.building", status: core.StatusBuilding},
		{typeName: "deployment.ready", status: core.StatusReady},
		{typeName: "deployment.error", status: core.StatusFailed},
		{typeName: "deployment.canceled", status: core.StatusCancelled},
	} {
		t.Run(test.typeName, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"id":      "evt_" + test.typeName,
				"type":    test.typeName,
				"payload": map[string]any{"deployment": map[string]any{"id": "dpl_1", "url": "preview.example.test"}},
			})
			mac := hmac.New(sha1.New, []byte(secret))
			_, _ = mac.Write(body)
			event, err := NewVercelProvider("").ParseWebhook(context.Background(), core.WebhookRequest{
				RawBody: body, SigningSecret: []byte(secret),
				Headers: map[string][]string{"X-Vercel-Signature": {hex.EncodeToString(mac.Sum(nil))}},
			})
			if err != nil {
				t.Fatalf("ParseWebhook: %v", err)
			}
			if event.ProviderOperationID != "dpl_1" || event.ObservedStatus != test.status {
				t.Fatalf("event = %+v", event)
			}
		})
	}

	body := []byte(`{"id":"evt-1","type":"deployment.ready","payload":{"deployment":{"id":"dpl_1"}}}`)
	_, err := NewVercelProvider("").ParseWebhook(context.Background(), core.WebhookRequest{
		RawBody: body, SigningSecret: []byte(secret), Headers: map[string][]string{"x-vercel-signature": {"bad"}},
	})
	if !errors.Is(err, core.ErrWebhookSignatureInvalid) {
		t.Fatalf("invalid signature error = %v", err)
	}
}
