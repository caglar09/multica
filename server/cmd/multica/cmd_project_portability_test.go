package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/projectbundle"
)

const testPortableProjectID = "11111111-1111-4111-8111-111111111111"

func newProjectExportTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "export"}
	cmd.Flags().String("file", "", "")
	cmd.Flags().String("dir", "", "")
	cmd.Flags().String("output", "json", "")
	return cmd
}

func newProjectImportTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "import"}
	cmd.Flags().String("file", "", "")
	cmd.Flags().String("dir", "", "")
	cmd.Flags().String("title", "", "")
	cmd.Flags().String("goal", "", "")
	cmd.Flags().Bool("dry-run", false, "")
	cmd.Flags().String("output", "table", "")
	return cmd
}

func newProjectSyncTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "sync"}
	cmd.Flags().String("file", "", "")
	cmd.Flags().String("dir", "", "")
	cmd.Flags().Bool("dry-run", false, "")
	cmd.Flags().String("output", "table", "")
	return cmd
}

func portableProjectBundleJSON(t *testing.T) []byte {
	t.Helper()
	bundle, err := projectbundle.DecodeBundle([]byte(`{
		"schema_version": "multica.project.v1",
		"exported_at": "2026-10-28T12:00:00Z",
		"origin": {"workspace_id": "22222222-2222-4222-8222-222222222222", "project_id": "` + testPortableProjectID + `"},
		"project": {"title": "Portable project", "status": "planned", "priority": "medium"},
		"tasks": []
	}`))
	if err != nil {
		t.Fatalf("decode test bundle: %v", err)
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("encode test bundle: %v", err)
	}
	return data
}

func prepareProjectPortabilityCLI(t *testing.T, serverURL string) {
	t.Helper()
	redirectTestHome(t, t.TempDir())
	t.Setenv("MULTICA_SERVER_URL", serverURL)
	t.Setenv("MULTICA_WORKSPACE_ID", "22222222-2222-4222-8222-222222222222")
	t.Setenv("MULTICA_TOKEN", "test-token")
}

func TestProjectPortabilityInputRejectsAmbiguousAndMalformedFiles(t *testing.T) {
	if _, err := readProjectPortabilityInput("one.json", "project", true, true, ""); err == nil {
		t.Fatal("expected mutually exclusive input paths to be rejected")
	}

	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProjectPortabilityInput(path, "", true, false, ""); err == nil {
		t.Fatal("expected malformed JSON bundle to be rejected")
	}
}

func TestProjectPortabilityDirectoryReconcilesBundleWithTasks(t *testing.T) {
	dir := t.TempDir()
	metadataDir := filepath.Join(dir, ".multica")
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	baseJSON := []byte(`{
		"schema_version": "multica.project.v1",
		"project": {"title": "Portable project"},
		"tasks": [{"key": "TASK-1", "title": "Finish item", "status": "done", "status_category": "done"}]
	}`)
	if err := os.WriteFile(filepath.Join(metadataDir, "project.json"), baseJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := projectbundle.DecodeBundle(baseJSON)
	if err != nil {
		t.Fatal(err)
	}
	markdown := strings.Replace(projectbundle.RenderTasksMarkdown(bundle), "- [x] Finish item", "- [ ] Finish item", 1)
	if err := os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte(markdown), 0o600); err != nil {
		t.Fatal(err)
	}

	input, err := readProjectPortabilityInput("", dir, false, true, "")
	if err != nil {
		t.Fatalf("read project directory: %v", err)
	}
	if input.Bundle == nil || input.Markdown != "" {
		t.Fatalf("directory input = %#v, want a reconciled bundle", input)
	}
	if input.Bundle.Project.Title != "Portable project" {
		t.Fatalf("project title = %q, want Portable project", input.Bundle.Project.Title)
	}
	if len(input.Bundle.Tasks) != 1 || input.Bundle.Tasks[0].Status == nil || *input.Bundle.Tasks[0].Status != "todo" {
		t.Fatalf("reconciled tasks = %#v, want TASK-1 changed to todo", input.Bundle.Tasks)
	}
}

func TestProjectPortabilityDirectoryCollectsRawProjectContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.source"), []byte("A project without Multica metadata."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", ".env.local"), []byte("SECRET=value"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := readProjectPortabilityInput("", dir, false, true, "")
	if err != nil {
		t.Fatalf("read raw project directory: %v", err)
	}
	if input.Bundle != nil || input.Markdown != "" || input.ProjectContext == nil {
		t.Fatalf("raw directory input = %#v, want project context only", input)
	}
	if len(input.ProjectContext.Files) != 1 || input.ProjectContext.Files[0].Path != "README.source" {
		t.Fatalf("collected files = %#v, want README.source only", input.ProjectContext.Files)
	}
	if strings.Contains(input.ProjectContext.Files[0].Content, "SECRET") {
		t.Fatal("sensitive file contents were included in project context")
	}
}

func TestProjectPortabilityRawImportPreviewsAndRequiresConfirmation(t *testing.T) {
	var requests int
	var requestBodies []map[string]any
	report := map[string]any{
		"project":    map[string]any{"title": "Discovered", "purpose": "Purpose", "goal": "Goal", "architecture_summary": "Architecture"},
		"boundaries": map[string]any{}, "implemented_features": []any{}, "next_tasks": []any{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requestBodies = append(requestBodies, body)
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"discovery_report": report, "preview": map[string]any{"project_action": "create", "warnings": []string{}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"project_id": testPortableProjectID, "warnings": []string{}})
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.src"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newProjectImportTestCmd()
	_ = cmd.Flags().Set("dir", dir)
	_ = cmd.Flags().Set("goal", "Improve the project")
	cmd.SetIn(strings.NewReader("y\n"))
	var stderr strings.Builder
	cmd.SetErr(&stderr)
	_, err := captureStdout(t, func() error { return runProjectImport(cmd, nil) })
	if err != nil {
		t.Fatalf("run raw project import: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want preview then confirmed import", requests)
	}
	if requestBodies[0]["dry_run"] != true || requestBodies[0]["instruction"] != "Improve the project" {
		t.Fatalf("preview request = %#v, want dry_run and goal instruction", requestBodies[0])
	}
	if _, ok := requestBodies[0]["project_context"].(map[string]any); !ok {
		t.Fatalf("preview request context = %#v, want project context", requestBodies[0]["project_context"])
	}
	if requestBodies[1]["dry_run"] != nil && requestBodies[1]["dry_run"] != false {
		t.Fatalf("apply request dry_run = %#v, want false", requestBodies[1]["dry_run"])
	}
	if _, ok := requestBodies[1]["project_context"]; ok {
		t.Fatalf("apply request unexpectedly repeated project context: %#v", requestBodies[1])
	}
	if _, ok := requestBodies[1]["discovery_report"].(map[string]any); !ok {
		t.Fatalf("apply request discovery report = %#v, want reviewed report", requestBodies[1]["discovery_report"])
	}
	if !strings.Contains(stderr.String(), "Import this reviewed project") {
		t.Fatalf("confirmation prompt missing: %q", stderr.String())
	}
}

func TestProjectPortabilityRawImportStopsWhenPreviewIsDeclined(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewDecoder(r.Body).Decode(new(map[string]any))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"discovery_report": map[string]any{
				"project":    map[string]any{"title": "Discovered", "purpose": "Purpose", "goal": "Goal", "architecture_summary": "Architecture"},
				"boundaries": map[string]any{}, "implemented_features": []any{}, "next_tasks": []any{},
			},
			"preview": map[string]any{"project_action": "create", "warnings": []string{}},
		})
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.src"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newProjectImportTestCmd()
	_ = cmd.Flags().Set("dir", dir)
	cmd.SetIn(strings.NewReader("n\n"))
	if _, err := captureStdout(t, func() error { return runProjectImport(cmd, nil) }); err != nil {
		t.Fatalf("run declined project import: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want preview only after declining", requests)
	}
}

func TestProjectPortabilityBundleRequestPreservesSparseFields(t *testing.T) {
	bundle, err := projectbundle.DecodeBundle([]byte(`{
		"schema_version": "multica.project.v1",
		"project": {"title": "Portable project"},
		"tasks": [
			{"key": "empty", "title": "Clear fields", "labels": [], "blocked_by_keys": []},
			{"key": "omitted", "title": "Leave fields alone"}
		]
	}`))
	if err != nil {
		t.Fatalf("decode test bundle: %v", err)
	}
	payload, err := json.Marshal(projectImportRequest(projectPortabilityInput{Bundle: &bundle}, false, ""))
	if err != nil {
		t.Fatalf("encode CLI request: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode CLI request: %v", err)
	}
	portableBundle, ok := request["bundle"].(map[string]any)
	if !ok {
		t.Fatalf("bundle = %T, want object", request["bundle"])
	}
	tasks, ok := portableBundle["tasks"].([]any)
	if !ok || len(tasks) != 2 {
		t.Fatalf("tasks = %#v, want two tasks", portableBundle["tasks"])
	}
	explicit, ok := tasks[0].(map[string]any)
	if !ok {
		t.Fatalf("first task = %T, want object", tasks[0])
	}
	for _, field := range []string{"labels", "blocked_by_keys"} {
		values, ok := explicit[field].([]any)
		if !ok || len(values) != 0 {
			t.Errorf("explicit %s = %#v, want empty array", field, explicit[field])
		}
	}
	omitted, ok := tasks[1].(map[string]any)
	if !ok {
		t.Fatalf("second task = %T, want object", tasks[1])
	}
	for _, field := range []string{"labels", "blocked_by_keys"} {
		if _, ok := omitted[field]; ok {
			t.Errorf("omitted %s unexpectedly serialized", field)
		}
	}
}

func TestProjectPortabilityImportRejectsMalformedOrAmbiguousInputBeforeRequestOrOutput(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"malformed JSON", "ambiguous paths"} {
		t.Run(name, func(t *testing.T) {
			cmd := newProjectImportTestCmd()
			_ = cmd.Flags().Set("file", path)
			if name == "ambiguous paths" {
				_ = cmd.Flags().Set("dir", t.TempDir())
			}
			out, err := captureStdout(t, func() error { return runProjectImport(cmd, nil) })
			if err == nil {
				t.Fatal("expected input error")
			}
			if out != "" {
				t.Fatalf("stdout = %q, want no partial output", out)
			}
			if got := atomic.LoadInt32(&requests); got != 0 {
				t.Fatalf("requests = %d, want 0 for rejected input", got)
			}
		})
	}
}

func TestProjectPortabilityImportPostsBundleAndPrintsTypedResult(t *testing.T) {
	var requestBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/projects/import" {
			t.Fatalf("request = %s %s, want POST /api/projects/import", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id":      testPortableProjectID,
			"created_count":   2,
			"updated_count":   1,
			"unchanged_count": 0,
			"warnings":        []string{},
		})
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	path := filepath.Join(t.TempDir(), "project.json")
	if err := os.WriteFile(path, portableProjectBundleJSON(t), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newProjectImportTestCmd()
	_ = cmd.Flags().Set("file", path)
	_ = cmd.Flags().Set("title", "Title hint")
	_ = cmd.Flags().Set("dry-run", "true")
	_ = cmd.Flags().Set("output", "json")
	out, err := captureStdout(t, func() error { return runProjectImport(cmd, nil) })
	if err != nil {
		t.Fatalf("run project import: %v", err)
	}
	if requestBody["dry_run"] != true || requestBody["project_title_hint"] != "Title hint" {
		t.Fatalf("request options = %#v", requestBody)
	}
	if _, ok := requestBody["bundle"].(map[string]any); !ok {
		t.Fatalf("request bundle = %T, want object", requestBody["bundle"])
	}
	if _, ok := requestBody["markdown"]; ok {
		t.Fatal("request included both bundle and markdown")
	}
	if !strings.Contains(out, `"project_id"`) || !strings.Contains(out, `"created_count": 2`) {
		t.Fatalf("typed result output = %q", out)
	}
}

func TestProjectPortabilityImportWarnsWhenAttachmentsAreSkipped(t *testing.T) {
	var requestBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(projectbundle.PortabilityResult{Warnings: []string{}})
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	path := filepath.Join(t.TempDir(), "project.json")
	data := []byte(`{
		"schema_version":"multica.project.v1",
		"origin":{},
		"project":{"title":"Portable project"},
		"tasks":[{"key":"TASK-1","title":"Task","attachments":[{"data":"secret"}]}]
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newProjectImportTestCmd()
	_ = cmd.Flags().Set("file", path)
	_ = cmd.Flags().Set("output", "json")
	out, err := captureStdout(t, func() error { return runProjectImport(cmd, nil) })
	if err != nil {
		t.Fatalf("run project import: %v", err)
	}
	if strings.Contains(out, "secret") || !strings.Contains(out, "Attachments are unsupported") {
		t.Fatalf("import output = %q, want skipped-attachment warning without attachment data", out)
	}
	bundle := requestBody["bundle"].(map[string]any)
	tasks := bundle["tasks"].([]any)
	task := tasks[0].(map[string]any)
	if _, exists := task["attachments"]; exists {
		t.Fatal("CLI forwarded unsupported attachment data")
	}
}

func TestProjectPortabilityImportPostsMarkdownWithTitleHint(t *testing.T) {
	var requestBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/projects/import" {
			t.Fatalf("request = %s %s, want POST /api/projects/import", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created_count":   0,
			"updated_count":   0,
			"unchanged_count": 0,
			"warnings":        []string{},
		})
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	markdown := "# New project\n\n- [ ] First task\n"
	path := filepath.Join(t.TempDir(), "TASKS.md")
	if err := os.WriteFile(path, []byte(markdown), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newProjectImportTestCmd()
	_ = cmd.Flags().Set("file", path)
	_ = cmd.Flags().Set("title", "Title hint")
	if _, err := captureStdout(t, func() error { return runProjectImport(cmd, nil) }); err != nil {
		t.Fatalf("run project import: %v", err)
	}
	if requestBody["markdown"] != markdown || requestBody["project_title_hint"] != "Title hint" {
		t.Fatalf("request = %#v, want Markdown and title hint", requestBody)
	}
	if _, ok := requestBody["bundle"]; ok {
		t.Fatal("Markdown request unexpectedly included bundle")
	}
}

func TestProjectPortabilitySyncInfersIDAndPostsDirectoryBundle(t *testing.T) {
	var requestBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/projects/"+testPortableProjectID+"/sync" {
			t.Fatalf("request = %s %s, want sync for origin project", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id":      testPortableProjectID,
			"created_count":   0,
			"updated_count":   0,
			"unchanged_count": 0,
			"warnings":        []string{},
		})
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	dir := t.TempDir()
	metadataDir := filepath.Join(dir, ".multica")
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "project.json"), portableProjectBundleJSON(t), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newProjectSyncTestCmd()
	_ = cmd.Flags().Set("dir", dir)
	_ = cmd.Flags().Set("dry-run", "true")
	_, err := captureStdout(t, func() error { return runProjectSync(cmd, nil) })
	if err != nil {
		t.Fatalf("run project sync: %v", err)
	}
	if requestBody["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", requestBody["dry_run"])
	}
	if _, ok := requestBody["bundle"].(map[string]any); !ok {
		t.Fatalf("request bundle = %T, want object", requestBody["bundle"])
	}
}

func TestProjectPortabilityExportDirectoryWritesPortableFiles(t *testing.T) {
	bundleBytes := portableProjectBundleJSON(t)
	var bundle projectbundle.Bundle
	if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/projects/"+testPortableProjectID+"/export" {
			t.Fatalf("request = %s %s, want project export", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bundle":               bundle,
			"tasks_markdown":       "# Portable project\n",
			"agent_guide_markdown": "# Agent guide\n",
		})
	}))
	defer srv.Close()
	prepareProjectPortabilityCLI(t, srv.URL)

	dir := t.TempDir()
	cmd := newProjectExportTestCmd()
	_ = cmd.Flags().Set("dir", dir)
	if err := runProjectExport(cmd, []string{testPortableProjectID}); err != nil {
		t.Fatalf("run project export: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "TASKS.md"):                   "# Portable project\n",
		filepath.Join(dir, ".multica", "AGENT_GUIDE.md"): "# Agent guide\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	projectJSON, err := os.ReadFile(filepath.Join(dir, ".multica", "project.json"))
	if err != nil {
		t.Fatalf("project.json missing: %v", err)
	}
	if _, err := projectbundle.DecodeBundle(projectJSON); err != nil {
		t.Fatalf("project.json is not a valid bundle: %v", err)
	}
}
