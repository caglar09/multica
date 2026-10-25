package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestFetchGitHubSourceRevisionResolvesSelectedBranchHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/caglar09/clock-time/commits/main" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected authorization", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sha":"c82e4ff816e642c2907b6953e19cb9","commit":{"message":"fix: deploy clock-time"}}`))
	}))
	defer server.Close()

	previousBase := githubAPIBase
	githubAPIBase = server.URL
	t.Cleanup(func() { githubAPIBase = previousBase })

	revision, err := fetchGitHubSourceRevision(
		context.Background(),
		server.Client(),
		"",
		"caglar09",
		"clock-time",
		"main",
	)
	if err != nil {
		t.Fatalf("fetch source revision: %v", err)
	}
	if revision.Repository != "caglar09/clock-time" || revision.GitRef != "main" || revision.CommitSHA != "c82e4ff816e642c2907b6953e19cb9" {
		t.Fatalf("revision = %+v", revision)
	}
	if revision.CommitMessage != "fix: deploy clock-time" {
		t.Fatalf("commit message = %q", revision.CommitMessage)
	}
}

func TestReportDaemonDeploymentSourceRevision_PersistsPrivateRepoRevision(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	projectID := dbfx.Project(t, "Private deployment source")
	dbfx.Insert(t, "project_resource", testutil.Cols{
		"project_id": projectID, "workspace_id": testWorkspaceID, "resource_type": "github_repo",
		"resource_ref": `{"url":"https://github.com/acme/private-app.git","ref":"main"}`, "position": 0,
	})

	req := withURLParams(newDaemonTokenRequest(http.MethodPost, "/api/daemon/workspaces/"+testWorkspaceID+"/projects/"+projectID+"/deployment-source-revision", map[string]string{
		"repository": "acme/private-app", "git_ref": "main", "commit_sha": "0123456789abcdef0123456789abcdef01234567", "commit_message": "publish private source",
	}, testWorkspaceID, "daemon-private-repo"), "workspaceId", testWorkspaceID, "projectId", projectID)
	w := testutil.Call(t, testHandler.ReportDaemonDeploymentSourceRevision, req).Want(http.StatusOK)
	var response deploymentSourceRevisionResponse
	if err := json.Unmarshal([]byte(w.Body.String()), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Repository != "acme/private-app" || response.GitRef != "main" || response.CommitSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("response = %+v", response)
	}
	stored, err := testHandler.Queries.GetDeploymentSourceRevision(context.Background(), db.GetDeploymentSourceRevisionParams{
		WorkspaceID: parseUUID(testWorkspaceID), ProjectID: parseUUID(projectID), Repository: "acme/private-app",
	})
	if err != nil {
		t.Fatalf("load persisted revision: %v", err)
	}
	if stored.CommitSha != response.CommitSHA || stored.DaemonID != "daemon-private-repo" {
		t.Fatalf("stored revision = %+v", stored)
	}
	if sources := testHandler.daemonDeploymentSources(context.Background(), testWorkspaceID); len(sources) != 1 || sources[0].ProjectID != projectID || sources[0].Repository != "https://github.com/acme/private-app.git" || sources[0].GitRef != "main" {
		t.Fatalf("daemon deployment sources = %+v", sources)
	}
	previousBase := githubAPIBase
	githubAPIBase = "http://127.0.0.1:1"
	t.Cleanup(func() { githubAPIBase = previousBase })
	getReq := withURLParam(newRequestAs(testUserID, http.MethodGet, "/api/projects/"+projectID+"/deployment-source-revision", nil), "id", projectID)
	getResponse := testutil.Call(t, testHandler.GetProjectDeploymentSourceRevision, getReq).Want(http.StatusOK)
	var resolved deploymentSourceRevisionResponse
	if err := json.Unmarshal([]byte(getResponse.Body.String()), &resolved); err != nil {
		t.Fatalf("decode resolved response: %v", err)
	}
	if resolved.CommitSHA != response.CommitSHA || resolved.Repository != response.Repository {
		t.Fatalf("resolved response = %+v", resolved)
	}

	wrongRef := withURLParams(newDaemonTokenRequest(http.MethodPost, "/api/daemon/workspaces/"+testWorkspaceID+"/projects/"+projectID+"/deployment-source-revision", map[string]string{
		"repository": "acme/private-app", "git_ref": "release", "commit_sha": "abcdef0123456789abcdef0123456789abcdef01",
	}, testWorkspaceID, "daemon-private-repo"), "workspaceId", testWorkspaceID, "projectId", projectID)
	testutil.Call(t, testHandler.ReportDaemonDeploymentSourceRevision, wrongRef).Want(http.StatusUnprocessableEntity)
}

func TestReportDaemonDeploymentSourceRevision_RejectsCrossWorkspaceProject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	foreignWorkspaceID := dbfx.Insert(t, "workspace", testutil.Cols{
		"name": "Foreign deployment workspace", "slug": "foreign-deployment-source", "description": "", "issue_prefix": "FDS",
	})
	foreignProjectID := dbfx.Project(t, "Foreign deployment project", testutil.Cols{"workspace_id": foreignWorkspaceID})
	req := withURLParams(newDaemonTokenRequest(http.MethodPost, "/api/daemon/workspaces/"+testWorkspaceID+"/projects/"+foreignProjectID+"/deployment-source-revision", map[string]string{
		"repository": "acme/private-app", "git_ref": "main", "commit_sha": "0123456789abcdef0123456789abcdef01234567",
	}, testWorkspaceID, "daemon-private-repo"), "workspaceId", testWorkspaceID, "projectId", foreignProjectID)
	testutil.Call(t, testHandler.ReportDaemonDeploymentSourceRevision, req).Want(http.StatusNotFound)
}
