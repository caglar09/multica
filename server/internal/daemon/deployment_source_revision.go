package daemon

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DeploymentSourceRevision is the daemon-verified source identity used by
// deployment providers. It deliberately contains no credentials or file data.
type DeploymentSourceRevision struct {
	Repository    string `json:"repository"`
	GitRef        string `json:"git_ref"`
	CommitSHA     string `json:"commit_sha"`
	CommitMessage string `json:"commit_message,omitempty"`
}

func canonicalGitHubRepository(raw string) (string, bool) {
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
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
	if owner == "" || repo == "" {
		return "", false
	}
	return owner + "/" + repo, true
}

func runDaemonGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", errors.New("repository path is empty")
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func resolveDaemonDeploymentSourceRevision(ctx context.Context, repoPath, repository, requestedRef string) (DeploymentSourceRevision, error) {
	canonical, ok := canonicalGitHubRepository(repository)
	if !ok {
		return DeploymentSourceRevision{}, errors.New("deployment source repository is not a GitHub repository")
	}
	remote, err := runDaemonGit(ctx, repoPath, "remote", "get-url", "origin")
	if err != nil {
		return DeploymentSourceRevision{}, errors.New("repository origin is unavailable")
	}
	remoteCanonical, ok := canonicalGitHubRepository(remote)
	if !ok || !strings.EqualFold(remoteCanonical, canonical) {
		return DeploymentSourceRevision{}, errors.New("repository origin does not match deployment source")
	}
	ref := strings.TrimSpace(requestedRef)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")
	if ref == "" {
		ref, err = runDaemonGit(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil || ref == "" {
			return DeploymentSourceRevision{}, errors.New("repository branch is unavailable")
		}
	}
	commit, err := runDaemonGit(ctx, repoPath, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil || commit == "" {
		return DeploymentSourceRevision{}, errors.New("repository ref is unavailable")
	}
	message, err := runDaemonGit(ctx, repoPath, "show", "-s", "--format=%B", commit)
	if err != nil {
		return DeploymentSourceRevision{}, errors.New("repository commit message is unavailable")
	}
	return DeploymentSourceRevision{Repository: canonical, GitRef: ref, CommitSHA: commit, CommitMessage: message}, nil
}

func daemonDeploymentSourcePath(d *Daemon, workspaceID string, source DeploymentSourceBinding) string {
	if strings.TrimSpace(source.LocalPath) != "" && d.daemonIDMatches(source.DaemonID) {
		return strings.TrimSpace(source.LocalPath)
	}
	if d.repoCache == nil {
		return ""
	}
	return d.repoCache.Lookup(workspaceID, source.Repository)
}

func (d *Daemon) daemonIDMatches(daemonID string) bool {
	daemonID = strings.TrimSpace(daemonID)
	if daemonID == "" || daemonID == d.cfg.DaemonID {
		return daemonID == d.cfg.DaemonID
	}
	for _, legacyID := range d.cfg.LegacyDaemonIDs {
		if daemonID == strings.TrimSpace(legacyID) {
			return true
		}
	}
	return false
}

func (d *Daemon) reportDeploymentSources(ctx context.Context, workspaceID string, sources []DeploymentSourceBinding) {
	for _, source := range sources {
		if strings.TrimSpace(source.ProjectID) == "" || strings.TrimSpace(source.Repository) == "" {
			continue
		}
		path := daemonDeploymentSourcePath(d, workspaceID, source)
		if path == "" {
			continue
		}
		resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		revision, err := resolveDaemonDeploymentSourceRevision(resolveCtx, path, source.Repository, source.GitRef)
		cancel()
		if err != nil {
			d.logger.Debug("deployment source revision unavailable", "workspace_id", workspaceID, "project_id", source.ProjectID, "error", err)
			continue
		}
		if err := d.client.ReportDeploymentSourceRevision(ctx, workspaceID, source.ProjectID, revision); err != nil {
			d.logger.Debug("deployment source revision report failed", "workspace_id", workspaceID, "project_id", source.ProjectID, "error", err)
		}
	}
}
