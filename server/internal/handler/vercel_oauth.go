package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	deployment "github.com/multica-ai/multica/server/internal/deployment"
	providers "github.com/multica-ai/multica/server/internal/integrations/deployment"
)

type VercelOAuthState struct {
	WorkspaceID  uuid.UUID
	UserID       uuid.UUID
	CodeVerifier string
}

type VercelOAuthStateStore interface {
	Put(ctx context.Context, state string, value VercelOAuthState, expiresAt time.Time) error
	Consume(ctx context.Context, state string) (VercelOAuthState, error)
}

type postgresVercelOAuthStateStore struct {
	handler *Handler
}

func (s *postgresVercelOAuthStateStore) Put(ctx context.Context, state string, value VercelOAuthState, expiresAt time.Time) error {
	if s == nil || s.handler == nil || s.handler.DB == nil {
		return errors.New("oauth state storage is unavailable")
	}
	sealed, err := s.handler.sealDeploymentSecret([]byte(value.CodeVerifier))
	if err != nil {
		return errors.New("oauth state storage is unavailable")
	}
	_, err = s.handler.DB.Exec(ctx, `DELETE FROM deployment_vercel_oauth_state WHERE expires_at <= now()`)
	if err != nil {
		return errors.New("oauth state storage is unavailable")
	}
	_, err = s.handler.DB.Exec(ctx, `
		INSERT INTO deployment_vercel_oauth_state (state_hash, workspace_id, user_id, code_verifier_ciphertext, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		`, oauthStateHash(state), value.WorkspaceID, value.UserID, base64.StdEncoding.EncodeToString(sealed), expiresAt)
	if err != nil {
		return errors.New("oauth state storage is unavailable")
	}
	return nil
}

func (s *postgresVercelOAuthStateStore) Consume(ctx context.Context, state string) (VercelOAuthState, error) {
	if s == nil || s.handler == nil || s.handler.DB == nil {
		return VercelOAuthState{}, errors.New("oauth state storage is unavailable")
	}
	var workspaceID, userID pgtype.UUID
	var ciphertext string
	err := s.handler.DB.QueryRow(ctx, `
		DELETE FROM deployment_vercel_oauth_state
		WHERE state_hash = $1 AND expires_at > now()
		RETURNING workspace_id, user_id, code_verifier_ciphertext
		`, oauthStateHash(state)).Scan(&workspaceID, &userID, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return VercelOAuthState{}, errors.New("oauth state is invalid or expired")
	}
	if err != nil || !workspaceID.Valid || !userID.Valid {
		return VercelOAuthState{}, errors.New("oauth state is invalid")
	}
	sealed, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return VercelOAuthState{}, errors.New("oauth state is invalid")
	}
	verifier, err := s.handler.openDeploymentSecret(sealed)
	if err != nil || len(verifier) == 0 {
		return VercelOAuthState{}, errors.New("oauth state is invalid")
	}
	return VercelOAuthState{WorkspaceID: uuid.UUID(workspaceID.Bytes), UserID: uuid.UUID(userID.Bytes), CodeVerifier: string(verifier)}, nil
}

func oauthStateHash(state string) string {
	digest := sha256.Sum256([]byte(state))
	return hex.EncodeToString(digest[:])
}

func (h *Handler) vercelOAuthClient() *providers.VercelOAuthClient {
	if h.VercelOAuthClient != nil {
		return h.VercelOAuthClient
	}
	return providers.NewVercelOAuthClient(h.cfg.VercelOAuthClientID, h.cfg.VercelOAuthClientSecret, h.cfg.VercelOAuthRedirectURL)
}

func (h *Handler) StartVercelOAuth(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	client := h.vercelOAuthClient()
	state, err := providers.NewVercelOAuthState()
	if err != nil || h.VercelOAuthStates == nil {
		writeError(w, http.StatusServiceUnavailable, "vercel cloud oauth is unavailable")
		return
	}
	verifier, challenge, err := providers.NewVercelPKCE()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "vercel cloud oauth is unavailable")
		return
	}
	workspaceUUID, err := uuid.Parse(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "workspace id is invalid")
		return
	}
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user is not authenticated")
		return
	}
	if err := h.VercelOAuthStates.Put(r.Context(), state, VercelOAuthState{WorkspaceID: workspaceUUID, UserID: userUUID, CodeVerifier: verifier}, time.Now().Add(10*time.Minute)); err != nil {
		writeError(w, http.StatusServiceUnavailable, "vercel cloud oauth is unavailable")
		return
	}
	redirectURL, err := client.AuthorizationURL(state, challenge)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "vercel cloud oauth is unavailable")
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *Handler) VercelOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" || h.VercelOAuthStates == nil {
		writeError(w, http.StatusBadRequest, "invalid oauth callback")
		return
	}
	pending, err := h.VercelOAuthStates.Consume(r.Context(), state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid oauth callback")
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("error")) != "" {
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	client := h.vercelOAuthClient()
	tokens, err := client.ExchangeCode(r.Context(), code, pending.CodeVerifier)
	if err != nil {
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	credentialEnvelope, err := providers.EncodeVercelOAuthCredentials(tokens)
	if err != nil {
		h.revokeVercelOAuthToken(r, client, tokens.AccessToken)
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	p, err := h.deploymentRegistry().Get("vercel")
	if err != nil {
		h.revokeVercelOAuthToken(r, client, tokens.AccessToken)
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	config := mustJSON(map[string]any{
		"auth_mode":        "oauth",
		"oauth_provider":   "vercel",
		"token_expires_at": tokens.ExpiresAt.UTC().Format(time.RFC3339),
	})
	creds := deployment.ConnectionCredentials{Config: config, RawSecret: credentialEnvelope}
	defer deployment.ScrubBytes(credentialEnvelope)
	info, err := p.ValidateConnection(r.Context(), creds)
	if err != nil {
		h.revokeVercelOAuthToken(r, client, tokens.AccessToken)
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	config = mustJSON(map[string]any{
		"auth_mode":           "oauth",
		"oauth_provider":      "vercel",
		"external_account_id": info.AccountID,
		"external_scope_id":   info.ScopeID,
		"external_scope_name": info.ScopeName,
		"token_expires_at":    tokens.ExpiresAt.UTC().Format(time.RFC3339),
	})
	sealed, err := h.sealDeploymentSecret(credentialEnvelope)
	if err != nil {
		h.revokeVercelOAuthToken(r, client, tokens.AccessToken)
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	secretRef := base64.StdEncoding.EncodeToString(sealed)
	_, err = h.deploymentRepository().CreateProviderConnection(r.Context(), deployment.CreateProviderConnectionInput{
		ID: uuid.New(), WorkspaceID: pending.WorkspaceID, Provider: "vercel", Name: "Vercel Cloud OAuth",
		SecretRef: &secretRef, Config: config, Enabled: true,
	})
	if err != nil {
		h.revokeVercelOAuthToken(r, client, tokens.AccessToken)
		h.redirectVercelOAuthResult(w, r, "error")
		return
	}
	h.redirectVercelOAuthResult(w, r, "connected")
}

func (h *Handler) RefreshVercelOAuthConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, chi.URLParam(r, "connectionId"))
	if !ok {
		return
	}
	tokens, ok := h.loadVercelOAuthTokens(w, r, c)
	if !ok {
		return
	}
	refreshed, err := h.vercelOAuthClient().Refresh(r.Context(), tokens.RefreshToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "vercel oauth refresh failed")
		return
	}
	if !h.persistVercelOAuthTokens(r, c, refreshed) {
		writeError(w, http.StatusInternalServerError, "failed to store vercel oauth credentials")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": h.deploymentConnectionResponse(c)})
}

func (h *Handler) RevokeVercelOAuthConnection(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, chi.URLParam(r, "connectionId"))
	if !ok {
		return
	}
	tokens, ok := h.loadVercelOAuthTokens(w, r, c)
	if !ok {
		return
	}
	revokeErr := h.vercelOAuthClient().Revoke(r.Context(), tokens.AccessToken)
	config := deploymentConfigMap(c.Config)
	config["auth_mode"] = "oauth"
	config["revoked_at"] = time.Now().UTC().Format(time.RFC3339)
	_, persistErr := h.deploymentRepository().UpdateProviderConnection(r.Context(), deployment.UpdateProviderConnectionInput{
		ID: c.ID, WorkspaceID: c.WorkspaceID, Name: c.Name, Config: mustJSON(config), Enabled: false,
		WebhookSecretRef: c.WebhookSecretRef,
	})
	if persistErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable vercel oauth connection")
		return
	}
	if revokeErr != nil {
		writeError(w, http.StatusBadGateway, "vercel oauth revocation failed; connection disabled locally")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) SetDeploymentWebhookSecret(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.deploymentWorkspace(w, r, true)
	if !ok {
		return
	}
	c, ok := h.loadDeploymentConnection(w, r, workspaceID, chi.URLParam(r, "connectionId"))
	if !ok {
		return
	}
	var req deploymentConnectionRequest
	if !decodeDeploymentJSON(w, r, &req) {
		return
	}
	secret, ok := deploymentWebhookSecretInput(req)
	if !ok {
		writeError(w, http.StatusBadRequest, "webhook secret is required")
		return
	}
	defer deployment.ScrubBytes(secret)
	creds, err := h.deploymentCredentials(r.Context(), c)
	if err == nil {
		sameAsCredential := bytesEqual(secret, creds.RawSecret)
		if c.Provider == "vercel" && deploymentAuthMode(c.Config) == "oauth" {
			if tokens, decodeErr := providers.DecodeVercelOAuthCredentials(creds.RawSecret); decodeErr == nil {
				sameAsCredential = sameAsCredential || bytesEqual(secret, []byte(tokens.AccessToken)) || bytesEqual(secret, []byte(tokens.RefreshToken))
			}
		}
		deployment.ScrubBytes(creds.RawSecret)
		if sameAsCredential {
			writeError(w, http.StatusBadRequest, "webhook secret must differ from provider credentials")
			return
		}
	} else {
		deployment.ScrubBytes(creds.RawSecret)
	}
	sealed, err := h.sealDeploymentSecret(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store deployment webhook secret")
		return
	}
	ref := base64.StdEncoding.EncodeToString(sealed)
	updated, err := h.deploymentRepository().UpdateProviderConnection(r.Context(), deployment.UpdateProviderConnectionInput{
		ID: c.ID, WorkspaceID: c.WorkspaceID, Name: c.Name, SecretRef: c.SecretRef,
		WebhookSecretRef: &ref, Config: c.Config, Enabled: c.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store deployment webhook secret")
		return
	}
	writeJSON(w, http.StatusOK, h.deploymentConnectionResponse(updated))
}

func (h *Handler) loadVercelOAuthTokens(w http.ResponseWriter, r *http.Request, c deployment.ProviderConnection) (providers.VercelOAuthTokenSet, bool) {
	if c.Provider != "vercel" || deploymentAuthMode(c.Config) != "oauth" {
		writeError(w, http.StatusBadRequest, "connection is not a Vercel Cloud OAuth connection")
		return providers.VercelOAuthTokenSet{}, false
	}
	creds, err := h.deploymentCredentials(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "vercel oauth credentials are unavailable")
		return providers.VercelOAuthTokenSet{}, false
	}
	defer deployment.ScrubBytes(creds.RawSecret)
	tokens, err := providers.DecodeVercelOAuthCredentials(creds.RawSecret)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "vercel oauth credentials are invalid")
		return providers.VercelOAuthTokenSet{}, false
	}
	return tokens, true
}

func (h *Handler) persistVercelOAuthTokens(r *http.Request, c deployment.ProviderConnection, tokens providers.VercelOAuthTokenSet) bool {
	envelope, err := providers.EncodeVercelOAuthCredentials(tokens)
	if err != nil {
		return false
	}
	defer deployment.ScrubBytes(envelope)
	sealed, err := h.sealDeploymentSecret(envelope)
	if err != nil {
		return false
	}
	ref := base64.StdEncoding.EncodeToString(sealed)
	config := deploymentConfigMap(c.Config)
	config["token_expires_at"] = tokens.ExpiresAt.UTC().Format(time.RFC3339)
	_, err = h.deploymentRepository().UpdateProviderConnection(r.Context(), deployment.UpdateProviderConnectionInput{
		ID: c.ID, WorkspaceID: c.WorkspaceID, Name: c.Name, SecretRef: &ref,
		WebhookSecretRef: c.WebhookSecretRef, Config: mustJSON(config), Enabled: c.Enabled,
	})
	return err == nil
}

func deploymentAuthMode(raw json.RawMessage) string {
	value, _ := deploymentConfigMap(raw)["auth_mode"].(string)
	return strings.TrimSpace(value)
}

func deploymentConfigMap(raw json.RawMessage) map[string]any {
	var config map[string]any
	if json.Unmarshal(raw, &config) != nil || config == nil {
		return map[string]any{}
	}
	return config
}

func (h *Handler) revokeVercelOAuthToken(r *http.Request, client *providers.VercelOAuthClient, token string) {
	_ = client.Revoke(r.Context(), token)
}

func (h *Handler) redirectVercelOAuthResult(w http.ResponseWriter, r *http.Request, result string) {
	if appURL := strings.TrimSpace(h.cfg.AppURL); appURL != "" {
		if parsed, err := url.Parse(appURL); err == nil && parsed.Scheme != "" && parsed.Host != "" && parsed.User == nil {
			query := parsed.Query()
			query.Set("deployment_vercel_oauth", result)
			parsed.RawQuery = query.Encode()
			http.Redirect(w, r, parsed.String(), http.StatusFound)
			return
		}
	}
	if result == "connected" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
		return
	}
	writeError(w, http.StatusBadGateway, "vercel oauth connection failed")
}

func bytesEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
