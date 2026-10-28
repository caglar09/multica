package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestProjectBrainWritersPersistEmbeddings(t *testing.T) {
	server, requests, inputs := projectBrainEmbeddingFakeServer(t)
	defer server.Close()
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "true")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY", "test-key")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL", server.URL)

	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Wave 3 brain embedding " + t.Name(),
		"bootstrap": map[string]any{
			"autonomy_mode": "autonomous",
			"brief":         "Keep private project text inside the deployment unless configured.",
			"knowledge": []map[string]any{{
				"kind":    "requirements",
				"title":   "Data handling",
				"content": "Project memory embeddings are explicitly opt-in.",
			}},
		},
	}))
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_brain_entry WHERE project_id=$1::uuid`, created.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_bootstrap WHERE project_id=$1::uuid`, created.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1::uuid`, created.ID)
	})
	var total, embedded int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE embedding IS NOT NULL)
		FROM autonomous_project_brain_entry
		WHERE project_id=$1::uuid AND source_type='bootstrap'
	`, created.ID).Scan(&total, &embedded); err != nil {
		t.Fatalf("count bootstrap brain entries: %v", err)
	}
	if total != 2 || embedded != total {
		t.Fatalf("bootstrap brain entries = %d total, %d embedded; want 2 embedded entries", total, embedded)
	}
	if requests.Load() != int32(total) {
		t.Fatalf("embedding requests = %d, want %d", requests.Load(), total)
	}
	joined := strings.Join(inputs(), "\n")
	for _, want := range []string{"Keep private project text", "Data handling", "explicitly opt-in"} {
		if !strings.Contains(joined, want) {
			t.Errorf("embedding input does not contain %q", want)
		}
	}
}

func TestBrainWritersRemainLocalWhenEmbeddingsDisabled(t *testing.T) {
	server, requests, _ := projectBrainEmbeddingFakeServer(t)
	defer server.Close()
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "false")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY", "")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL", server.URL)

	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Wave 3 disabled brain embedding",
		"bootstrap": map[string]any{
			"autonomy_mode": "autonomous",
			"brief":         "This private text remains local.",
		},
	}))
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_brain_entry WHERE project_id=$1::uuid`, created.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM autonomous_project_bootstrap WHERE project_id=$1::uuid`, created.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE id=$1::uuid`, created.ID)
	})
	var total, embedded int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE embedding IS NOT NULL)
		FROM autonomous_project_brain_entry
		WHERE project_id=$1::uuid AND source_type='bootstrap'
	`, created.ID).Scan(&total, &embedded); err != nil {
		t.Fatalf("count bootstrap brain entries: %v", err)
	}
	if total != 1 || embedded != 0 {
		t.Fatalf("bootstrap brain entries = %d total, %d embedded; want one local-only entry", total, embedded)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("disabled embedding provider received %d requests, want 0", got)
	}
}

func projectBrainEmbeddingFakeServer(t *testing.T) (*httptest.Server, *atomic.Int32, func() []string) {
	t.Helper()
	var requests atomic.Int32
	var mu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			t.Errorf("embedding request = %s %s, want POST /embeddings", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("embedding authorization = %q, want test key", r.Header.Get("Authorization"))
		}
		var input struct {
			Model      string `json:"model"`
			Input      string `json:"input"`
			Dimensions int    `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode embedding request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if input.Model != "text-embedding-3-small" || input.Dimensions != 1536 {
			t.Errorf("embedding model/dimensions = %q/%d", input.Model, input.Dimensions)
		}
		mu.Lock()
		seen = append(seen, input.Input)
		mu.Unlock()
		vector := strings.TrimSuffix(strings.Repeat("0,", 1536), ",")
		_, _ = fmt.Fprintf(w, `{"data":[{"index":0,"embedding":[%s]}]}`, vector)
	}))
	return server, &requests, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}
