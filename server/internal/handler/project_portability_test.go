package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/projectbundle"
)

func TestBundleFromRequestUsesJSONTitleHintOnlyWhenMissing(t *testing.T) {
	bundle, err := projectbundle.DecodeBundle([]byte(`{"schema_version":"multica.project.v1","project":{},"tasks":[]}`))
	if err != nil {
		t.Fatalf("decode sparse bundle: %v", err)
	}
	got, err := bundleFromRequest(&bundle, "", "Fallback project")
	if err != nil {
		t.Fatalf("bundleFromRequest with title hint: %v", err)
	}
	if got.Project.Title != "Fallback project" {
		t.Fatalf("project title = %q, want title hint", got.Project.Title)
	}

	bundle.Project.Title = "Bundle title"
	got, err = bundleFromRequest(&bundle, "", "Ignored hint")
	if err != nil {
		t.Fatalf("bundleFromRequest with bundle title: %v", err)
	}
	if got.Project.Title != "Bundle title" {
		t.Fatalf("project title = %q, want bundle title to take precedence", got.Project.Title)
	}
}

func TestProjectPortabilityImportDryRunDoesNotWrite(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	title := "Portability dry run " + uuid.NewString()
	body := map[string]any{
		"bundle": map[string]any{
			"schema_version": "multica.project.v1",
			"project":        map[string]any{"title": title},
			"tasks":          []any{},
		},
		"dry_run": true,
	}
	request := newRequest("POST", "/api/projects/import", body)
	response := httptest.NewRecorder()
	testHandler.ImportProject(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ImportProject dry run status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode dry-run response: %v", err)
	}
	if _, exists := payload["project_id"]; exists {
		t.Fatalf("dry-run create unexpectedly returned a project_id: %s", response.Body.String())
	}
	var created int
	if err := testPool.QueryRow(request.Context(), `SELECT count(*) FROM project WHERE workspace_id = $1 AND title = $2`, testWorkspaceID, title).Scan(&created); err != nil {
		t.Fatalf("count projects after dry run: %v", err)
	}
	if created != 0 {
		t.Fatalf("dry-run import created %d project rows", created)
	}
}

func TestProjectDiscoveryImportPreviewsBeforeApplyingReviewedReport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	previousAnalyzer := testHandler.ProjectDiscoveryAnalyzer
	testHandler.ProjectDiscoveryAnalyzer = func(_ context.Context, _ projectbundle.ProjectContextPayload, instruction string) (projectbundle.ProjectDiscoveryReport, error) {
		if instruction != "Improve the inventory workflow" {
			t.Errorf("discovery instruction = %q", instruction)
		}
		return projectbundle.ProjectDiscoveryReport{
			Project:             projectbundle.DiscoveredProject{Title: "Discovered inventory", Purpose: "Track stock", Goal: "Reduce stock errors", ArchitectureSummary: "An API and web client."},
			Boundaries:          projectbundle.DiscoveredBoundaries{DefinitionOfDone: []string{"Stock changes are auditable"}},
			ImplementedFeatures: []projectbundle.DiscoveredFeature{{Key: "feature-auth", Title: "Authentication", Description: "Users can sign in.", Evidence: []string{"src/auth.any"}}},
			NextTasks:           []projectbundle.DiscoveredTask{{Key: "task-audit", Title: "Add stock audit history", Description: "Record quantity changes.", Priority: "high", Stage: 1, BlockedByKeys: []string{"feature-auth"}, AcceptanceCriteria: []string{"Each change has an actor."}}},
		}, nil
	}
	t.Cleanup(func() { testHandler.ProjectDiscoveryAnalyzer = previousAnalyzer })

	contextPayload := projectbundle.ProjectContextPayload{RootName: "inventory", Files: []projectbundle.ProjectContextFile{{Path: "src/auth.any", Content: "sign-in flow"}}}
	previewResponse := httptest.NewRecorder()
	testHandler.ImportProject(previewResponse, newRequest("POST", "/api/projects/import", projectbundle.ImportRequest{
		Instruction: "Improve the inventory workflow", ProjectContext: &contextPayload, DryRun: true,
	}))
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("discovery preview status = %d, want 200: %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview struct {
		DiscoveryReport *projectbundle.ProjectDiscoveryReport `json:"discovery_report"`
		Preview         *projectbundle.PortabilityPreview     `json:"preview"`
	}
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode discovery preview: %v", err)
	}
	if preview.DiscoveryReport == nil || preview.Preview == nil {
		t.Fatalf("discovery preview payload = %s, want report and changes", previewResponse.Body.String())
	}
	if !strings.Contains(strings.Join(preview.Preview.Warnings, " "), "no agent-accessible") {
		t.Fatalf("preview did not disclose unattached source: %#v", preview.Preview.Warnings)
	}
	if preview.DiscoveryReport.Project.Title != "Discovered inventory" || len(preview.DiscoveryReport.NextTasks) != 1 {
		t.Fatalf("discovery report = %#v", preview.DiscoveryReport)
	}

	applyResponse := httptest.NewRecorder()
	testHandler.ImportProject(applyResponse, newRequest("POST", "/api/projects/import", projectbundle.ImportRequest{DiscoveryReport: preview.DiscoveryReport}))
	if applyResponse.Code != http.StatusCreated {
		t.Fatalf("reviewed discovery apply status = %d, want 201: %s", applyResponse.Code, applyResponse.Body.String())
	}
	var applied projectbundle.PortabilityResult
	if err := json.Unmarshal(applyResponse.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode applied discovery result: %v", err)
	}
	if applied.ProjectID == "" || applied.DiscoveryReport == nil || applied.CreatedCount < 2 {
		t.Fatalf("applied discovery result = %+v", applied)
	}
	t.Cleanup(func() { cleanupProjectPortabilityFixture(t, applied.ProjectID) })
	var brainCount, issueCount, specCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM autonomous_project_brain_entry WHERE project_id = $1`, applied.ProjectID).Scan(&brainCount); err != nil {
		t.Fatalf("count imported project brain entries: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE project_id = $1`, applied.ProjectID).Scan(&issueCount); err != nil {
		t.Fatalf("count imported project tasks: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM autonomous_project_specification_revision WHERE project_id = $1`, applied.ProjectID).Scan(&specCount); err != nil {
		t.Fatalf("count imported project specification revisions: %v", err)
	}
	if brainCount == 0 || issueCount != 2 || specCount == 0 {
		t.Fatalf("imported project OS state: brain=%d issues=%d spec=%d", brainCount, issueCount, specCount)
	}
	var planStatus string
	var approvalPending, digestValid bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, approved_at IS NULL, plan_digest = autonomous_project_plan_digest(id)
		FROM autonomous_project_plan WHERE project_id = $1 ORDER BY revision DESC LIMIT 1
	`, applied.ProjectID).Scan(&planStatus, &approvalPending, &digestValid); err != nil {
		t.Fatalf("read imported project plan approval state: %v", err)
	}
	if planStatus != "awaiting_approval" || !approvalPending || !digestValid {
		t.Fatalf("imported plan lifecycle status=%q approval_pending=%v digest_valid=%v", planStatus, approvalPending, digestValid)
	}
}

func TestProjectPortabilityImportRequiresWorkspaceMembership(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	body := map[string]any{
		"bundle": map[string]any{
			"schema_version": "multica.project.v1",
			"project":        map[string]any{"title": "Unauthorized portability import"},
			"tasks":          []any{},
		},
		"dry_run": true,
	}
	request := newRequestAs(uuid.NewString(), "POST", "/api/projects/import", body)
	response := httptest.NewRecorder()
	testHandler.ImportProject(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("ImportProject without workspace membership status = %d, want 404: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "preview") {
		t.Fatalf("unauthorized import returned a preview: %s", response.Body.String())
	}
}

func TestProjectPortabilityImportExportAndIdempotentSync(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	title := "Portability round trip " + uuid.NewString()
	importBody := map[string]any{
		"bundle": map[string]any{
			"schema_version": "multica.project.v1",
			"project":        map[string]any{"title": title},
			"tasks":          []any{map[string]any{"key": "portable-root", "title": "Root task"}},
		},
	}
	importResponse := httptest.NewRecorder()
	testHandler.ImportProject(importResponse, newRequest("POST", "/api/projects/import", importBody))
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("ImportProject status = %d, want 201: %s", importResponse.Code, importResponse.Body.String())
	}
	var imported struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(importResponse.Body.Bytes(), &imported); err != nil || imported.ProjectID == "" {
		t.Fatalf("decode import result project id %q: %v", imported.ProjectID, err)
	}
	t.Cleanup(func() { cleanupProjectPortabilityFixture(t, imported.ProjectID) })

	exportRequest := withURLParam(newRequest("GET", "/api/projects/"+imported.ProjectID+"/export", nil), "id", imported.ProjectID)
	exportResponse := httptest.NewRecorder()
	testHandler.ExportProject(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("ExportProject status = %d, want 200: %s", exportResponse.Code, exportResponse.Body.String())
	}
	var exported struct {
		Bundle        map[string]any `json:"bundle"`
		TasksMarkdown string         `json:"tasks_markdown"`
		GuideMarkdown string         `json:"agent_guide_markdown"`
	}
	if err := json.Unmarshal(exportResponse.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if !strings.Contains(exported.TasksMarkdown, "portable-root") || !strings.Contains(exported.GuideMarkdown, "multica project sync") {
		t.Fatalf("export markdown missing stable task guidance: tasks=%q guide=%q", exported.TasksMarkdown, exported.GuideMarkdown)
	}

	syncResponse := httptest.NewRecorder()
	syncRequest := withURLParam(newRequest("POST", "/api/projects/"+imported.ProjectID+"/sync", map[string]any{"bundle": exported.Bundle}), "id", imported.ProjectID)
	testHandler.SyncProject(syncResponse, syncRequest)
	if syncResponse.Code != http.StatusOK {
		t.Fatalf("SyncProject status = %d, want 200: %s", syncResponse.Code, syncResponse.Body.String())
	}
	var syncResult struct {
		CreatedCount   int `json:"created_count"`
		UpdatedCount   int `json:"updated_count"`
		UnchangedCount int `json:"unchanged_count"`
	}
	if err := json.Unmarshal(syncResponse.Body.Bytes(), &syncResult); err != nil {
		t.Fatalf("decode sync result: %v", err)
	}
	if syncResult.CreatedCount != 0 || syncResult.UpdatedCount != 0 || syncResult.UnchangedCount != 1 {
		t.Fatalf("idempotent sync counts = %+v, want one unchanged task", syncResult)
	}

	bundle := exported.Bundle
	tasks := bundle["tasks"].([]any)
	tasks = append(tasks, map[string]any{"key": "portable-child", "title": "Added child", "parent_key": "portable-root"})
	bundle["tasks"] = tasks
	changedResponse := httptest.NewRecorder()
	changedRequest := withURLParam(newRequest("POST", "/api/projects/"+imported.ProjectID+"/sync", map[string]any{"bundle": bundle}), "id", imported.ProjectID)
	testHandler.SyncProject(changedResponse, changedRequest)
	if changedResponse.Code != http.StatusOK {
		t.Fatalf("SyncProject with new task status = %d, want 200: %s", changedResponse.Code, changedResponse.Body.String())
	}
	var taskCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1 AND project_id = $2`, testWorkspaceID, imported.ProjectID).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks after sync: %v", err)
	}
	if taskCount != 2 {
		t.Fatalf("task count after additive sync = %d, want 2", taskCount)
	}
}

func TestProjectPortabilityMarkdownImport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	title := "Markdown portability " + uuid.NewString()
	body := map[string]any{
		"markdown":           "- [ ] First task\n  - [x] Nested task\n",
		"project_title_hint": title,
	}
	response := httptest.NewRecorder()
	testHandler.ImportProject(response, newRequest("POST", "/api/projects/import", body))
	if response.Code != http.StatusCreated {
		t.Fatalf("Markdown ImportProject status = %d, want 201: %s", response.Code, response.Body.String())
	}
	var result struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ProjectID == "" {
		t.Fatalf("decode Markdown import project id %q: %v", result.ProjectID, err)
	}
	t.Cleanup(func() { cleanupProjectPortabilityFixture(t, result.ProjectID) })
	var taskCount, childCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1 AND project_id = $2`, testWorkspaceID, result.ProjectID).Scan(&taskCount); err != nil {
		t.Fatalf("count Markdown-imported tasks: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1 AND project_id = $2 AND parent_issue_id IS NOT NULL`, testWorkspaceID, result.ProjectID).Scan(&childCount); err != nil {
		t.Fatalf("count Markdown-imported subtasks: %v", err)
	}
	if taskCount != 2 || childCount != 1 {
		t.Fatalf("Markdown import tasks=%d subtasks=%d, want 2 and 1", taskCount, childCount)
	}
}

func cleanupProjectPortabilityFixture(t *testing.T, projectID string) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`DELETE FROM issue_dependency WHERE issue_id IN (SELECT id FROM issue WHERE project_id = $1) OR depends_on_issue_id IN (SELECT id FROM issue WHERE project_id = $1)`,
		`DELETE FROM issue_to_label WHERE issue_id IN (SELECT id FROM issue WHERE project_id = $1)`,
		`DELETE FROM activity_log WHERE issue_id IN (SELECT id FROM issue WHERE project_id = $1)`,
		`DELETE FROM issue_child_event WHERE parent_id IN (SELECT id FROM issue WHERE project_id = $1) OR child_id IN (SELECT id FROM issue WHERE project_id = $1)`,
		`DELETE FROM comment WHERE issue_id IN (SELECT id FROM issue WHERE project_id = $1)`,
		`DELETE FROM issue WHERE project_id = $1`,
		`DELETE FROM project_resource WHERE project_id = $1`,
		`DELETE FROM autonomous_project_brain_entry WHERE project_id = $1`,
		`DELETE FROM autonomous_project_plan_edge WHERE project_id = $1`,
		`DELETE FROM autonomous_project_plan_node WHERE project_id = $1`,
		`DELETE FROM autonomous_project_plan WHERE project_id = $1`,
		`DELETE FROM autonomous_project_bootstrap WHERE project_id = $1`,
		`DELETE FROM project WHERE id = $1`,
	} {
		if _, err := testPool.Exec(ctx, statement, projectID); err != nil {
			t.Fatalf("cleanup project portability fixture with %q: %v", statement, err)
		}
	}
}
