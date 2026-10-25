package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	core "github.com/multica-ai/multica/server/internal/deployment"
)

const defaultVercelAPI = "https://api.vercel.com"

var (
	ErrVercelConfirmationRequired = core.ErrTargetCreationConfirmationRequired
	ErrVercelProjectName          = errors.New("invalid vercel project name")
	ErrVercelTargetConfig         = errors.New("invalid vercel target configuration")
	ErrVercelScope                = errors.New("vercel team scope is not accessible")
	ErrVercelDeploymentRequest    = errors.New("invalid vercel deployment request")
	ErrVercelRepositoryIDMissing  = errors.New("Vercel repository ID bulunamadı; target’ı yeniden bağlayın")
	ErrVercelUnsupported          = errors.New("vercel operation is not supported")
)

// VercelProvider implements connection validation, target management, and the
// Vercel deployment lifecycle.
type VercelProvider struct {
	BaseURL        string
	HTTPClient     *http.Client
	ObserveRequest func(operation, result string, duration time.Duration)
}

// VercelClient is kept as the public client name for callers that use the
// adapter as a direct REST client.
type VercelClient = VercelProvider

func NewVercelProvider(baseURL string, clients ...*http.Client) *VercelProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultVercelAPI
	}
	client := http.DefaultClient
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &VercelProvider{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: client}
}

func NewVercelClient(baseURL string, clients ...*http.Client) *VercelClient {
	return NewVercelProvider(baseURL, clients...)
}

func (p *VercelProvider) Kind() string { return "vercel" }

func (p *VercelProvider) Capabilities() core.CapabilitySet {
	return core.CapabilitySet{
		Trigger: true, Status: true, Webhook: true, Evidence: true, Logs: true,
		TargetDiscovery: true, TargetCreate: true,
	}
}

func (p *VercelProvider) SetRequestObserver(observer func(operation, result string, duration time.Duration)) {
	p.ObserveRequest = observer
}

type vercelCredentials struct {
	Token string
}

func parseVercelCredentials(creds core.ConnectionCredentials) (vercelCredentials, error) {
	value := strings.TrimSpace(string(creds.RawSecret))
	if value == "" {
		return vercelCredentials{}, errors.New("vercel credentials are required")
	}
	if strings.HasPrefix(value, "{") {
		var obj struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal([]byte(value), &obj); err != nil {
			return vercelCredentials{}, errors.New("vercel credentials are invalid")
		}
		value = obj.AccessToken
		if value == "" {
			value = obj.Token
		}
	} else if strings.HasPrefix(value, "\"") {
		var token string
		if json.Unmarshal([]byte(value), &token) == nil {
			value = token
		}
	}
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
		return vercelCredentials{}, errors.New("vercel credentials are invalid")
	}
	return vercelCredentials{Token: strings.TrimSpace(value)}, nil
}

type vercelScope struct {
	TeamID   string
	TeamSlug string
}

func connectionScope(conn core.ProviderConnection) vercelScope {
	var cfg map[string]any
	if json.Unmarshal(conn.Config, &cfg) != nil {
		return vercelScope{}
	}
	stringValue := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := cfg[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	return vercelScope{
		TeamID:   stringValue("team_id", "teamId", "scope_id"),
		TeamSlug: stringValue("team_slug", "teamSlug", "scope_slug"),
	}
}

type vercelUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}

type vercelTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type vercelTeams struct {
	Teams []vercelTeam `json:"teams"`
}

func (p *VercelProvider) ValidateConnection(ctx context.Context, creds core.ConnectionCredentials) (core.ConnectionInfo, error) {
	parsed, err := parseVercelCredentials(creds)
	if err != nil {
		return core.ConnectionInfo{}, err
	}
	scope := connectionScope(core.ProviderConnection{Config: creds.Config})
	if scope.TeamID != "" || scope.TeamSlug != "" {
		var response vercelTeams
		if err := p.doJSON(ctx, "validate_connection", http.MethodGet, "/v2/teams", parsed.Token, "", nil, &response); err != nil {
			return core.ConnectionInfo{}, err
		}
		for _, team := range response.Teams {
			if (scope.TeamID != "" && team.ID == scope.TeamID) || (scope.TeamSlug != "" && team.Slug == scope.TeamSlug) {
				return core.ConnectionInfo{AccountID: team.ID, ScopeID: team.ID, ScopeName: team.Name, DisplayName: team.Name}, nil
			}
		}
		return core.ConnectionInfo{}, fmt.Errorf("%w: requested team", ErrVercelScope)
	}
	var response struct {
		User vercelUser `json:"user"`
	}
	if err := p.doJSON(ctx, "validate_connection", http.MethodGet, "/v2/user", parsed.Token, "", nil, &response); err != nil {
		return core.ConnectionInfo{}, err
	}
	if response.User.ID == "" {
		return core.ConnectionInfo{}, errors.New("vercel user response is missing an id")
	}
	return core.ConnectionInfo{AccountID: response.User.ID, ScopeID: response.User.ID, ScopeName: response.User.Username, DisplayName: firstNonEmpty(response.User.Name, response.User.Email, response.User.Username)}, nil
}

func (p *VercelProvider) validateScopedConnection(ctx context.Context, conn core.ProviderConnection, creds vercelCredentials) (core.ConnectionInfo, vercelScope, error) {
	scope := connectionScope(conn)
	if scope.TeamID == "" && scope.TeamSlug == "" {
		info, err := p.ValidateConnection(ctx, core.ConnectionCredentials{Config: conn.Config, RawSecret: []byte(creds.Token)})
		return info, scope, err
	}
	var response vercelTeams
	if err := p.doJSON(ctx, "validate_connection", http.MethodGet, "/v2/teams", creds.Token, "", nil, &response); err != nil {
		return core.ConnectionInfo{}, scope, err
	}
	for _, team := range response.Teams {
		if (scope.TeamID != "" && team.ID == scope.TeamID) || (scope.TeamSlug != "" && team.Slug == scope.TeamSlug) {
			scope.TeamID, scope.TeamSlug = team.ID, team.Slug
			return core.ConnectionInfo{AccountID: team.ID, ScopeID: team.ID, ScopeName: team.Name, DisplayName: team.Name}, scope, nil
		}
	}
	return core.ConnectionInfo{}, scope, fmt.Errorf("%w: requested team", ErrVercelScope)
}

type vercelProject struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Framework string `json:"framework"`
	AccountID string `json:"accountId"`
	Link      *struct {
		Type             string          `json:"type"`
		Repo             string          `json:"repo"`
		RepoID           json.RawMessage `json:"repoId"`
		ProductionBranch string          `json:"productionBranch"`
	} `json:"link"`
	Targets map[string]struct {
		URL string `json:"url"`
	} `json:"targets"`
}

type vercelProjectPage struct {
	Projects   []vercelProject `json:"projects"`
	Pagination struct {
		Next    int  `json:"next"`
		HasMore bool `json:"hasMore"`
	} `json:"pagination"`
}

func (p *VercelProvider) ListTargets(ctx context.Context, conn core.ProviderConnection, creds core.ConnectionCredentials) ([]core.RemoteTarget, error) {
	parsed, err := parseVercelCredentials(creds)
	if err != nil {
		return nil, err
	}
	_, scope, err := p.validateScopedConnection(ctx, conn, parsed)
	if err != nil {
		return nil, err
	}
	var targets []core.RemoteTarget
	for cursor, pages := 0, 0; ; pages++ {
		if pages >= 100 { // ponytail: bounded pagination; Vercel's API must expose a finite cursor chain.
			return nil, errors.New("vercel project pagination exceeded limit")
		}
		query := ""
		if scope.TeamID != "" {
			query = "teamId=" + url.QueryEscape(scope.TeamID)
		}
		if cursor > 0 {
			if query != "" {
				query += "&"
			}
			query += fmt.Sprintf("from=%d", cursor)
		}
		var page vercelProjectPage
		if err := p.doJSON(ctx, "list_targets", http.MethodGet, "/v10/projects", parsed.Token, query, nil, &page); err != nil {
			return nil, err
		}
		for _, project := range page.Projects {
			targets = append(targets, projectTarget(project, scope.TeamID))
		}
		if !page.Pagination.HasMore || page.Pagination.Next <= 0 {
			return targets, nil
		}
		cursor = page.Pagination.Next
	}
}

func (p *VercelProvider) GetTarget(ctx context.Context, conn core.ProviderConnection, creds core.ConnectionCredentials, externalID string) (core.RemoteTarget, error) {
	parsed, err := parseVercelCredentials(creds)
	if err != nil {
		return core.RemoteTarget{}, err
	}
	_, scope, err := p.validateScopedConnection(ctx, conn, parsed)
	if err != nil {
		return core.RemoteTarget{}, err
	}
	if strings.TrimSpace(externalID) == "" {
		return core.RemoteTarget{}, errors.New("vercel project id is required")
	}
	var project vercelProject
	query := ""
	if scope.TeamID != "" {
		query = "teamId=" + url.QueryEscape(scope.TeamID)
	}
	if err := p.doJSON(ctx, "get_target", http.MethodGet, "/v9/projects/"+url.PathEscape(externalID), parsed.Token, query, nil, &project); err != nil {
		return core.RemoteTarget{}, err
	}
	return projectTarget(project, scope.TeamID), nil
}

var vercelProjectNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)

type vercelTargetConfig struct {
	Framework     string
	RootDirectory string
	BuildCommand  string
}

func parseVercelTargetConfig(req core.CreateTargetRequest) (vercelTargetConfig, error) {
	var raw map[string]any
	if len(req.Config) > 0 && string(req.Config) != "null" {
		if err := json.Unmarshal(req.Config, &raw); err != nil || raw == nil {
			return vercelTargetConfig{}, fmt.Errorf("%w: config must be an object", ErrVercelTargetConfig)
		}
	}
	stringValue := func(primary, alternate string) (string, error) {
		value, exists := raw[primary]
		if !exists {
			value = raw[alternate]
			exists = value != nil
		}
		if !exists || value == nil {
			return "", nil
		}
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%w: %s must be a string", ErrVercelTargetConfig, primary)
		}
		return strings.TrimSpace(text), nil
	}
	framework, err := stringValue("framework", "framework")
	if err != nil {
		return vercelTargetConfig{}, err
	}
	root, err := stringValue("rootDirectory", "root_directory")
	if err != nil {
		return vercelTargetConfig{}, err
	}
	build, err := stringValue("buildCommand", "build_command")
	if err != nil {
		return vercelTargetConfig{}, err
	}
	if req.Framework != "" {
		framework = strings.TrimSpace(req.Framework)
	}
	if req.RootDirectory != "" {
		root = strings.TrimSpace(req.RootDirectory)
	}
	if req.BuildCommand != "" {
		build = strings.TrimSpace(req.BuildCommand)
	}
	if framework != "" && (len(framework) > 64 || strings.ContainsAny(framework, "\r\n")) {
		return vercelTargetConfig{}, fmt.Errorf("%w: framework", ErrVercelTargetConfig)
	}
	if root != "" {
		clean := path.Clean(root)
		if len(root) > 256 || root != clean && root != "./" || path.IsAbs(root) || strings.HasPrefix(clean, "../") || strings.ContainsRune(root, '\x00') {
			return vercelTargetConfig{}, fmt.Errorf("%w: rootDirectory", ErrVercelTargetConfig)
		}
	}
	if len(build) > 512 || strings.ContainsAny(build, "\r\n") {
		return vercelTargetConfig{}, fmt.Errorf("%w: buildCommand", ErrVercelTargetConfig)
	}
	return vercelTargetConfig{Framework: framework, RootDirectory: root, BuildCommand: build}, nil
}

func (p *VercelProvider) ValidateTargetConfig(req core.CreateTargetRequest) error {
	_, err := parseVercelTargetConfig(req)
	return err
}

func isSupportedVercelFramework(value string) bool {
	switch strings.TrimSpace(value) {
	case "nextjs", "vite", "remix", "astro", "nuxtjs":
		return true
	default:
		return false
	}
}

func (p *VercelProvider) CreateTarget(ctx context.Context, conn core.ProviderConnection, creds core.ConnectionCredentials, req core.CreateTargetRequest) (core.RemoteTarget, error) {
	if !req.Confirmed && !req.Confirm {
		return core.RemoteTarget{}, ErrVercelConfirmationRequired
	}
	name := strings.TrimSpace(req.Name)
	if !vercelProjectNamePattern.MatchString(name) {
		return core.RemoteTarget{}, ErrVercelProjectName
	}
	config, err := parseVercelTargetConfig(req)
	if err != nil {
		return core.RemoteTarget{}, err
	}
	parsed, err := parseVercelCredentials(creds)
	if err != nil {
		return core.RemoteTarget{}, err
	}
	_, scope, err := p.validateScopedConnection(ctx, conn, parsed)
	if err != nil {
		return core.RemoteTarget{}, err
	}
	body := map[string]any{"name": name}
	if isSupportedVercelFramework(config.Framework) {
		body["framework"] = config.Framework
	}
	if config.RootDirectory != "" && config.RootDirectory != "." && config.RootDirectory != "./" {
		body["rootDirectory"] = config.RootDirectory
	}
	if config.BuildCommand != "" {
		body["buildCommand"] = config.BuildCommand
	}
	if repository := strings.TrimSuffix(strings.TrimSpace(req.Repository), ".git"); repository != "" {
		gitRepository := map[string]string{"type": repositoryType(repository), "repo": normalizeRepository(repository)}
		body["gitRepository"] = gitRepository
	}
	query := ""
	if scope.TeamID != "" {
		query = "teamId=" + url.QueryEscape(scope.TeamID)
	}
	var project vercelProject
	if err := p.doJSON(ctx, "create_target", http.MethodPost, "/v10/projects", parsed.Token, query, body, &project); err != nil {
		return core.RemoteTarget{}, err
	}
	return projectTarget(project, scope.TeamID), nil
}

func (p *VercelProvider) DeleteTarget(ctx context.Context, conn core.ProviderConnection, creds core.ConnectionCredentials, externalID string) error {
	parsed, err := parseVercelCredentials(creds)
	if err != nil {
		return err
	}
	_, scope, err := p.validateScopedConnection(ctx, conn, parsed)
	if err != nil {
		return err
	}
	if strings.TrimSpace(externalID) == "" {
		return errors.New("vercel project id is required")
	}
	query := ""
	if scope.TeamID != "" {
		query = "teamId=" + url.QueryEscape(scope.TeamID)
	}
	return p.doJSON(ctx, "delete_target", http.MethodDelete, "/v9/projects/"+url.PathEscape(externalID), parsed.Token, query, nil, nil)
}

func repositoryType(repository string) string {
	if parsed, err := url.Parse(repository); err == nil {
		switch parsed.Host {
		case "github.com":
			return "github"
		case "gitlab.com":
			return "gitlab"
		case "bitbucket.org":
			return "bitbucket"
		}
	}
	for _, item := range []struct{ prefix, kind string }{{"github.com/", "github"}, {"gitlab.com/", "gitlab"}, {"bitbucket.org/", "bitbucket"}} {
		if strings.HasPrefix(repository, item.prefix) {
			return item.kind
		}
	}
	return "github"
}

func normalizeRepository(repository string) string {
	if parsed, err := url.Parse(repository); err == nil && parsed.Host != "" {
		return strings.TrimPrefix(strings.TrimSuffix(parsed.Path, ".git"), "/")
	}
	return strings.TrimPrefix(repository, "github.com/")
}

func projectTarget(project vercelProject, teamID string) core.RemoteTarget {
	branch, repository := "", ""
	repositoryTypeValue := ""
	if project.Link != nil {
		branch, repository, repositoryTypeValue = project.Link.ProductionBranch, project.Link.Repo, project.Link.Type
	}
	webURL := ""
	if production, ok := project.Targets["production"]; ok {
		webURL = production.URL
	}
	if webURL == "" && project.Name != "" {
		webURL = "https://" + project.Name + ".vercel.app"
	}
	repoID := ""
	if project.Link != nil {
		repoID = normalizeVercelRepositoryID(project.Link.RepoID)
	}
	metadata, _ := json.Marshal(map[string]string{"team_id": teamID, "framework": project.Framework, "repository": repository, "repository_type": repositoryTypeValue, "repo_id": repoID})
	return core.RemoteTarget{ExternalID: project.ID, Name: project.Name, Environment: "production", DefaultBranch: branch, WebURL: webURL, Metadata: metadata}
}

func normalizeVercelRepositoryID(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var stringID string
	if json.Unmarshal(raw, &stringID) == nil {
		return strings.TrimSpace(stringID)
	}
	var numberID json.Number
	if json.Unmarshal(raw, &numberID) == nil {
		return numberID.String()
	}
	return ""
}

type vercelDeployment struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	InspectorURL string `json:"inspectorUrl"`
	ReadyState   string `json:"readyState"`
	Target       string `json:"target"`
	ProjectID    string `json:"projectId"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

type vercelGitSource struct {
	Type   string `json:"type"`
	Repo   string `json:"repo,omitempty"`
	RepoID string `json:"repoId,omitempty"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha,omitempty"`
}

func vercelTargetDeploymentConfig(target core.RemoteTarget, req core.DeploymentRequest) (repository, repositoryTypeValue, repoID, teamID string) {
	var metadata map[string]any
	_ = json.Unmarshal(target.Metadata, &metadata)
	var config map[string]any
	_ = json.Unmarshal(req.Config, &config)
	stringValue := func(key string, sources ...map[string]any) string {
		for _, source := range sources {
			if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	repository = stringValue("repository", metadata, config)
	if repository == "" {
		repository = stringValue("repo", metadata, config)
	}
	repositoryTypeValue = stringValue("repository_type", metadata, config)
	if repositoryTypeValue == "" {
		repositoryTypeValue = stringValue("type", metadata, config)
	}
	repoID = stringValue("repo_id", metadata, config)
	if repoID == "" {
		repoID = stringValue("repoId", metadata, config)
	}
	teamID = stringValue("team_id", metadata, config)
	return
}

func vercelDeploymentOperation(deployment vercelDeployment, fallbackID string) (core.RemoteOperation, error) {
	id := strings.TrimSpace(deployment.ID)
	if id == "" {
		id = strings.TrimSpace(fallbackID)
	}
	if id == "" {
		return core.RemoteOperation{}, errors.New("vercel deployment response is missing an id")
	}
	status, err := normalizeVercelStatus(deployment.ReadyState)
	if err != nil {
		return core.RemoteOperation{}, err
	}
	evidence := map[string]any{
		"ready_state": deployment.ReadyState,
		"target":      deployment.Target,
		"url":         deployment.URL,
		"inspect_url": deployment.InspectorURL,
		"project_id":  deployment.ProjectID,
	}
	evidenceJSON, _ := json.Marshal(evidence)
	return core.RemoteOperation{
		ProviderOperationID: id,
		ExternalStatus:      deployment.ReadyState,
		NormalizedStatus:    status,
		DeploymentURL:       deployment.URL,
		InspectURL:          deployment.InspectorURL,
		Evidence:            evidenceJSON,
		ErrorMessage:        core.RedactEvidenceText(deployment.ErrorMessage),
	}, nil
}

func (p *VercelProvider) TriggerDeployment(ctx context.Context, req core.DeploymentRequest) (core.RemoteOperation, error) {
	parsed, err := parseVercelCredentials(req.Credentials)
	if err != nil {
		return core.RemoteOperation{}, err
	}
	projectID := strings.TrimSpace(req.ExternalTarget.ExternalID)
	commitSHA := strings.TrimSpace(req.CommitSHA)
	ref := strings.TrimSpace(req.Branch)
	if ref == "" {
		ref = strings.TrimSpace(req.ExternalTarget.DefaultBranch)
	}
	if projectID == "" || ref == "" {
		return core.RemoteOperation{}, fmt.Errorf("%w: project and git ref are required", ErrVercelDeploymentRequest)
	}
	repository, repositoryTypeValue, repoID, teamID := vercelTargetDeploymentConfig(req.ExternalTarget, req)
	if repoID == "" {
		return core.RemoteOperation{}, ErrVercelRepositoryIDMissing
	}
	if repositoryTypeValue == "" {
		repositoryTypeValue = repositoryType(repository)
	}
	gitSource := vercelGitSource{Type: repositoryTypeValue, Ref: ref, SHA: commitSHA}
	gitSource.RepoID = repoID
	if repository != "" {
		gitSource.Repo = normalizeRepository(repository)
	}
	body := map[string]any{
		"name":      firstNonEmpty(strings.TrimSpace(req.ExternalTarget.Name), projectID),
		"project":   projectID,
		"gitSource": gitSource,
	}
	environment := strings.TrimSpace(req.Environment)
	if environment == "" {
		environment = strings.TrimSpace(req.ExternalTarget.Environment)
	}
	if strings.EqualFold(environment, "production") {
		body["target"] = "production"
	}
	query := "forceNew=1&skipAutoDetectionConfirmation=1"
	if teamID == "" {
		teamID = connectionScope(core.ProviderConnection{Config: req.Credentials.Config}).TeamID
	}
	if teamID != "" {
		query += "&teamId=" + url.QueryEscape(teamID)
	}
	var deployment vercelDeployment
	if err := p.doJSON(ctx, "trigger", http.MethodPost, "/v13/deployments", parsed.Token, query, body, &deployment); err != nil {
		return core.RemoteOperation{}, err
	}
	return vercelDeploymentOperation(deployment, "")
}

func (p *VercelProvider) GetOperation(ctx context.Context, req core.OperationLookup) (core.RemoteOperation, error) {
	parsed, err := parseVercelCredentials(req.Credentials)
	if err != nil {
		return core.RemoteOperation{}, err
	}
	deploymentID := strings.TrimSpace(req.ProviderOperationID)
	if deploymentID == "" {
		return core.RemoteOperation{}, fmt.Errorf("%w: provider operation id is required", ErrVercelDeploymentRequest)
	}
	_, _, _, teamID := vercelTargetDeploymentConfig(req.ExternalTarget, core.DeploymentRequest{Config: req.Credentials.Config})
	query := ""
	if teamID != "" {
		query = "teamId=" + url.QueryEscape(teamID)
	}
	var deployment vercelDeployment
	if err := p.doJSON(ctx, "get_operation", http.MethodGet, "/v13/deployments/"+url.PathEscape(deploymentID), parsed.Token, query, nil, &deployment); err != nil {
		return core.RemoteOperation{}, err
	}
	return vercelDeploymentOperation(deployment, deploymentID)
}

type vercelDeploymentEvent struct {
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Payload struct {
		Text string `json:"text"`
		Info struct {
			ReadyState string `json:"readyState"`
		} `json:"info"`
	} `json:"payload"`
}

func (p *VercelProvider) GetLogs(ctx context.Context, req core.LogRequest) (core.LogResult, error) {
	parsed, err := parseVercelCredentials(req.Credentials)
	if err != nil {
		return core.LogResult{}, err
	}
	deploymentID := strings.TrimSpace(req.ProviderOperationID)
	if deploymentID == "" {
		return core.LogResult{}, fmt.Errorf("%w: provider operation id is required", ErrVercelDeploymentRequest)
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	query := url.Values{"direction": {"forward"}, "builds": {"1"}, "limit": {fmt.Sprint(limit)}}
	_, _, _, teamID := vercelTargetDeploymentConfig(req.ExternalTarget, core.DeploymentRequest{Config: req.Credentials.Config})
	if teamID != "" {
		query.Set("teamId", teamID)
	}
	if req.Since != nil {
		query.Set("since", fmt.Sprint(req.Since.UnixMilli()))
	}
	var events []vercelDeploymentEvent
	if err := p.doJSON(ctx, "get_logs", http.MethodGet, "/v3/deployments/"+url.PathEscape(deploymentID)+"/events", parsed.Token, query.Encode(), nil, &events); err != nil {
		return core.LogResult{}, err
	}
	logs := make([]string, 0, len(events))
	for _, event := range events {
		line := strings.TrimSpace(event.Payload.Text)
		if line == "" && event.Payload.Info.ReadyState != "" {
			line = event.Type + ": " + event.Payload.Info.ReadyState
		}
		if line != "" {
			logs = append(logs, core.RedactEvidenceText(line))
		}
	}
	return core.LogResult{Logs: logs, Completed: true}, nil
}

func (p *VercelProvider) CancelOperation(context.Context, core.CancelRequest) (core.RemoteOperation, error) {
	return core.RemoteOperation{}, ErrVercelUnsupported
}

type vercelWebhookPayload struct {
	Deployment struct {
		ID           string `json:"id"`
		URL          string `json:"url"`
		ReadyState   string `json:"readyState"`
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	} `json:"deployment"`
	Links struct {
		Deployment string `json:"deployment"`
	} `json:"links"`
	Project struct {
		ID string `json:"id"`
	} `json:"project"`
	ProjectID string `json:"projectId"`
	Target    string `json:"target"`
}

type vercelWebhook struct {
	ID      string               `json:"id"`
	Type    string               `json:"type"`
	Payload vercelWebhookPayload `json:"payload"`
}

func vercelHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func normalizeVercelStatus(value string) (core.OperationStatus, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "QUEUED", "PENDING":
		return core.StatusQueued, nil
	case "INITIALIZING", "BUILDING":
		return core.StatusBuilding, nil
	case "DEPLOYING":
		return core.StatusDeploying, nil
	case "READY":
		return core.StatusReady, nil
	case "SUCCEEDED", "SUCCESS":
		return core.StatusSucceeded, nil
	case "ERROR", "FAILED":
		return core.StatusFailed, nil
	case "CANCELED", "CANCELLED":
		return core.StatusCancelled, nil
	default:
		return "", fmt.Errorf("unsupported vercel deployment status %q", value)
	}
}

func statusForVercelEvent(eventType, readyState string) (core.OperationStatus, error) {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "deployment.created", "deployment":
		return core.StatusQueued, nil
	case "deployment.building", "deployment.initializing", "building", "initializing":
		return core.StatusBuilding, nil
	case "deployment.ready", "deployment-ready", "ready":
		return core.StatusReady, nil
	case "deployment.succeeded", "succeeded":
		return core.StatusSucceeded, nil
	case "deployment.error", "deployment-error", "error", "failed":
		return core.StatusFailed, nil
	case "deployment.canceled", "deployment.cancelled", "deployment-canceled", "canceled", "cancelled":
		return core.StatusCancelled, nil
	default:
		return normalizeVercelStatus(readyState)
	}
}

func (p *VercelProvider) ParseWebhook(ctx context.Context, req core.WebhookRequest) (core.ProviderEvent, error) {
	if err := ctx.Err(); err != nil {
		return core.ProviderEvent{}, err
	}
	if err := core.VerifyVercelSignature(req.RawBody, vercelHeader(req.Headers, "x-vercel-signature"), string(req.SigningSecret)); err != nil {
		return core.ProviderEvent{}, err
	}
	var webhook vercelWebhook
	if err := json.Unmarshal(req.RawBody, &webhook); err != nil {
		return core.ProviderEvent{}, fmt.Errorf("decode vercel webhook: %w", err)
	}
	operationID := strings.TrimSpace(webhook.Payload.Deployment.ID)
	if operationID == "" {
		return core.ProviderEvent{}, errors.New("vercel webhook has no deployment id")
	}
	status, err := statusForVercelEvent(webhook.Type, webhook.Payload.Deployment.ReadyState)
	if err != nil {
		return core.ProviderEvent{}, err
	}
	eventID := strings.TrimSpace(webhook.ID)
	if eventID == "" {
		digest := sha256.Sum256(req.RawBody)
		eventID = fmt.Sprintf("body:%x", digest[:])
	}
	projectID := webhook.Payload.Project.ID
	if projectID == "" {
		projectID = webhook.Payload.ProjectID
	}
	evidence, _ := json.Marshal(map[string]string{
		"url":           webhook.Payload.Deployment.URL,
		"inspect_url":   webhook.Payload.Links.Deployment,
		"project_id":    projectID,
		"target":        webhook.Payload.Target,
		"ready_state":   webhook.Payload.Deployment.ReadyState,
		"error_code":    webhook.Payload.Deployment.ErrorCode,
		"error_message": core.RedactEvidenceText(webhook.Payload.Deployment.ErrorMessage),
	})
	return core.ProviderEvent{
		ProviderEventID:     eventID,
		ProviderEventType:   webhook.Type,
		ProviderOperationID: operationID,
		ObservedStatus:      status,
		Evidence:            evidence,
		RawPayload:          append(json.RawMessage(nil), req.RawBody...),
	}, nil
}

func (p *VercelProvider) doJSON(ctx context.Context, operation, method, endpoint, token, query string, body any, out any) (err error) {
	started := time.Now()
	result := "success"
	defer func() {
		if err != nil {
			if strings.Contains(err.Error(), "status 429") {
				result = "rate_limited"
			} else {
				result = "error"
			}
		}
		if p.ObserveRequest != nil {
			p.ObserveRequest(operation, result, time.Since(started))
		}
	}()
	encoded := []byte(nil)
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return errors.New("encode vercel request")
		}
	}
	u := strings.TrimRight(p.BaseURL, "/") + endpoint
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(encoded))
	if err != nil {
		return errors.New("create vercel request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(req)
	if err != nil {
		return &core.RetryableProviderError{Err: fmt.Errorf("vercel request failed: %w", err)}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&apiErr); err == nil && apiErr.Error.Message != "" {
			err := &core.ProviderAPIError{StatusCode: response.StatusCode, Message: core.RedactEvidenceText(apiErr.Error.Message)}
			if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError {
				return &core.RetryableProviderError{Err: err}
			}
			return &core.FinalProviderError{Err: err}
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		err = &core.ProviderAPIError{StatusCode: response.StatusCode}
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError {
			return &core.RetryableProviderError{Err: err}
		}
		return &core.FinalProviderError{Err: err}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode vercel %s response: %w", operation, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
