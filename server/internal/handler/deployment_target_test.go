package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

type failingProviderTargetCreator struct {
	*providers.FakeProvider
	err error
}

func (p *failingProviderTargetCreator) CreateTarget(context.Context, deployment.ProviderConnection, deployment.ConnectionCredentials, deployment.CreateTargetRequest) (deployment.RemoteTarget, error) {
	return deployment.RemoteTarget{}, p.err
}

type failingProviderTargetLister struct {
	*providers.FakeProvider
	err error
}

func (p *failingProviderTargetLister) ListTargets(context.Context, deployment.ProviderConnection, deployment.ConnectionCredentials) ([]deployment.RemoteTarget, error) {
	return nil, p.err
}

type failingTargetRepository struct {
	*deployment.MemoryRepository
	err error
}

func (r *failingTargetRepository) CreateTarget(context.Context, deployment.CreateTargetInput) (deployment.Target, error) {
	return deployment.Target{}, r.err
}

type repositoryIDTargetProvider struct {
	*providers.FakeProvider
}

func (p *repositoryIDTargetProvider) CreateTarget(ctx context.Context, conn deployment.ProviderConnection, creds deployment.ConnectionCredentials, req deployment.CreateTargetRequest) (deployment.RemoteTarget, error) {
	target, err := p.FakeProvider.CreateTarget(ctx, conn, creds, req)
	if err != nil {
		return deployment.RemoteTarget{}, err
	}
	target.Metadata = json.RawMessage(`{"repository":"owner/repo","repository_type":"github","repo_id":"repo_new"}`)
	return target, nil
}

func newTargetFlowFixture(t *testing.T) (*Handler, *deployment.MemoryRepository, *providers.FakeProvider, *mockDeploymentDB, uuid.UUID, deployment.ProviderConnection) {
	t.Helper()
	h, repo, fake, mockDB := setupDeploymentTestHandler()
	key := bytes.Repeat([]byte("k"), secretbox.KeySize)
	keyRing, err := secretbox.NewKeyRing(1, map[uint8][]byte{1: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.DeploymentKeyRing = keyRing
	sealed, err := keyRing.Seal([]byte("provider-token"))
	if err != nil {
		t.Fatal(err)
	}
	secretRef := base64.StdEncoding.EncodeToString(sealed)
	workspaceID := uuid.New()
	connection, err := repo.CreateProviderConnection(context.Background(), deployment.CreateProviderConnectionInput{
		ID: uuid.New(), WorkspaceID: workspaceID, Provider: "fake", Name: "fake", SecretRef: &secretRef, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, repo, fake, mockDB, workspaceID, connection
}

func targetRequest(t *testing.T, method, path string, body any, workspaceID uuid.UUID, role string, mockDB *mockDeploymentDB) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req = withMemberAuth(req, workspaceID.String(), role, mockDB)
	return withChiURLParams(req, map[string]string{"id": workspaceID.String()})
}

func TestDeploymentTargetAttachVerifiesExternalTargetAndPersistsPolicy(t *testing.T) {
	h, repo, fake, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	remote, err := fake.CreateTarget(context.Background(), deployment.ProviderConnection{}, deployment.ConnectionCredentials{}, deployment.CreateTargetRequest{
		Name: "existing", Environment: "production", Repository: "owner/repo", Branch: "main", Confirmed: true,
		Config: json.RawMessage(`{"repository":"owner/repo","default_branch":"main","repo_id":"repo_existing"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-targets", map[string]any{
		"connection_id": connection.ID.String(), "name": "Production", "environment": "production",
		"external_id": remote.ExternalID, "config": map[string]any{"default_branch": "release"},
	}, workspaceID, "member", mockDB)
	rec := httptest.NewRecorder()
	h.CreateDeploymentTarget(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("attach status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response deploymentTargetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	target, err := repo.GetTarget(context.Background(), workspaceID.String(), response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.ExternalID == nil || *target.ExternalID != remote.ExternalID || string(target.Config) == "" {
		t.Fatalf("target=%+v", target)
	}
	if !bytes.Contains(target.Config, []byte(`"repository":"owner/repo"`)) || !bytes.Contains(target.Config, []byte(`"default_branch":"release"`)) || !bytes.Contains(target.Config, []byte(`"repo_id":"repo_existing"`)) {
		t.Fatalf("source policy was not persisted: %s", target.Config)
	}
}

func TestDeploymentProviderTargetCreateRequiresConfirmationAndBindsExternalID(t *testing.T) {
	h, repo, fake, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	provider := &repositoryIDTargetProvider{FakeProvider: fake}
	registry := providers.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	h.DeploymentProviders = registry
	base := map[string]any{
		"connection_id": connection.ID.String(), "name": "New project", "environment": "preview",
		"repository": "owner/repo", "branch": "develop", "config": map[string]any{"framework": "nextjs"},
	}
	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-providers/connections/"+connection.ID.String()+"/targets", base, workspaceID, "admin", mockDB)
	rec := httptest.NewRecorder()
	h.CreateDeploymentProviderTarget(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed create status=%d body=%s", rec.Code, rec.Body.String())
	}
	if targets, _ := fake.ListTargets(context.Background(), connection, deployment.ConnectionCredentials{}); len(targets) != 0 {
		t.Fatalf("unconfirmed create made remote target: %+v", targets)
	}

	base["confirmed"] = true
	req = targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-providers/connections/"+connection.ID.String()+"/targets", base, workspaceID, "admin", mockDB)
	rec = httptest.NewRecorder()
	h.CreateDeploymentProviderTarget(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("confirmed create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response deploymentTargetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ExternalID == nil || *response.ExternalID == "" {
		t.Fatalf("created target is not bound to external id: %+v", response)
	}
	if len(repoTargets(t, repo, workspaceID)) != 1 {
		t.Fatal("confirmed create did not persist local target")
	}
	target := repoTargets(t, repo, workspaceID)[0]
	if !bytes.Contains(target.Config, []byte(`"repo_id":"repo_new"`)) {
		t.Fatalf("created target repository id was not persisted: %+v", target)
	}
}

func TestDeploymentProviderTargetCreateExposesSafeProviderErrorAndStructuredLog(t *testing.T) {
	h, _, _, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	provider := &failingProviderTargetCreator{
		FakeProvider: providers.NewFakeProvider(),
		err: &deployment.ProviderAPIError{
			StatusCode: http.StatusForbidden,
			Message:    "GitHub repository access denied: token=vercel-secret",
		},
	}
	registry := providers.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	h.DeploymentProviders = registry

	var logs strings.Builder
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-providers/connections/"+connection.ID.String()+"/targets", map[string]any{
		"connection_id": connection.ID.String(), "name": "GitHub app", "environment": "preview",
		"repository": "owner/repo", "branch": "main", "confirmed": true,
	}, workspaceID, "admin", mockDB)
	rec := httptest.NewRecorder()
	h.CreateDeploymentProviderTarget(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("provider failure status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "GitHub repository access denied") || strings.Contains(rec.Body.String(), "vercel-secret") {
		t.Fatalf("unsafe provider response: %s", rec.Body.String())
	}
	logOutput := logs.String()
	for _, field := range []string{"provider=fake", "operation=create_target", "http_status=403", "provider_error_message"} {
		if !strings.Contains(logOutput, field) {
			t.Fatalf("structured log missing %q: %s", field, logOutput)
		}
	}
	if strings.Contains(logOutput, "vercel-secret") {
		t.Fatalf("provider secret leaked into structured log: %s", logOutput)
	}
}

func TestDeploymentProviderTargetDiscoveryExposesSafeProviderErrorAndStructuredLog(t *testing.T) {
	h, _, _, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	provider := &failingProviderTargetLister{
		FakeProvider: providers.NewFakeProvider(),
		err: &deployment.ProviderAPIError{
			StatusCode: http.StatusForbidden,
			Message:    "project access denied: token=vercel-secret",
		},
	}
	registry := providers.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	h.DeploymentProviders = registry

	var logs strings.Builder
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	req := targetRequest(t, http.MethodGet, "/api/workspaces/"+workspaceID.String()+"/deployment-providers/connections/"+connection.ID.String()+"/targets", nil, workspaceID, "member", mockDB)
	req = withChiURLParams(req, map[string]string{"id": workspaceID.String(), "connectionId": connection.ID.String()})
	rec := httptest.NewRecorder()
	h.ListDeploymentProviderTargets(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("provider discovery status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project access denied") || strings.Contains(rec.Body.String(), "vercel-secret") {
		t.Fatalf("unsafe provider response: %s", rec.Body.String())
	}
	logOutput := logs.String()
	for _, field := range []string{"provider=fake", "operation=list_targets", "http_status=403", "provider_error_message"} {
		if !strings.Contains(logOutput, field) {
			t.Fatalf("structured log missing %q: %s", field, logOutput)
		}
	}
	if strings.Contains(logOutput, "vercel-secret") {
		t.Fatalf("provider secret leaked into structured log: %s", logOutput)
	}
}

func TestDeploymentTargetCreateRejectsMissingExternalIDAndCrossWorkspaceConnection(t *testing.T) {
	h, _, _, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-targets", map[string]any{
		"connection_id": connection.ID.String(), "name": "Missing ID", "environment": "preview",
		"config": map[string]any{"repository": "owner/repo", "default_branch": "main"},
	}, workspaceID, "member", mockDB)
	rec := httptest.NewRecorder()
	h.CreateDeploymentTarget(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing external id status=%d body=%s", rec.Code, rec.Body.String())
	}

	otherWorkspace := uuid.New()
	path := "/api/workspaces/" + otherWorkspace.String() + "/deployment-targets"
	req = targetRequest(t, http.MethodPost, path, map[string]any{
		"connection_id": connection.ID.String(), "name": "Cross workspace", "environment": "preview",
		"external_id": "remote-1", "config": map[string]any{"repository": "owner/repo", "default_branch": "main"},
	}, otherWorkspace, "member", mockDB)
	rec = httptest.NewRecorder()
	h.CreateDeploymentTarget(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeploymentRejectsTargetWithoutRepositoryOrRefPolicy(t *testing.T) {
	h, repo, _, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	target, err := repo.CreateTarget(context.Background(), deployment.CreateTargetInput{
		ID: uuid.New(), WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Invalid", Environment: "preview",
		ExternalID: stringPtr("remote-1"), Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-targets/"+target.ID.String()+"/deploy", map[string]any{
		"commit_sha": "sha-1", "git_ref": "main",
	}, workspaceID, "member", mockDB)
	req = withChiURLParams(req, map[string]string{"id": workspaceID.String(), "targetId": target.ID.String()})
	rec := httptest.NewRecorder()
	h.TriggerDeployment(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid target deploy status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeploymentRejectsSourceRevisionFromDifferentRepository(t *testing.T) {
	h, repo, _, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	target, err := repo.CreateTarget(context.Background(), deployment.CreateTargetInput{
		ID: uuid.New(), WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Clock time", Environment: "preview",
		ExternalID: stringPtr("remote-1"), Config: json.RawMessage(`{"repository":"caglar09/clock-time","default_branch":"main"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-targets/"+target.ID.String()+"/deploy", map[string]any{
		"repository": "caglar09/multica", "git_ref": "main", "commit_sha": "c82e4ff",
	}, workspaceID, "member", mockDB)
	req = withChiURLParams(req, map[string]string{"id": workspaceID.String(), "targetId": target.ID.String()})
	rec := httptest.NewRecorder()
	h.TriggerDeployment(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched repository deploy status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "source repository does not match deployment target") {
		t.Fatalf("unexpected mismatch error: %s", rec.Body.String())
	}
}

func TestDeploymentAcceptsVerifiedTargetBranchWithoutCommitSHA(t *testing.T) {
	h, repo, _, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	target, err := repo.CreateTarget(context.Background(), deployment.CreateTargetInput{
		ID: uuid.New(), WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Clock time", Environment: "preview",
		ExternalID: stringPtr("remote-1"), Config: json.RawMessage(`{"repository":"caglar09/clock-time","default_branch":"main"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-targets/"+target.ID.String()+"/deploy", map[string]any{
		"repository": "https://github.com/caglar09/clock-time.git", "git_ref": "main",
	}, workspaceID, "member", mockDB)
	req = withChiURLParams(req, map[string]string{"id": workspaceID.String(), "targetId": target.ID.String()})
	rec := httptest.NewRecorder()
	h.TriggerDeployment(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("branch deploy status=%d body=%s", rec.Code, rec.Body.String())
	}
	operations, err := repo.ListOperations(context.Background(), workspaceID.String(), target.ID.String())
	if err != nil || len(operations) != 1 {
		t.Fatalf("operations=%+v err=%v", operations, err)
	}
	if !strings.HasPrefix(operations[0].SourceRevision, "ref:main@") {
		t.Fatalf("source revision = %q", operations[0].SourceRevision)
	}
}

func TestDeploymentProviderTargetCreateCompensatesRemoteOnLocalFailure(t *testing.T) {
	h, repo, fake, mockDB, workspaceID, connection := newTargetFlowFixture(t)
	h.DeploymentRepository = &failingTargetRepository{MemoryRepository: repo, err: context.DeadlineExceeded}
	req := targetRequest(t, http.MethodPost, "/api/workspaces/"+workspaceID.String()+"/deployment-providers/connections/"+connection.ID.String()+"/targets", map[string]any{
		"connection_id": connection.ID.String(), "name": "Orphan safe", "environment": "preview",
		"repository": "owner/repo", "branch": "main", "confirmed": true,
	}, workspaceID, "admin", mockDB)
	rec := httptest.NewRecorder()
	h.CreateDeploymentProviderTarget(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("persistence failure status=%d body=%s", rec.Code, rec.Body.String())
	}
	if targets, _ := fake.ListTargets(context.Background(), connection, deployment.ConnectionCredentials{}); len(targets) != 0 {
		t.Fatalf("remote target was not compensated: %+v", targets)
	}
}

func repoTargets(t *testing.T, repo *deployment.MemoryRepository, workspaceID uuid.UUID) []deployment.Target {
	t.Helper()
	targets, err := repo.ListTargets(context.Background(), workspaceID.String())
	if err != nil {
		t.Fatal(err)
	}
	return targets
}
