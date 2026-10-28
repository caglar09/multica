package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// These response types are intentionally separate from the persistence models.
// In particular, SecretRef is ciphertext custody metadata, not an API field.
type deploymentConnectionResponse struct {
	ID                string                   `json:"id"`
	WorkspaceID       string                   `json:"workspace_id"`
	Provider          string                   `json:"provider"`
	Name              string                   `json:"name"`
	Enabled           bool                     `json:"enabled"`
	HasCredentials    bool                     `json:"has_credentials"`
	Capabilities      deployment.CapabilitySet `json:"capabilities,omitempty"`
	Config            json.RawMessage          `json:"config"`
	WebhookURL        string                   `json:"webhook_url"`
	WebhookConfigured bool                     `json:"webhook_configured"`
	WebhookStatus     string                   `json:"webhook_status"`
	CreatedAt         string                   `json:"created_at"`
	UpdatedAt         string                   `json:"updated_at"`
}

type deploymentTargetResponse struct {
	ID           string                   `json:"id"`
	WorkspaceID  string                   `json:"workspace_id"`
	ProjectID    string                   `json:"project_id,omitempty"`
	ConnectionID string                   `json:"connection_id"`
	Name         string                   `json:"name"`
	Environment  string                   `json:"environment"`
	ExternalID   *string                  `json:"external_id,omitempty"`
	Config       json.RawMessage          `json:"config"`
	Capabilities deployment.CapabilitySet `json:"capabilities,omitempty"`
	CreatedAt    string                   `json:"created_at"`
	UpdatedAt    string                   `json:"updated_at"`
}

type deploymentConnectionRequest struct {
	Provider      string          `json:"provider"`
	Name          string          `json:"name"`
	Credentials   json.RawMessage `json:"credentials"`
	Token         json.RawMessage `json:"token"`
	AccessToken   json.RawMessage `json:"access_token"`
	WebhookSecret json.RawMessage `json:"webhook_secret"`
	Config        json.RawMessage `json:"config"`
	Enabled       *bool           `json:"enabled"`
}

type deploymentTargetRequest struct {
	ProjectID     string          `json:"project_id"`
	ConnectionID  string          `json:"connection_id"`
	Name          string          `json:"name"`
	Environment   string          `json:"environment"`
	Repository    string          `json:"repository"`
	Branch        string          `json:"branch"`
	Framework     string          `json:"framework"`
	RootDirectory string          `json:"root_directory"`
	BuildCommand  string          `json:"build_command"`
	Confirmed     bool            `json:"confirmed"`
	Confirm       bool            `json:"confirm"`
	ExternalID    *string         `json:"external_id"`
	Config        json.RawMessage `json:"config"`
}

type deploymentTargetPatch struct {
	ProjectID   *string          `json:"project_id"`
	Name        *string          `json:"name"`
	Environment *string          `json:"environment"`
	ExternalID  *string          `json:"external_id"`
	Config      *json.RawMessage `json:"config"`
}

type deploymentActionRequest struct {
	Action            string `json:"action"`
	Repository        string `json:"repository"`
	GitRef            string `json:"git_ref"`
	CommitSHA         string `json:"commit_sha"`
	CommitMessage     string `json:"commit_message"`
	Environment       string `json:"environment"`
	ApprovalReference string `json:"approval_reference"`
	Confirmed         bool   `json:"confirmed"`
	Confirm           bool   `json:"confirm"`
}

type deploymentPromotionRequest struct {
	CandidateOperationID string `json:"candidate_operation_id"`
	ApprovalReference    string `json:"approval_reference"`
}

type deploymentRollbackRequest struct {
	TargetOperationID string `json:"target_operation_id"`
	ApprovalReference string `json:"approval_reference"`
}

func (h *Handler) deploymentRegistry() *providers.Registry {
	if h.DeploymentProviders == nil {
		return providers.NewRegistry()
	}
	return h.DeploymentProviders
}

func (h *Handler) deploymentRepository() deployment.Repository {
	return h.DeploymentRepository
}

func (h *Handler) deploymentWorkspace(w http.ResponseWriter, r *http.Request, admin bool) (string, bool) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id"); !ok {
		return "", false
	}
	if admin {
		if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
			return "", false
		}
	} else if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return "", false
	}
	return workspaceID, true
}

func decodeDeploymentJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func deploymentConfig(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	var v any
	if json.Unmarshal(value, &v) != nil {
		return json.RawMessage(`{}`)
	}
	return redactDeploymentJSON(v)
}

func redactDeploymentJSON(v any) json.RawMessage {
	var redact func(any) any
	redact = func(value any) any {
		switch x := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(x))
			for key, item := range x {
				lower := strings.ToLower(key)
				if slicesContainSensitive(lower) {
					out[key] = "[REDACTED]"
				} else {
					out[key] = redact(item)
				}
			}
			return out
		case []any:
			for i := range x {
				x[i] = redact(x[i])
			}
		}
		return value
	}
	return mustJSON(redact(v))
}

func slicesContainSensitive(key string) bool {
	for _, name := range deployment.SensitiveKeyNames {
		if strings.Contains(key, name) {
			return true
		}
	}
	return false
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

func (h *Handler) deploymentConnectionResponse(c deployment.ProviderConnection) deploymentConnectionResponse {
	webhookConfigured := c.WebhookSecretRef != nil && strings.TrimSpace(*c.WebhookSecretRef) != ""
	webhookStatus := "polling_only"
	if p, err := h.deploymentRegistry().Get(c.Provider); err == nil && !p.Capabilities().Webhook {
		webhookStatus = "unsupported"
	} else if webhookConfigured {
		webhookStatus = "configured"
	}
	result := deploymentConnectionResponse{
		ID: c.ID.String(), WorkspaceID: c.WorkspaceID.String(), Provider: c.Provider, Name: c.Name,
		Enabled: c.Enabled, HasCredentials: c.SecretRef != nil, Config: deploymentConfig(c.Config),
		WebhookURL:        strings.TrimRight(h.cfg.PublicURL, "/") + "/api/webhooks/deployments/" + c.WorkspaceID.String() + "/" + c.ID.String(),
		WebhookConfigured: webhookConfigured, WebhookStatus: webhookStatus,
		CreatedAt: c.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"), UpdatedAt: c.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"),
	}
	if strings.TrimSpace(h.cfg.PublicURL) == "" {
		result.WebhookURL = "/api/webhooks/deployments/" + c.WorkspaceID.String() + "/" + c.ID.String()
	}
	if p, err := h.deploymentRegistry().Get(c.Provider); err == nil {
		result.Capabilities = p.Capabilities()
	}
	return result
}

func (h *Handler) deploymentTargetResponse(t deployment.Target) deploymentTargetResponse {
	projectID := ""
	var cfg map[string]any
	if json.Unmarshal(t.Config, &cfg) == nil {
		if value, ok := cfg["project_id"].(string); ok {
			projectID = value
		}
	}
	result := deploymentTargetResponse{
		ID: t.ID.String(), WorkspaceID: t.WorkspaceID.String(), ProjectID: projectID,
		ConnectionID: t.ConnectionID.String(), Name: t.Name, Environment: t.Environment,
		ExternalID: t.ExternalID, Config: deploymentConfig(t.Config),
		CreatedAt: t.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"), UpdatedAt: t.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"),
	}
	return result
}

func (h *Handler) sealDeploymentSecret(secret []byte) ([]byte, error) {
	if h.DeploymentKeyRing != nil {
		return h.DeploymentKeyRing.Seal(secret)
	}
	if h.DeploymentSecretBox != nil {
		return h.DeploymentSecretBox.SealVersioned(1, secret)
	}
	return nil, errors.New("deployment credentials are not configured")
}

func (h *Handler) openDeploymentSecret(b []byte) ([]byte, error) {
	if h.DeploymentKeyRing != nil {
		return h.DeploymentKeyRing.Open(b)
	}
	if h.DeploymentSecretBox != nil {
		return h.DeploymentSecretBox.Open(b)
	}
	return nil, errors.New("deployment credentials are not configured")
}

func (h *Handler) deploymentCredentials(ctx context.Context, c deployment.ProviderConnection) (deployment.ConnectionCredentials, error) {
	if c.SecretRef == nil || strings.TrimSpace(*c.SecretRef) == "" {
		return deployment.ConnectionCredentials{}, errors.New("deployment credentials are missing")
	}
	if h.DeploymentSecretBox == nil && h.DeploymentKeyRing == nil {
		return deployment.ConnectionCredentials{}, errors.New("deployment credentials are not configured")
	}
	b, err := base64.StdEncoding.DecodeString(*c.SecretRef)
	if err != nil {
		return deployment.ConnectionCredentials{}, errors.New("stored deployment credentials are invalid")
	}
	secret, err := h.openDeploymentSecret(b)
	if err != nil {
		return deployment.ConnectionCredentials{}, errors.New("stored deployment credentials could not be opened")
	}
	if c.Provider == "vercel" && deploymentAuthMode(c.Config) == "oauth" {
		tokens, err := providers.DecodeVercelOAuthCredentials(secret)
		if err != nil {
			deployment.ScrubBytes(secret)
			return deployment.ConnectionCredentials{}, errors.New("stored vercel oauth credentials are invalid")
		}
		if tokens.NeedsRefresh(time.Now()) {
			refreshed, err := h.vercelOAuthClient().Refresh(ctx, tokens.RefreshToken)
			if err != nil {
				deployment.ScrubBytes(secret)
				return deployment.ConnectionCredentials{}, errors.New("stored vercel oauth credentials could not be refreshed")
			}
			envelope, err := providers.EncodeVercelOAuthCredentials(refreshed)
			if err != nil {
				deployment.ScrubBytes(secret)
				return deployment.ConnectionCredentials{}, errors.New("stored vercel oauth credentials are invalid")
			}
			sealed, err := h.sealDeploymentSecret(envelope)
			if err != nil {
				deployment.ScrubBytes(secret)
				deployment.ScrubBytes(envelope)
				return deployment.ConnectionCredentials{}, errors.New("stored vercel oauth credentials could not be sealed")
			}
			secretRef := base64.StdEncoding.EncodeToString(sealed)
			config := deploymentConfigMap(c.Config)
			config["token_expires_at"] = refreshed.ExpiresAt.UTC().Format(time.RFC3339)
			if _, err := h.deploymentRepository().UpdateProviderConnection(ctx, deployment.UpdateProviderConnectionInput{
				ID: c.ID, WorkspaceID: c.WorkspaceID, Name: c.Name, SecretRef: &secretRef,
				WebhookSecretRef: c.WebhookSecretRef, Config: mustJSON(config), Enabled: c.Enabled,
			}); err != nil {
				deployment.ScrubBytes(secret)
				deployment.ScrubBytes(envelope)
				return deployment.ConnectionCredentials{}, errors.New("stored vercel oauth credentials could not be persisted")
			}
			deployment.ScrubBytes(secret)
			secret = envelope
			return deployment.ConnectionCredentials{SecretRef: secretRef, Config: mustJSON(config), RawSecret: secret}, nil
		}
	}
	return deployment.ConnectionCredentials{SecretRef: *c.SecretRef, Config: c.Config, RawSecret: secret}, nil
}

func (h *Handler) loadDeploymentConnection(w http.ResponseWriter, r *http.Request, workspaceID, id string) (deployment.ProviderConnection, bool) {
	if _, ok := parseUUIDOrBadRequest(w, id, "connection id"); !ok {
		return deployment.ProviderConnection{}, false
	}
	c, err := h.deploymentRepository().GetProviderConnection(r.Context(), workspaceID, id)
	if errors.Is(err, deployment.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment connection not found")
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load deployment connection")
	}
	return c, err == nil
}

func (h *Handler) loadDeploymentTarget(w http.ResponseWriter, r *http.Request, workspaceID, id string) (deployment.Target, bool) {
	if _, ok := parseUUIDOrBadRequest(w, id, "target id"); !ok {
		return deployment.Target{}, false
	}
	t, err := h.deploymentRepository().GetTarget(r.Context(), workspaceID, id)
	if errors.Is(err, deployment.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment target not found")
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load deployment target")
	}
	return t, err == nil
}

func (h *Handler) ensureProject(w http.ResponseWriter, r *http.Request, workspaceID, projectID string) bool {
	if projectID == "" {
		return true
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, projectID, "project id")
	if !ok {
		return false
	}
	if h.Queries == nil {
		writeError(w, http.StatusInternalServerError, "failed to validate project")
		return false
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectUUID, WorkspaceID: parseUUID(workspaceID)}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return false
	}
	return true
}

func targetSourceValue(config map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := config[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func targetSourcePolicy(input deploymentTargetRequest, metadata json.RawMessage, remoteBranch string) (json.RawMessage, string, string, error) {
	config := map[string]any{}
	if len(input.Config) > 0 && string(input.Config) != "null" {
		if err := json.Unmarshal(input.Config, &config); err != nil || config == nil {
			return nil, "", "", errors.New("target config must be an object")
		}
	}
	var remoteConfig map[string]any
	_ = json.Unmarshal(metadata, &remoteConfig)
	repository := strings.TrimSpace(input.Repository)
	if repository == "" {
		repository = targetSourceValue(config, "repository", "repo")
	}
	if repository == "" {
		repository = targetSourceValue(remoteConfig, "repository", "repo")
	}
	branch := strings.TrimSpace(input.Branch)
	if branch == "" {
		branch = targetSourceValue(config, "default_branch", "branch", "ref_policy")
	}
	if branch == "" {
		branch = strings.TrimSpace(remoteBranch)
	}
	if branch == "" {
		branch = targetSourceValue(remoteConfig, "default_branch", "branch", "ref_policy")
	}
	if repository == "" || branch == "" {
		return nil, "", "", errors.New("repository and ref policy are required")
	}
	if len(repository) > 2048 || len(branch) > 512 || strings.ContainsAny(repository+branch, "\r\n\x00") {
		return nil, "", "", errors.New("repository and ref policy are invalid")
	}
	config["repository"] = repository
	config["default_branch"] = branch
	if repoID := targetSourceValue(remoteConfig, "repo_id", "repoId"); repoID != "" {
		config["repo_id"] = repoID
	}
	return mustJSON(config), repository, branch, nil
}

func targetExternalID(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func normalizeDeploymentRepository(repository string) string {
	repository = strings.TrimSpace(strings.TrimSuffix(repository, ".git"))
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/", "git@github.com:"} {
		if strings.HasPrefix(repository, prefix) {
			return strings.Trim(repository[len(prefix):], "/")
		}
	}
	return strings.Trim(repository, "/")
}

func normalizeDeploymentRef(ref string) string {
	ref = strings.TrimSpace(ref)
	for _, prefix := range []string{"refs/heads/", "refs/tags/"} {
		ref = strings.TrimPrefix(ref, prefix)
	}
	return ref
}

func validateDeploymentSourceRevision(target deployment.Target, req deploymentActionRequest) error {
	_, targetRepository, targetBranch, err := targetSourcePolicy(deploymentTargetRequest{Config: target.Config}, nil, "")
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.GitRef) == "" {
		return errors.New("git_ref is required")
	}
	if strings.TrimSpace(req.Repository) == "" {
		return errors.New("source repository is required")
	}
	if normalizeDeploymentRepository(req.Repository) != normalizeDeploymentRepository(targetRepository) {
		return errors.New("source repository does not match deployment target")
	}
	if normalizeDeploymentRef(req.GitRef) != normalizeDeploymentRef(targetBranch) {
		return errors.New("source ref does not match deployment target")
	}
	return nil
}

func deploymentSecret(req deploymentConnectionRequest) ([]byte, bool) {
	if len(req.Credentials) != 0 && string(req.Credentials) != "null" {
		var value any
		if json.Unmarshal(req.Credentials, &value) == nil {
			return append([]byte(nil), req.Credentials...), true
		}
	}
	for _, raw := range []json.RawMessage{req.Token, req.AccessToken} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			value = strings.TrimSpace(value)
			return []byte(value), value != ""
		}
		return append([]byte(nil), raw...), true
	}
	return nil, false
}

func deploymentWebhookSecretInput(req deploymentConnectionRequest) ([]byte, bool) {
	if len(req.WebhookSecret) == 0 || string(req.WebhookSecret) == "null" {
		return nil, false
	}
	var value string
	if json.Unmarshal(req.WebhookSecret, &value) != nil {
		return nil, false
	}
	value = strings.TrimSpace(value)
	return []byte(value), value != ""
}

func (h *Handler) deploymentWebhookSecret(c deployment.ProviderConnection) ([]byte, error) {
	if c.WebhookSecretRef == nil || strings.TrimSpace(*c.WebhookSecretRef) == "" {
		return nil, errors.New("deployment webhook secret is missing")
	}
	if h.DeploymentSecretBox == nil && h.DeploymentKeyRing == nil {
		return nil, errors.New("deployment webhook secret is not configured")
	}
	b, err := base64.StdEncoding.DecodeString(*c.WebhookSecretRef)
	if err != nil {
		return nil, errors.New("stored deployment webhook secret is invalid")
	}
	secret, err := h.openDeploymentSecret(b)
	if err != nil {
		return nil, errors.New("stored deployment webhook secret could not be opened")
	}
	return secret, nil
}

func jsonHasKey(raw json.RawMessage, wanted string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var visit func(any) bool
	visit = func(value any) bool {
		switch value := value.(type) {
		case map[string]any:
			for key, nested := range value {
				if strings.EqualFold(key, wanted) || visit(nested) {
					return true
				}
			}
		case []any:
			for _, nested := range value {
				if visit(nested) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func (h *Handler) ListDeploymentConnections(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	rows, err := h.deploymentRepository().ListProviderConnections(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deployment connections")
		return
	}
	out := make([]deploymentConnectionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.deploymentConnectionResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) CreateDeploymentConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	var req deploymentConnectionRequest
	if !decodeDeploymentJSON(w, r, &req) {
		return
	}
	providerKind := strings.TrimSpace(req.Provider)
	name := strings.TrimSpace(req.Name)
	if providerKind == "" || name == "" {
		writeError(w, http.StatusBadRequest, "provider and name are required")
		return
	}
	p, err := h.deploymentRegistry().Get(providerKind)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported deployment provider")
		return
	}
	secret, hasSecret := deploymentSecret(req)
	if !hasSecret {
		writeError(w, http.StatusBadRequest, "credentials are required")
		return
	}
	defer deployment.ScrubBytes(secret)
	if jsonHasKey(req.Credentials, "webhook_secret") || jsonHasKey(req.Config, "webhook_secret") {
		writeError(w, http.StatusBadRequest, "webhook secret must be provided separately")
		return
	}
	webhookSecret, hasWebhookSecret := deploymentWebhookSecretInput(req)
	if hasWebhookSecret && bytes.Equal(secret, webhookSecret) {
		writeError(w, http.StatusBadRequest, "webhook secret must differ from provider credentials")
		return
	}
	if h.DeploymentSecretBox == nil && h.DeploymentKeyRing == nil {
		writeError(w, http.StatusServiceUnavailable, "deployment credentials are not configured")
		return
	}
	creds := deployment.ConnectionCredentials{Config: req.Config, RawSecret: secret}
	defer deployment.ScrubBytes(webhookSecret)
	if _, err := p.ValidateConnection(r.Context(), creds); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "provider credentials could not be validated")
		return
	}
	sealed, err := h.sealDeploymentSecret(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store deployment credentials")
		return
	}
	secretRef := base64.StdEncoding.EncodeToString(sealed)
	var webhookSecretRef *string
	if hasWebhookSecret {
		sealedWebhook, err := h.sealDeploymentSecret(webhookSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store deployment webhook secret")
			return
		}
		ref := base64.StdEncoding.EncodeToString(sealedWebhook)
		webhookSecretRef = &ref
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	config := req.Config
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	c, err := h.deploymentRepository().CreateProviderConnection(r.Context(), deployment.CreateProviderConnectionInput{ID: uuid.New(), WorkspaceID: uuid.MustParse(workspaceID), Provider: providerKind, Name: name, SecretRef: &secretRef, WebhookSecretRef: webhookSecretRef, Config: config, Enabled: enabled})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create deployment connection")
		return
	}
	writeJSON(w, http.StatusCreated, h.deploymentConnectionResponse(c))
}

func (h *Handler) GetDeploymentConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, chi.URLParam(r, "connectionId"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.deploymentConnectionResponse(c))
}

func (h *Handler) ValidateDeploymentConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, chi.URLParam(r, "connectionId"))
	if !ok {
		return
	}
	validationStarted := time.Now()
	validationResult := "success"
	defer func() {
		if validationResult != "success" {
			h.Metrics.RecordDeploymentConnectionValidation(c.Provider, validationResult, time.Since(validationStarted))
			return
		}
		h.Metrics.RecordDeploymentConnectionValidation(c.Provider, "success", time.Since(validationStarted))
	}()
	p, err := h.deploymentRegistry().Get(c.Provider)
	if err != nil {
		validationResult = "error"
		writeError(w, http.StatusBadRequest, "unsupported deployment provider")
		return
	}
	creds, err := h.deploymentCredentials(r.Context(), c)
	if err != nil {
		validationResult = "error"
		writeError(w, http.StatusServiceUnavailable, "deployment credentials are unavailable")
		return
	}
	defer deployment.ScrubBytes(creds.RawSecret)
	info, err := p.ValidateConnection(r.Context(), creds)
	if err != nil {
		validationResult = "error"
		writeError(w, http.StatusUnprocessableEntity, "provider credentials could not be validated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": h.deploymentConnectionResponse(c), "account_id": info.AccountID, "scope_id": info.ScopeID, "scope_name": info.ScopeName, "display_name": info.DisplayName})
}

func (h *Handler) DeleteDeploymentConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	if _, ok := h.loadDeploymentConnection(w, r, workspaceID, chi.URLParam(r, "connectionId")); !ok {
		return
	}
	if err := h.deploymentRepository().DeleteProviderConnection(r.Context(), workspaceID, chi.URLParam(r, "connectionId")); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete deployment connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListDeploymentProviderTargets(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	connectionID := chi.URLParam(r, "connectionId")
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, connectionID)
	if !ok {
		return
	}
	p, err := h.deploymentRegistry().Get(c.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported deployment provider")
		return
	}
	if !p.Capabilities().TargetDiscovery {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support target discovery")
		return
	}
	creds, err := h.deploymentCredentials(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment credentials are unavailable")
		return
	}
	defer deployment.ScrubBytes(creds.RawSecret)
	targets, err := p.ListTargets(r.Context(), c, creds)
	if err != nil {
		httpStatus := 0
		safeMessage := ""
		var providerErr *deployment.ProviderAPIError
		if errors.As(err, &providerErr) {
			httpStatus = providerErr.StatusCode
			safeMessage = deployment.RedactEvidenceText(providerErr.Message)
		}
		if safeMessage == "" {
			safeMessage = deployment.RedactEvidenceText(err.Error())
		}
		slog.ErrorContext(r.Context(), "deployment provider target discovery failed",
			"provider", c.Provider,
			"operation", "list_targets",
			"http_status", httpStatus,
			"provider_error_message", safeMessage,
		)
		if safeMessage != "" {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("%s target discovery failed: %s", c.Provider, safeMessage))
			return
		}
		writeError(w, http.StatusBadGateway, "provider target discovery failed")
		return
	}
	// Keep the generic external_id while also exposing the conventional id
	// expected by existing provider-project clients.
	out := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		out = append(out, map[string]any{
			"id": target.ExternalID, "external_id": target.ExternalID, "name": target.Name,
			"environment": target.Environment, "default_branch": target.DefaultBranch,
			"web_url": target.WebURL, "metadata": deploymentConfig(target.Metadata),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) CreateDeploymentProviderTarget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	connectionID := chi.URLParam(r, "connectionId")
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, connectionID)
	if !ok {
		return
	}
	p, err := h.deploymentRegistry().Get(c.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported deployment provider")
		return
	}
	if !p.Capabilities().TargetCreate {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support target creation")
		return
	}
	deleter, ok := p.(deployment.TargetDeleter)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "provider target creation cannot be compensated")
		return
	}
	var input deploymentTargetRequest
	if !decodeDeploymentJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Environment) == "" {
		writeError(w, http.StatusBadRequest, "name and environment are required")
		return
	}
	if !h.ensureProject(w, r, workspaceID, input.ProjectID) {
		return
	}
	config, repository, branch, err := targetSourcePolicy(input, nil, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	creds, err := h.deploymentCredentials(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment credentials are unavailable")
		return
	}
	defer deployment.ScrubBytes(creds.RawSecret)
	remote, err := p.CreateTarget(r.Context(), c, creds, deployment.CreateTargetRequest{
		Name: input.Name, Environment: input.Environment, Repository: repository,
		Branch:    branch,
		Framework: input.Framework, RootDirectory: input.RootDirectory, BuildCommand: input.BuildCommand,
		Confirmed: input.Confirmed || input.Confirm, Config: config,
	})
	if err != nil {
		if errors.Is(err, deployment.ErrTargetCreationConfirmationRequired) {
			writeError(w, http.StatusBadRequest, "target creation requires confirmation")
			return
		}
		httpStatus := 0
		safeMessage := ""
		var providerErr *deployment.ProviderAPIError
		if errors.As(err, &providerErr) {
			httpStatus = providerErr.StatusCode
			safeMessage = deployment.RedactEvidenceText(providerErr.Message)
		}
		slog.ErrorContext(r.Context(), "deployment provider target creation failed",
			"provider", c.Provider,
			"operation", "create_target",
			"http_status", httpStatus,
			"provider_error_message", safeMessage,
		)
		if safeMessage != "" {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("%s target creation failed: %s", c.Provider, safeMessage))
			return
		}
		writeError(w, http.StatusBadGateway, "provider target creation failed")
		return
	}
	config, _, _, err = targetSourcePolicy(input, remote.Metadata, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(remote.ExternalID) == "" {
		writeError(w, http.StatusBadGateway, "provider target creation returned no external id")
		return
	}
	target, err := h.deploymentRepository().CreateTarget(r.Context(), deployment.CreateTargetInput{ID: uuid.New(), WorkspaceID: uuid.MustParse(workspaceID), ConnectionID: c.ID, Name: input.Name, Environment: input.Environment, ExternalID: &remote.ExternalID, Config: config})
	if err != nil {
		if compensationErr := deleter.DeleteTarget(context.WithoutCancel(r.Context()), c, creds, remote.ExternalID); compensationErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist target and compensate remote project")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create deployment target")
		return
	}
	writeJSON(w, http.StatusCreated, h.deploymentTargetResponse(target))
}

func (h *Handler) ListDeploymentTargets(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	targets, err := h.deploymentRepository().ListTargets(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deployment targets")
		return
	}
	projectID := r.URL.Query().Get("project_id")
	out := make([]deploymentTargetResponse, 0, len(targets))
	for _, target := range targets {
		response := h.deploymentTargetResponse(target)
		if projectID == "" || response.ProjectID == projectID {
			out = append(out, response)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	target, ok := h.loadDeploymentTarget(w, r, workspaceID, chi.URLParam(r, "targetId"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.deploymentTargetResponse(target))
}

func (h *Handler) CreateDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	var req deploymentTargetRequest
	if !decodeDeploymentJSON(w, r, &req) {
		return
	}
	if req.ConnectionID == "" || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Environment) == "" {
		writeError(w, http.StatusBadRequest, "connection_id, name, and environment are required")
		return
	}
	if !h.ensureProject(w, r, workspaceID, req.ProjectID) {
		return
	}
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, req.ConnectionID)
	if !ok {
		return
	}
	externalID := targetExternalID(req.ExternalID)
	if externalID == "" {
		writeError(w, http.StatusBadRequest, "external_id is required for an attached deployment target")
		return
	}
	creds, err := h.deploymentCredentials(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment credentials are unavailable")
		return
	}
	defer deployment.ScrubBytes(creds.RawSecret)
	provider, err := h.deploymentRegistry().Get(c.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported deployment provider")
		return
	}
	remote, err := provider.GetTarget(r.Context(), c, creds, externalID)
	if err != nil || strings.TrimSpace(remote.ExternalID) == "" || !strings.EqualFold(strings.TrimSpace(remote.ExternalID), externalID) {
		writeError(w, http.StatusUnprocessableEntity, "external deployment target could not be verified")
		return
	}
	config, _, _, err := targetSourcePolicy(req, remote.Metadata, remote.DefaultBranch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if provider, err := h.deploymentRegistry().Get(c.Provider); err == nil {
		if validator, ok := provider.(interface {
			ValidateTargetConfig(deployment.CreateTargetRequest) error
		}); ok {
			if err := validator.ValidateTargetConfig(deployment.CreateTargetRequest{Config: config}); err != nil {
				writeError(w, http.StatusBadRequest, "invalid deployment target configuration")
				return
			}
		}
	}
	if req.ProjectID != "" {
		var cfg map[string]any
		_ = json.Unmarshal(config, &cfg)
		if cfg == nil {
			cfg = map[string]any{}
		}
		cfg["project_id"] = req.ProjectID
		config = mustJSON(cfg)
	}
	verifiedExternalID := remote.ExternalID
	target, err := h.deploymentRepository().CreateTarget(r.Context(), deployment.CreateTargetInput{ID: uuid.New(), WorkspaceID: uuid.MustParse(workspaceID), ConnectionID: c.ID, Name: req.Name, Environment: req.Environment, ExternalID: &verifiedExternalID, Config: config})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create deployment target")
		return
	}
	writeJSON(w, http.StatusCreated, h.deploymentTargetResponse(target))
}

func (h *Handler) UpdateDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	target, ok := h.loadDeploymentTarget(w, r, workspaceID, chi.URLParam(r, "targetId"))
	if !ok {
		return
	}
	var req deploymentTargetPatch
	if !decodeDeploymentJSON(w, r, &req) {
		return
	}
	projectID := h.deploymentTargetResponse(target).ProjectID
	if req.ProjectID != nil {
		projectID = *req.ProjectID
	}
	if !h.ensureProject(w, r, workspaceID, projectID) {
		return
	}
	name, environment, config := target.Name, target.Environment, target.Config
	externalID := target.ExternalID
	if req.Name != nil {
		name = *req.Name
	}
	if req.Environment != nil {
		environment = *req.Environment
	}
	if req.Config != nil {
		config = *req.Config
	}
	if req.ExternalID != nil {
		externalID = req.ExternalID
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(environment) == "" {
		writeError(w, http.StatusBadRequest, "name and environment are required")
		return
	}
	if targetExternalID(externalID) == "" {
		writeError(w, http.StatusBadRequest, "external_id is required for a deployable target")
		return
	}
	config, _, _, err := targetSourcePolicy(deploymentTargetRequest{Config: config}, nil, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ProjectID != nil {
		var cfg map[string]any
		_ = json.Unmarshal(config, &cfg)
		if cfg == nil {
			cfg = map[string]any{}
		}
		cfg["project_id"] = *req.ProjectID
		config = mustJSON(cfg)
	}
	updated, err := h.deploymentRepository().UpdateTarget(r.Context(), deployment.UpdateTargetInput{ID: target.ID, WorkspaceID: target.WorkspaceID, Name: name, Environment: environment, ExternalID: externalID, Config: config})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update deployment target")
		return
	}
	writeJSON(w, http.StatusOK, h.deploymentTargetResponse(updated))
}

func (h *Handler) DeleteDeploymentTarget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	if _, ok := h.loadDeploymentTarget(w, r, workspaceID, chi.URLParam(r, "targetId")); !ok {
		return
	}
	if err := h.deploymentRepository().DeleteTarget(r.Context(), workspaceID, chi.URLParam(r, "targetId")); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete deployment target")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type deploymentOperationResponse struct {
	ID                  string                     `json:"id"`
	WorkspaceID         string                     `json:"workspace_id"`
	TargetID            string                     `json:"target_id"`
	Action              string                     `json:"action"`
	IdempotencyKey      string                     `json:"idempotency_key"`
	Status              deployment.OperationStatus `json:"status"`
	AttemptCount        int32                      `json:"attempt_count"`
	MaxAttempts         int32                      `json:"max_attempts"`
	ProviderOperationID *string                    `json:"provider_operation_id,omitempty"`
	Request             json.RawMessage            `json:"request"`
	Evidence            json.RawMessage            `json:"evidence"`
	LastError           *string                    `json:"last_error,omitempty"`
	CreatedAt           string                     `json:"created_at"`
	UpdatedAt           string                     `json:"updated_at"`
}

func (h *Handler) deploymentOperationResponse(o deployment.Operation) deploymentOperationResponse {
	return deploymentOperationResponse{
		ID: o.ID.String(), WorkspaceID: o.WorkspaceID.String(), TargetID: o.TargetID.String(), Action: o.Action,
		IdempotencyKey: o.IdempotencyKey, Status: o.Status, AttemptCount: o.AttemptCount, MaxAttempts: o.MaxAttempts,
		ProviderOperationID: o.ProviderOperationID, Request: deploymentConfig(o.Request), Evidence: deploymentConfig(o.Evidence),
		LastError: safeDeploymentError(o.LastError), CreatedAt: o.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"), UpdatedAt: o.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"),
	}
}

func safeDeploymentError(value *string) *string {
	if value == nil {
		return nil
	}
	// Provider errors are persisted after adapter redaction, but do not trust
	// historical rows at the API boundary.
	if slicesContainSensitive(strings.ToLower(*value)) {
		redacted := "provider operation failed"
		return &redacted
	}
	return value
}

func (h *Handler) deploymentProviderForTarget(w http.ResponseWriter, r *http.Request, workspaceID string, target deployment.Target) (deployment.ProviderConnection, deployment.Provider, bool) {
	connection, ok := h.loadDeploymentConnection(w, r, workspaceID, target.ConnectionID.String())
	if !ok {
		return deployment.ProviderConnection{}, nil, false
	}
	if !connection.Enabled {
		writeError(w, http.StatusConflict, "deployment connection is disabled")
		return deployment.ProviderConnection{}, nil, false
	}
	p, err := h.deploymentRegistry().Get(connection.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported deployment provider")
		return deployment.ProviderConnection{}, nil, false
	}
	return connection, p, true
}

func (h *Handler) ListDeploymentOperations(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	target, ok := h.loadDeploymentTarget(w, r, workspaceID, chi.URLParam(r, "targetId"))
	if !ok {
		return
	}
	rows, err := h.deploymentRepository().ListOperations(r.Context(), workspaceID, target.ID.String())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deployments")
		return
	}
	out := make([]deploymentOperationResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.deploymentOperationResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetDeploymentOperation(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	op, err := h.deploymentRepository().GetOperation(r.Context(), workspaceID, chi.URLParam(r, "operationId"))
	if errors.Is(err, deployment.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load deployment")
		return
	}
	writeJSON(w, http.StatusOK, h.deploymentOperationResponse(op))
}

func (h *Handler) GetDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	op, err := h.deploymentRepository().GetOperation(r.Context(), workspaceID, chi.URLParam(r, "operationId"))
	if errors.Is(err, deployment.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load deployment")
		return
	}
	if op.ProviderOperationID == nil || strings.TrimSpace(*op.ProviderOperationID) == "" {
		writeError(w, http.StatusConflict, "deployment has not been accepted by the provider")
		return
	}
	target, connection, provider, err := h.resolveDeploymentProvider(r.Context(), op)
	if err != nil {
		writeError(w, http.StatusBadGateway, "deployment provider is unavailable")
		return
	}
	logProvider, ok := provider.(deployment.LogProvider)
	if !ok || !provider.Capabilities().Logs {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support deployment logs")
		return
	}
	creds, err := h.deploymentCredentials(r.Context(), connection)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment credentials are unavailable")
		return
	}
	defer deployment.ScrubBytes(creds.RawSecret)
	result, err := logProvider.GetLogs(r.Context(), deployment.LogRequest{
		WorkspaceID: op.WorkspaceID, OperationID: op.ID, ProviderOperationID: *op.ProviderOperationID,
		ExternalTarget: deploymentRemoteTarget(target), Credentials: creds, Limit: 500,
	})
	if err != nil {
		safeMessage := deployment.RedactEvidenceText(err.Error())
		slog.ErrorContext(r.Context(), "deployment provider log retrieval failed",
			"provider", provider.Kind(), "operation", "get_logs", "provider_error_message", safeMessage)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("%s log retrieval failed: %s", provider.Kind(), safeMessage))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) TriggerDeployment(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	target, ok := h.loadDeploymentTarget(w, r, workspaceID, chi.URLParam(r, "targetId"))
	if !ok {
		return
	}
	var req deploymentActionRequest
	if !decodeDeploymentJSON(w, r, &req) {
		return
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = "deploy"
	}
	if action != "deploy" {
		writeError(w, http.StatusBadRequest, "action must be deploy")
		return
	}
	environment := strings.TrimSpace(req.Environment)
	if environment == "" {
		environment = target.Environment
	}
	if environment == "production" {
		if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
			return
		}
		if !req.Confirmed && !req.Confirm && strings.TrimSpace(req.ApprovalReference) == "" {
			writeError(w, http.StatusBadRequest, "production deployment requires confirmation")
			return
		}
	}
	if targetExternalID(target.ExternalID) == "" {
		writeError(w, http.StatusBadRequest, "external_id is required for deployment")
		return
	}
	if err := validateDeploymentSourceRevision(target, req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, p, ok := h.deploymentProviderForTarget(w, r, workspaceID, target)
	if !ok {
		return
	}
	if !p.Capabilities().Trigger {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support deployment triggers")
		return
	}
	requestBody := mustJSON(map[string]any{"action": action, "repository": req.Repository, "git_ref": req.GitRef, "commit_sha": req.CommitSHA, "commit_message": req.CommitMessage, "environment": environment, "target_name": target.Name, "approval_reference": req.ApprovalReference})
	operationID := uuid.New()
	sourceRevision := strings.TrimSpace(req.CommitSHA)
	if sourceRevision == "" {
		sourceRevision = "ref:" + normalizeDeploymentRef(req.GitRef) + "@" + operationID.String()
	}
	operation, created, err := h.deploymentRepository().CreateOperation(r.Context(), deployment.CreateOperationInput{ID: operationID, WorkspaceID: target.WorkspaceID, TargetID: target.ID, Action: action, IdempotencyKey: r.Header.Get("Idempotency-Key"), SourceRevision: sourceRevision, Environment: environment, OperationKind: action, ApprovalReference: req.ApprovalReference, MaxAttempts: 3, Request: requestBody})
	if err != nil {
		writeDeploymentRepositoryError(w, err)
		return
	}
	if created {
		h.Metrics.RecordDeploymentOperationState(p.Kind(), string(operation.Status))
	}
	if created && h.DeploymentDispatcher != nil {
		// The operation is durable before this goroutine can call the provider.
		go h.dispatchDeploymentOperation(operation)
	}
	writeJSON(w, http.StatusAccepted, h.deploymentOperationResponse(operation))
}

func (h *Handler) dispatchDeploymentOperation(operation deployment.Operation) {
	provider, request, err := h.resolveDeploymentTrigger(context.Background(), operation)
	if err != nil {
		// The lifecycle reconciler persists the safe retry/final decision on its
		// next tick. Keeping this path side-effect free avoids a second failure
		// writer racing the durable operation claim.
		return
	}
	updated, _, dispatchErr := h.DeploymentDispatcher.DispatchExisting(context.Background(), operation, request, provider)
	result := "success"
	if dispatchErr != nil || updated.LastError != nil {
		result = "error"
	}
	h.Metrics.RecordDeploymentTrigger(provider.Kind(), result)
	h.Metrics.RecordDeploymentOperationState(provider.Kind(), string(updated.Status))
}

func (h *Handler) resolveDeploymentProvider(ctx context.Context, operation deployment.Operation) (deployment.Target, deployment.ProviderConnection, deployment.Provider, error) {
	target, err := h.deploymentRepository().GetTarget(ctx, operation.WorkspaceID.String(), operation.TargetID.String())
	if err != nil {
		return deployment.Target{}, deployment.ProviderConnection{}, nil, err
	}
	connection, err := h.deploymentRepository().GetProviderConnection(ctx, operation.WorkspaceID.String(), target.ConnectionID.String())
	if err != nil {
		return deployment.Target{}, deployment.ProviderConnection{}, nil, err
	}
	if !connection.Enabled {
		return deployment.Target{}, deployment.ProviderConnection{}, nil, errors.New("deployment connection is disabled")
	}
	provider, err := h.deploymentRegistry().Get(connection.Provider)
	if err != nil {
		return deployment.Target{}, deployment.ProviderConnection{}, nil, err
	}
	return target, connection, provider, nil
}

func deploymentRemoteTarget(target deployment.Target) deployment.RemoteTarget {
	var config map[string]any
	_ = json.Unmarshal(target.Config, &config)
	externalID := ""
	if target.ExternalID != nil {
		externalID = strings.TrimSpace(*target.ExternalID)
	}
	if externalID == "" {
		if value, ok := config["project_id"].(string); ok {
			externalID = strings.TrimSpace(value)
		}
	}
	defaultBranch, _ := config["default_branch"].(string)
	return deployment.RemoteTarget{ExternalID: externalID, Name: target.Name, Environment: target.Environment, DefaultBranch: strings.TrimSpace(defaultBranch), Metadata: append(json.RawMessage(nil), target.Config...)}
}

func (h *Handler) resolveDeploymentTrigger(ctx context.Context, operation deployment.Operation) (deployment.Provider, deployment.DeploymentRequest, error) {
	target, connection, provider, err := h.resolveDeploymentProvider(ctx, operation)
	if err != nil {
		return nil, deployment.DeploymentRequest{}, err
	}
	if targetExternalID(target.ExternalID) == "" {
		return nil, deployment.DeploymentRequest{}, errors.New("external_id is required for deployment")
	}
	if _, _, _, err := targetSourcePolicy(deploymentTargetRequest{Config: target.Config}, nil, ""); err != nil {
		return nil, deployment.DeploymentRequest{}, err
	}
	creds, err := h.deploymentCredentials(ctx, connection)
	if err != nil {
		return nil, deployment.DeploymentRequest{}, err
	}
	var saved deploymentActionRequest
	if err := json.Unmarshal(operation.Request, &saved); err != nil {
		deployment.ScrubBytes(creds.RawSecret)
		return nil, deployment.DeploymentRequest{}, errors.New("stored deployment request is invalid")
	}
	return provider, deployment.DeploymentRequest{
		WorkspaceID: operation.WorkspaceID, OperationID: operation.ID, TargetID: operation.TargetID,
		ExternalTarget: deploymentRemoteTarget(target), IdempotencyKey: operation.IdempotencyKey,
		Environment: operation.Environment, CommitSHA: saved.CommitSHA, Branch: saved.GitRef,
		CommitMessage: saved.CommitMessage, Credentials: creds, Config: append(json.RawMessage(nil), target.Config...),
	}, nil
}

func (h *Handler) resolveDeploymentLookup(ctx context.Context, operation deployment.Operation) (deployment.Provider, deployment.OperationLookup, error) {
	target, connection, provider, err := h.resolveDeploymentProvider(ctx, operation)
	if err != nil {
		return nil, deployment.OperationLookup{}, err
	}
	creds, err := h.deploymentCredentials(ctx, connection)
	if err != nil {
		return nil, deployment.OperationLookup{}, err
	}
	return provider, deployment.OperationLookup{
		WorkspaceID: operation.WorkspaceID, OperationID: operation.ID,
		ProviderOperationID: strings.TrimSpace(*operation.ProviderOperationID),
		ExternalTarget:      deploymentRemoteTarget(target), Credentials: creds,
	}, nil
}

// ReconcileDeployments is the server-lifecycle entry point for the generic
// deployment worker. The DB-backed scheduler owns cadence and restart wakeup.
func (h *Handler) ReconcileDeployments(ctx context.Context) (int, error) {
	lister, ok := h.deploymentRepository().(deployment.OperationLister)
	if !ok {
		return 0, errors.New("deployment repository does not support reconciliation")
	}
	dispatcher := h.DeploymentDispatcher
	if dispatcher == nil {
		dispatcher = &deployment.AsyncDispatcher{Repository: h.deploymentRepository(), Workers: 4, Timeout: 2 * time.Minute}
	}
	reconciler := &deployment.Reconciler{
		Repository: h.deploymentRepository(), Lister: lister, Resolve: h.resolveDeploymentLookup,
		Trigger: h.resolveDeploymentTrigger, Dispatcher: dispatcher,
		Interval: 10 * time.Second, Backoff: 5 * time.Second, Deadline: 30 * time.Minute,
		OnLongRunning: func(deployment.Operation) {
			h.Metrics.RecordDeploymentLongRunning("all", "deadline")
		},
	}
	processed, err := reconciler.Reconcile(ctx, "")
	result := "success"
	if err != nil {
		result = "error"
	}
	h.Metrics.RecordDeploymentReconciliation("all", result)
	return processed, err
}

func writeDeploymentRepositoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deployment.ErrNotFound):
		writeError(w, http.StatusNotFound, "deployment resource not found")
	case errors.Is(err, deployment.ErrIdempotencyKeyRequired), errors.Is(err, deployment.ErrEventDedupeKeyRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, deployment.ErrRevisionConflict):
		writeError(w, http.StatusConflict, "deployment was modified; retry")
	default:
		writeError(w, http.StatusInternalServerError, "deployment operation failed")
	}
}

func (h *Handler) CancelDeployment(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, false)
	if !ok {
		return
	}
	op, err := h.deploymentRepository().GetOperation(r.Context(), workspaceID, chi.URLParam(r, "operationId"))
	if errors.Is(err, deployment.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load deployment")
		return
	}
	target, ok := h.loadDeploymentTarget(w, r, workspaceID, op.TargetID.String())
	if !ok {
		return
	}
	if target.Environment == "production" {
		if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
			return
		}
	}
	if op.Status.IsTerminal() {
		writeJSON(w, http.StatusOK, h.deploymentOperationResponse(op))
		return
	}
	_, p, ok := h.deploymentProviderForTarget(w, r, workspaceID, target)
	if !ok {
		return
	}
	if !p.Capabilities().Cancel {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support deployment cancellation")
		return
	}
	op.Status = deployment.StatusCancelled
	updated, err := h.deploymentRepository().UpdateOperation(r.Context(), op, op.Revision)
	if err != nil {
		writeDeploymentRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.deploymentOperationResponse(updated))
}

func (h *Handler) PromoteDeployment(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	target, ok := h.loadDeploymentTarget(w, r, workspaceID, chi.URLParam(r, "targetId"))
	if !ok {
		return
	}
	connection, p, ok := h.deploymentProviderForTarget(w, r, workspaceID, target)
	if !ok {
		return
	}
	if !p.Capabilities().Promote {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support promotion")
		return
	}
	if _, ok := p.(deployment.PromotionProvider); !ok {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support promotion")
		return
	}
	var req deploymentPromotionRequest
	if !decodeDeploymentJSON(w, r, &req) {
		return
	}
	candidate, err := h.deploymentRepository().GetOperation(r.Context(), workspaceID, req.CandidateOperationID)
	if err != nil || candidate.TargetID != target.ID || candidate.ProviderOperationID == nil {
		writeError(w, http.StatusBadRequest, "candidate deployment is invalid")
		return
	}
	if strings.TrimSpace(req.ApprovalReference) == "" {
		writeError(w, http.StatusBadRequest, "approval_reference is required")
		return
	}
	operation, _, err := h.deploymentRepository().CreateOperation(r.Context(), deployment.CreateOperationInput{ID: uuid.New(), WorkspaceID: target.WorkspaceID, TargetID: target.ID, Action: "promote", IdempotencyKey: r.Header.Get("Idempotency-Key"), SourceRevision: candidate.SourceRevision, Environment: "production", OperationKind: "promote", ApprovalReference: req.ApprovalReference, Request: mustJSON(map[string]any{"candidate_operation_id": candidate.ID.String(), "environment": "production", "approval_reference": req.ApprovalReference})})
	_ = connection
	if err != nil {
		writeDeploymentRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, h.deploymentOperationResponse(operation))
}

func (h *Handler) RollbackDeployment(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	target, ok := h.loadDeploymentTarget(w, r, workspaceID, chi.URLParam(r, "targetId"))
	if !ok {
		return
	}
	_, p, ok := h.deploymentProviderForTarget(w, r, workspaceID, target)
	if !ok {
		return
	}
	if !p.Capabilities().Rollback {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support rollback")
		return
	}
	if _, ok := p.(deployment.RollbackProvider); !ok {
		writeError(w, http.StatusUnprocessableEntity, "provider does not support rollback")
		return
	}
	var req deploymentRollbackRequest
	if !decodeDeploymentJSON(w, r, &req) {
		return
	}
	candidate, err := h.deploymentRepository().GetOperation(r.Context(), workspaceID, req.TargetOperationID)
	if err != nil || candidate.TargetID != target.ID || candidate.ProviderOperationID == nil {
		writeError(w, http.StatusBadRequest, "rollback deployment is invalid")
		return
	}
	if strings.TrimSpace(req.ApprovalReference) == "" {
		writeError(w, http.StatusBadRequest, "approval_reference is required")
		return
	}
	operation, _, err := h.deploymentRepository().CreateOperation(r.Context(), deployment.CreateOperationInput{ID: uuid.New(), WorkspaceID: target.WorkspaceID, TargetID: target.ID, Action: "rollback", IdempotencyKey: r.Header.Get("Idempotency-Key"), SourceRevision: candidate.SourceRevision, Environment: "production", OperationKind: "rollback", ApprovalReference: req.ApprovalReference, Request: mustJSON(map[string]any{"target_operation_id": candidate.ID.String(), "environment": "production", "approval_reference": req.ApprovalReference})})
	if err != nil {
		writeDeploymentRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, h.deploymentOperationResponse(operation))
}

// HandleDeploymentWebhook processes inbound deployment provider webhooks (e.g. Vercel).
// It verifies provider-specific cryptographic signatures (e.g. HMAC-SHA1 for Vercel)
// using constant-time comparison before mutating any database state.
// Invalid or missing signatures return 401 Unauthorized or 400 Bad Request immediately.
func (h *Handler) HandleDeploymentWebhook(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "connectionId")
	if _, ok := parseUUIDOrBadRequest(w, connectionID, "connection id"); !ok {
		return
	}

	workspaceID := chi.URLParam(r, "workspaceId")

	// Capped body read (1 MiB DoS cap)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id required for deployment webhook")
		return
	}

	conn, err := h.deploymentRepository().GetProviderConnection(r.Context(), workspaceID, connectionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "deployment connection not found")
		return
	}

	if !conn.Enabled {
		writeError(w, http.StatusForbidden, "deployment connection is disabled")
		return
	}

	webhookSecret, err := h.deploymentWebhookSecret(conn)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deployment webhook secret is not configured")
		return
	}
	defer deployment.ScrubBytes(webhookSecret)

	p, err := h.deploymentRegistry().Get(conn.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported deployment provider")
		return
	}
	if !p.Capabilities().Webhook {
		writeError(w, http.StatusBadRequest, "provider does not support webhooks")
		return
	}

	// Verify signature strictly before ANY database state mutation
	_, err = p.ParseWebhook(r.Context(), deployment.WebhookRequest{
		Headers:       r.Header,
		RawBody:       body,
		SigningSecret: webhookSecret,
	})
	if err != nil {
		h.Metrics.RecordDeploymentWebhook(conn.Provider, "verification_failed")
		if errors.Is(err, deployment.ErrWebhookSignatureMissing) || errors.Is(err, deployment.ErrWebhookTimestampMalformed) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, deployment.ErrWebhookSignatureInvalid) || errors.Is(err, deployment.ErrWebhookTimestampExpired) {
			writeError(w, http.StatusUnauthorized, "invalid webhook signature")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.Metrics.RecordDeploymentWebhook(conn.Provider, "verified")

	gateway := &deployment.EventGateway{
		Repository: h.deploymentRepository(),
		Providers:  h.deploymentRegistry(),
		Find: func(ctx context.Context, wsID, providerName, provOpID string) (deployment.Operation, error) {
			return h.deploymentRepository().GetOperationByProviderOperation(ctx, wsID, provOpID)
		},
	}

	res, err := gateway.Handle(r.Context(), conn.WorkspaceID.String(), conn.Provider, webhookSecret, r.Header, body)
	if err != nil {
		if errors.Is(err, deployment.ErrNotFound) {
			h.Metrics.RecordDeploymentWebhook(conn.Provider, "unmatched")
			writeJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": "operation not found"})
			return
		}
		h.Metrics.RecordDeploymentWebhook(conn.Provider, "error")
		writeError(w, http.StatusInternalServerError, "failed to process deployment event")
		return
	}
	if res.Duplicate {
		h.Metrics.RecordDeploymentWebhook(conn.Provider, "duplicate")
	} else if res.Applied {
		h.Metrics.RecordDeploymentWebhook(conn.Provider, "applied")
		if res.Event.ObservedStatus != nil {
			h.Metrics.RecordDeploymentOperationState(conn.Provider, string(*res.Event.ObservedStatus))
		}
	} else {
		h.Metrics.RecordDeploymentWebhook(conn.Provider, "ignored")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"applied":   res.Applied,
		"duplicate": res.Duplicate,
		"event_id":  res.Event.ID.String(),
	})
}
