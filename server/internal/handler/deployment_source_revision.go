package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type deploymentSourceRevisionResponse struct {
	Repository    string `json:"repository"`
	GitRef        string `json:"git_ref"`
	CommitSHA     string `json:"commit_sha"`
	CommitMessage string `json:"commit_message,omitempty"`
}

type githubSourceRevision struct {
	Repository    string
	GitRef        string
	CommitSHA     string
	CommitMessage string
}

type githubSourceStatusError struct {
	status int
}

func (e githubSourceStatusError) Error() string {
	return fmt.Sprintf("github source API returned status %d", e.status)
}

func githubRepositoryParts(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "git@github.com:") {
		raw = "https://github.com/" + strings.TrimPrefix(raw, "git@github.com:")
	} else if !strings.Contains(raw, "://") {
		trimmed := strings.Trim(raw, "/")
		if strings.Count(trimmed, "/") == 1 {
			raw = "https://github.com/" + trimmed
		} else {
			raw = "https://" + raw
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}

func githubSourceRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	return strings.TrimPrefix(ref, "refs/tags/")
}

func fetchGitHubSourceRevision(
	ctx context.Context,
	client *http.Client,
	token, owner, repo, requestedRef string,
) (githubSourceRevision, error) {
	ref := githubSourceRef(requestedRef)
	base := strings.TrimRight(githubAPIBase, "/")
	if ref == "" {
		repoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s", base, url.PathEscape(owner), url.PathEscape(repo)), nil)
		if err != nil {
			return githubSourceRevision{}, err
		}
		if token != "" {
			setGitHubAPIHeaders(repoReq, token)
		}
		repoResp, err := client.Do(repoReq)
		if err != nil {
			return githubSourceRevision{}, err
		}
		defer repoResp.Body.Close()
		if repoResp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(repoResp.Body, githubAPIResponseLimit))
			return githubSourceRevision{}, githubSourceStatusError{status: repoResp.StatusCode}
		}
		var repoBody struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := json.NewDecoder(io.LimitReader(repoResp.Body, githubAPIResponseLimit)).Decode(&repoBody); err != nil {
			return githubSourceRevision{}, err
		}
		ref = strings.TrimSpace(repoBody.DefaultBranch)
		if ref == "" {
			return githubSourceRevision{}, errors.New("github repository has no default branch")
		}
	}

	commitReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s/commits/%s", base, url.PathEscape(owner), url.PathEscape(repo), escapeRefPath(ref)), nil)
	if err != nil {
		return githubSourceRevision{}, err
	}
	if token != "" {
		setGitHubAPIHeaders(commitReq, token)
	}
	commitResp, err := client.Do(commitReq)
	if err != nil {
		return githubSourceRevision{}, err
	}
	defer commitResp.Body.Close()
	if commitResp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(commitResp.Body, githubAPIResponseLimit))
		return githubSourceRevision{}, githubSourceStatusError{status: commitResp.StatusCode}
	}
	var commitBody struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(io.LimitReader(commitResp.Body, githubAPIResponseLimit)).Decode(&commitBody); err != nil {
		return githubSourceRevision{}, err
	}
	if strings.TrimSpace(commitBody.SHA) == "" {
		return githubSourceRevision{}, errors.New("github returned an empty commit SHA")
	}
	return githubSourceRevision{
		Repository:    owner + "/" + repo,
		GitRef:        ref,
		CommitSHA:     strings.TrimSpace(commitBody.SHA),
		CommitMessage: strings.TrimSpace(commitBody.Commit.Message),
	}, nil
}

func acquireGitHubInstallationToken(ctx context.Context, installationID int64, contentsRead bool) (*http.Client, string, error) {
	appJWT, err := signGitHubAppJWT(time.Now())
	if err != nil {
		return nil, "", err
	}
	if appJWT == "" {
		return nil, "", errors.New("github App JWT credentials unavailable")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	permissions := `{"permissions":{"metadata":"read"}}`
	if contentsRead {
		permissions = `{"permissions":{"contents":"read"}}`
	}
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", strings.TrimRight(githubAPIBase, "/"), installationID), strings.NewReader(permissions))
	if err != nil {
		return nil, "", err
	}
	setGitHubAPIHeaders(tokenReq, appJWT)
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		return nil, "", fmt.Errorf("create installation token: %w", err)
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(tokenResp.Body, githubAPIResponseLimit))
		return nil, "", fmt.Errorf("create installation token: github status %d", tokenResp.StatusCode)
	}
	var tokenBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokenResp.Body, githubAPIResponseLimit)).Decode(&tokenBody); err != nil {
		return nil, "", fmt.Errorf("decode installation token: %w", err)
	}
	if tokenBody.Token == "" {
		return nil, "", errors.New("github returned an empty installation token")
	}
	return client, tokenBody.Token, nil
}

type daemonDeploymentSource struct {
	ProjectID  string `json:"project_id"`
	Repository string `json:"repository"`
	GitRef     string `json:"git_ref,omitempty"`
	LocalPath  string `json:"local_path,omitempty"`
	DaemonID   string `json:"daemon_id,omitempty"`
}

func (h *Handler) daemonDeploymentSources(ctx context.Context, workspaceID string) []daemonDeploymentSource {
	rows, err := h.Queries.ListDeploymentSourceResourcesByWorkspace(ctx, parseUUID(workspaceID))
	if err != nil {
		slog.Warn("deployment source resources unavailable", "workspace_id", workspaceID, "error", err)
		return nil
	}
	type projectSources struct {
		github []struct{ repository, gitRef string }
		local  []struct{ path, daemonID string }
	}
	byProject := make(map[string]*projectSources)
	for _, row := range rows {
		projectID := uuidToString(row.ProjectID)
		entry := byProject[projectID]
		if entry == nil {
			entry = &projectSources{}
			byProject[projectID] = entry
		}
		switch row.ResourceType {
		case "github_repo":
			var ref githubRepoRef
			if json.Unmarshal(row.ResourceRef, &ref) != nil {
				continue
			}
			_, _, ok := githubRepositoryParts(ref.URL)
			if !ok {
				continue
			}
			gitRef := githubSourceRef(ref.Ref)
			if gitRef == "" {
				gitRef = githubSourceRef(ref.DefaultBranchHint)
			}
			// Keep the resource URL for the daemon's repo-cache key. The daemon
			// canonicalizes it before comparing the origin and reporting the
			// revision to the backend.
			entry.github = append(entry.github, struct{ repository, gitRef string }{strings.TrimSpace(ref.URL), gitRef})
		case "local_directory":
			var ref localDirectoryRef
			if json.Unmarshal(row.ResourceRef, &ref) != nil || strings.TrimSpace(ref.LocalPath) == "" {
				continue
			}
			entry.local = append(entry.local, struct{ path, daemonID string }{strings.TrimSpace(ref.LocalPath), strings.TrimSpace(ref.DaemonID)})
		}
	}
	result := make([]daemonDeploymentSource, 0)
	for projectID, sources := range byProject {
		for _, github := range sources.github {
			if len(sources.local) == 0 {
				result = append(result, daemonDeploymentSource{ProjectID: projectID, Repository: github.repository, GitRef: github.gitRef})
				continue
			}
			for _, local := range sources.local {
				result = append(result, daemonDeploymentSource{ProjectID: projectID, Repository: github.repository, GitRef: github.gitRef, LocalPath: local.path, DaemonID: local.daemonID})
			}
		}
	}
	return result
}

func validGitCommitSHA(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, ch := range value {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func (h *Handler) ReportDaemonDeploymentSourceRevision(w http.ResponseWriter, r *http.Request) {
	if middleware.DaemonAuthPathFromContext(r.Context()) != middleware.DaemonAuthPathDaemonToken {
		writeError(w, http.StatusUnauthorized, "daemon token required")
		return
	}
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceId"))
	if !h.requireDaemonWorkspaceAccess(w, r, workspaceID) {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "projectId"), "project id")
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	var req struct {
		Repository    string `json:"repository"`
		GitRef        string `json:"git_ref"`
		CommitSHA     string `json:"commit_sha"`
		CommitMessage string `json:"commit_message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	owner, repo, valid := githubRepositoryParts(req.Repository)
	if !valid || strings.TrimSpace(req.GitRef) == "" || !validGitCommitSHA(req.CommitSHA) || len(req.CommitMessage) > 4096 {
		writeError(w, http.StatusUnprocessableEntity, "invalid deployment source revision")
		return
	}
	repository := owner + "/" + repo
	resources, err := h.Queries.ListProjectResourcesInWorkspace(r.Context(), db.ListProjectResourcesInWorkspaceParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project resources")
		return
	}
	matched := false
	for _, resource := range resources {
		if resource.ResourceType != "github_repo" {
			continue
		}
		var ref githubRepoRef
		if json.Unmarshal(resource.ResourceRef, &ref) != nil {
			continue
		}
		resourceOwner, resourceRepo, resourceValid := githubRepositoryParts(ref.URL)
		resourceRef := githubSourceRef(ref.Ref)
		if resourceRef == "" {
			resourceRef = githubSourceRef(ref.DefaultBranchHint)
		}
		if resourceValid && strings.EqualFold(repository, resourceOwner+"/"+resourceRepo) &&
			(resourceRef == "" || strings.EqualFold(resourceRef, githubSourceRef(req.GitRef))) {
			matched = true
			break
		}
	}
	if !matched {
		writeError(w, http.StatusUnprocessableEntity, "deployment source repository is not attached to this project")
		return
	}
	stored, err := h.Queries.UpsertDeploymentSourceRevision(r.Context(), db.UpsertDeploymentSourceRevisionParams{
		WorkspaceID:   project.WorkspaceID,
		ProjectID:     project.ID,
		Repository:    repository,
		GitRef:        githubSourceRef(req.GitRef),
		CommitSha:     strings.TrimSpace(req.CommitSHA),
		CommitMessage: pgtype.Text{String: strings.TrimSpace(req.CommitMessage), Valid: strings.TrimSpace(req.CommitMessage) != ""},
		DaemonID:      middleware.DaemonIDFromContext(r.Context()),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist deployment source revision")
		return
	}
	writeJSON(w, http.StatusOK, deploymentSourceRevisionResponse{
		Repository: stored.Repository, GitRef: stored.GitRef, CommitSHA: stored.CommitSha, CommitMessage: stored.CommitMessage.String,
	})
}

func (h *Handler) GetProjectDeploymentSourceRevision(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	resources, err := h.Queries.ListProjectResources(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project resources")
		return
	}
	var repositoryURL, requestedRef string
	for _, resource := range resources {
		if resource.ResourceType != "github_repo" {
			continue
		}
		var ref struct {
			URL               string `json:"url"`
			Ref               string `json:"ref"`
			DefaultBranchHint string `json:"default_branch_hint"`
		}
		if json.Unmarshal(resource.ResourceRef, &ref) == nil {
			repositoryURL = strings.TrimSpace(ref.URL)
			requestedRef = strings.TrimSpace(ref.Ref)
			if requestedRef == "" {
				requestedRef = strings.TrimSpace(ref.DefaultBranchHint)
			}
			break
		}
	}
	owner, repo, valid := githubRepositoryParts(repositoryURL)
	if !valid {
		writeError(w, http.StatusUnprocessableEntity, "source revision unavailable")
		return
	}
	stored, storedErr := h.Queries.GetDeploymentSourceRevision(r.Context(), db.GetDeploymentSourceRevisionParams{
		WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Repository: owner + "/" + repo,
	})
	if storedErr == nil && (requestedRef == "" || strings.EqualFold(githubSourceRef(stored.GitRef), githubSourceRef(requestedRef))) {
		writeJSON(w, http.StatusOK, deploymentSourceRevisionResponse{
			Repository: stored.Repository, GitRef: stored.GitRef, CommitSHA: stored.CommitSha, CommitMessage: stored.CommitMessage.String,
		})
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	if revision, publicErr := fetchGitHubSourceRevision(r.Context(), client, "", owner, repo, requestedRef); publicErr == nil {
		writeJSON(w, http.StatusOK, deploymentSourceRevisionResponse(revision))
		return
	}

	installations, err := h.Queries.ListGitHubInstallationsByWorkspace(r.Context(), project.WorkspaceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load github connection")
		return
	}
	for _, installation := range installations {
		installationClient, token, tokenErr := acquireGitHubInstallationToken(r.Context(), installation.InstallationID, true)
		if tokenErr != nil {
			continue
		}
		revision, revisionErr := fetchGitHubSourceRevision(r.Context(), installationClient, token, owner, repo, requestedRef)
		revokeGitHubInstallationToken(installationClient, token)
		if revisionErr == nil {
			writeJSON(w, http.StatusOK, deploymentSourceRevisionResponse(revision))
			return
		}
	}
	writeError(w, http.StatusUnprocessableEntity, "source revision unavailable")
}
