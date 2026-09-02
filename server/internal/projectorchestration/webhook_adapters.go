package projectorchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

// WebhookAdapterConfig configures backend-owned deployment/observation
// integrations. Credentials remain server-side and are never exposed to LLMs.
type WebhookAdapterConfig struct {
	DeploymentURL  string
	ObservationURL string
	BearerToken    string
	Timeout        time.Duration
}

type webhookAdapters struct {
	deploymentURL  string
	observationURL string
	bearerToken    string
	client         *http.Client
}

func NewWebhookDeploymentAdapter(cfg WebhookAdapterConfig) DeploymentAdapter {
	if strings.TrimSpace(cfg.DeploymentURL) == "" {
		return nil
	}
	return newWebhookAdapters(cfg)
}

func NewWebhookObservationAdapter(cfg WebhookAdapterConfig) ObservationAdapter {
	if strings.TrimSpace(cfg.ObservationURL) == "" {
		return nil
	}
	return newWebhookAdapters(cfg)
}

func newWebhookAdapters(cfg WebhookAdapterConfig) *webhookAdapters {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &webhookAdapters{
		deploymentURL:  strings.TrimSpace(cfg.DeploymentURL),
		observationURL: strings.TrimSpace(cfg.ObservationURL),
		bearerToken:    strings.TrimSpace(cfg.BearerToken),
		client:         &http.Client{Timeout: timeout},
	}
}

func (a *webhookAdapters) Deploy(ctx context.Context, request DeploymentRequest) (DeploymentResult, error) {
	if a == nil || a.deploymentURL == "" {
		return DeploymentResult{}, ErrAdapterNotConfigured
	}
	payload := map[string]any{
		"action":       "deploy",
		"workspace_id": util.UUIDToString(request.WorkspaceID),
		"project_id":   util.UUIDToString(request.ProjectID),
		"plan_id":      util.UUIDToString(request.PlanID),
		"environment":  request.Environment,
		"release_ref":  request.ReleaseRef,
		"policy":       request.Policy,
	}
	var result DeploymentResult
	if err := a.post(ctx, a.deploymentURL, payload, &result); err != nil {
		return DeploymentResult{}, err
	}
	if result.Provider == "" {
		result.Provider = "webhook"
	}
	switch result.Status {
	case "succeeded", "failed":
	default:
		return DeploymentResult{}, fmt.Errorf("deployment webhook returned unsupported terminal status %q", result.Status)
	}
	return result, nil
}

func (a *webhookAdapters) Rollback(ctx context.Context, request RollbackRequest) (DeploymentResult, error) {
	if a == nil || a.deploymentURL == "" {
		return DeploymentResult{}, ErrAdapterNotConfigured
	}
	payload := map[string]any{
		"action":        "rollback",
		"workspace_id":  util.UUIDToString(request.WorkspaceID),
		"project_id":    util.UUIDToString(request.ProjectID),
		"deployment_id": util.UUIDToString(request.DeploymentID),
		"reason":        request.Reason,
	}
	var result DeploymentResult
	if err := a.post(ctx, a.deploymentURL, payload, &result); err != nil {
		return DeploymentResult{}, err
	}
	if result.Provider == "" {
		result.Provider = "webhook"
	}
	switch result.Status {
	case "succeeded", "rolled_back", "failed":
	default:
		return DeploymentResult{}, fmt.Errorf("deployment webhook returned unsupported rollback status %q", result.Status)
	}
	return result, nil
}

func (a *webhookAdapters) Observe(ctx context.Context, request ObservationRequest) (ObservationResult, error) {
	if a == nil || a.observationURL == "" {
		return ObservationResult{}, ErrAdapterNotConfigured
	}
	payload := map[string]any{
		"action":         "observe",
		"workspace_id":   util.UUIDToString(request.WorkspaceID),
		"project_id":     util.UUIDToString(request.ProjectID),
		"deployment_id":  util.UUIDToString(request.DeploymentID),
		"window_seconds": request.WindowSeconds,
	}
	var result ObservationResult
	if err := a.post(ctx, a.observationURL, payload, &result); err != nil {
		return ObservationResult{}, err
	}
	return result, nil
}

func (a *webhookAdapters) post(ctx context.Context, endpoint string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if a.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("autonomous webhook returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode autonomous webhook response: %w", err)
	}
	return nil
}
