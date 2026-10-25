package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runDeploymentRevisionGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func newDeploymentRevisionRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runDeploymentRevisionGit(t, dir, "init", "--initial-branch=main")
	runDeploymentRevisionGit(t, dir, "remote", "add", "origin", "https://github.com/acme/private-app.git")
	runDeploymentRevisionGit(t, dir, "config", "user.email", "test@example.com")
	runDeploymentRevisionGit(t, dir, "config", "user.name", "Deployment Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("private source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDeploymentRevisionGit(t, dir, "add", "README.md")
	runDeploymentRevisionGit(t, dir, "commit", "-m", "publish private source")
	return dir
}

func TestResolveDaemonDeploymentSourceRevision_UsesVerifiedPrivateRepository(t *testing.T) {
	dir := newDeploymentRevisionRepo(t)

	revision, err := resolveDaemonDeploymentSourceRevision(context.Background(), dir, "https://github.com/acme/private-app.git", "refs/heads/main")
	if err != nil {
		t.Fatalf("resolve revision: %v", err)
	}
	if revision.Repository != "acme/private-app" || revision.GitRef != "main" {
		t.Fatalf("revision identity = %#v", revision)
	}
	if len(revision.CommitSHA) != 40 || revision.CommitMessage != "publish private source" {
		t.Fatalf("revision details = %#v", revision)
	}
}

func TestResolveDaemonDeploymentSourceRevision_FailsClosedOnOriginMismatch(t *testing.T) {
	dir := newDeploymentRevisionRepo(t)

	_, err := resolveDaemonDeploymentSourceRevision(context.Background(), dir, "https://github.com/acme/other-app", "main")
	if err == nil || !strings.Contains(err.Error(), "origin does not match") {
		t.Fatalf("expected origin mismatch, got %v", err)
	}
}

func TestReportDeploymentSources_SendsVerifiedRevisionToBackend(t *testing.T) {
	dir := newDeploymentRevisionRepo(t)
	var got DeploymentSourceRevision
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/workspaces/workspace-1/projects/project-1/deployment-source-revision" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL)
	client.SetToken("daemon-token")
	d := &Daemon{client: client, logger: testLogger(), cfg: Config{DaemonID: "daemon-1"}}
	source := DeploymentSourceBinding{ProjectID: "project-1", Repository: "acme/private-app", GitRef: "main", LocalPath: dir, DaemonID: "daemon-1"}
	if got := daemonDeploymentSourcePath(d, "workspace-1", source); got != dir {
		t.Fatalf("source path = %q", got)
	}
	d.reportDeploymentSources(context.Background(), "workspace-1", []DeploymentSourceBinding{source})

	if gotAuth != "Bearer daemon-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if got.Repository != "acme/private-app" || got.GitRef != "main" || len(got.CommitSHA) != 40 {
		t.Fatalf("reported revision = %#v", got)
	}
	if got.CommitMessage != "publish private source" {
		t.Fatalf("reported commit message = %q", got.CommitMessage)
	}
}

func testLogger() *slog.Logger { return slog.Default() }
