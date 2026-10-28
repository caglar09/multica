package workflowruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

func TestProjectBrainWritersPersistEmbeddings(t *testing.T) {
	server, requests, inputs := workflowBrainEmbeddingFakeServer(t)
	defer server.Close()
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "true")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY", "test-key")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL", server.URL)

	fixture := newSynchronousBrainFixture(t)
	runtime := &Runtime{
		pool: fixture.pool,
		repositoryAnalyzer: workflowBrainRepositoryAnalyzer{snapshot: projectorchestration.RepositorySnapshot{
			Revision: "embedding-wave3-revision",
			Modules:  []string{"internal/workflowruntime"},
			Evidence: map[string]any{"summary": "repository-snapshot-embedding-evidence"},
		}},
	}
	if err := runtime.refreshRepositoryIntelligence(fixture.ctx, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("persist repository intelligence: %v", err)
	}
	prepareWorkflowTaskArtifactFixture(t, fixture)
	if _, err := runtime.recordProjectTaskArtifact(fixture.ctx, fixture.task, fixture.predecessorIssue); err != nil {
		t.Fatalf("persist task artifact brain entry: %v", err)
	}

	var total, embedded int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*), count(*) FILTER (WHERE embedding IS NOT NULL AND vector_dims(embedding)=1536)
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2
		  AND source_type IN ('repository_analyzer','artifact')
	`, fixture.workspaceID, fixture.projectID).Scan(&total, &embedded); err != nil {
		t.Fatalf("count repository and task artifact brain embeddings: %v", err)
	}
	if total != 2 || embedded != 2 {
		t.Fatalf("repository/task-artifact brain entries = %d total, %d embedded; want 2/2", total, embedded)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("fake embedding requests = %d, want one for each repository/task-artifact entry", got)
	}
	joined := strings.Join(inputs(), "\n")
	for _, want := range []string{"Repository snapshot", "repository-snapshot-embedding-evidence", "Task artifact embedding observation"} {
		if !strings.Contains(joined, want) {
			t.Errorf("embedding inputs do not contain %q", want)
		}
	}
}

func TestBrainWritersRemainLocalWhenEmbeddingsDisabled(t *testing.T) {
	requests := disableBrainEmbeddingRequests(t)
	fixture := newSynchronousBrainFixture(t)
	runtime := &Runtime{
		pool: fixture.pool,
		repositoryAnalyzer: workflowBrainRepositoryAnalyzer{snapshot: projectorchestration.RepositorySnapshot{
			Revision: "local-only-wave3-revision",
			Modules:  []string{"internal/workflowruntime"},
			Evidence: map[string]any{"summary": "local-only-repository-evidence"},
		}},
	}
	if err := runtime.refreshRepositoryIntelligence(fixture.ctx, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("persist local repository intelligence: %v", err)
	}
	prepareWorkflowTaskArtifactFixture(t, fixture)
	if _, err := runtime.recordProjectTaskArtifact(fixture.ctx, fixture.task, fixture.predecessorIssue); err != nil {
		t.Fatalf("persist local task artifact brain entry: %v", err)
	}

	var total, embedded int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*), count(*) FILTER (WHERE embedding IS NOT NULL)
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2
		  AND source_type IN ('repository_analyzer','artifact')
	`, fixture.workspaceID, fixture.projectID).Scan(&total, &embedded); err != nil {
		t.Fatalf("count local repository and task artifact brain entries: %v", err)
	}
	if total != 2 || embedded != 0 {
		t.Fatalf("local repository/task-artifact brain entries = %d total, %d embedded; want 2/0", total, embedded)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("disabled embedding provider received %d requests, want 0", got)
	}
}

func prepareWorkflowTaskArtifactFixture(t *testing.T, fixture *synchronousBrainFixture) {
	t.Helper()
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_plan_node
		SET kind='research', title='Task artifact embedding observation'
		WHERE id=$1
	`, fixture.predecessorID); err != nil {
		t.Fatalf("set research node for task artifact fixture: %v", err)
	}
	fixture.task.Result = []byte(`{"observation":"Task artifact embedding observation: preserve the PostgreSQL retrieval contract."}`)
}

type workflowBrainRepositoryAnalyzer struct {
	snapshot projectorchestration.RepositorySnapshot
}

func (a workflowBrainRepositoryAnalyzer) Snapshot(context.Context, pgtype.UUID, pgtype.UUID) (projectorchestration.RepositorySnapshot, error) {
	return a.snapshot, nil
}

func (workflowBrainRepositoryAnalyzer) Impact(context.Context, pgtype.UUID, pgtype.UUID, projectorchestration.ChangeImpactRequest) (projectorchestration.ChangeImpactEvidence, error) {
	return projectorchestration.ChangeImpactEvidence{}, nil
}

func workflowBrainEmbeddingFakeServer(t *testing.T) (*httptest.Server, *atomic.Int64, func() []string) {
	t.Helper()
	var requests atomic.Int64
	var seenMu sync.Mutex
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
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if input.Model != "text-embedding-3-small" || input.Dimensions != 1536 {
			t.Errorf("embedding model/dimensions = %q/%d", input.Model, input.Dimensions)
		}
		seenMu.Lock()
		seen = append(seen, input.Input)
		seenMu.Unlock()
		vector := make([]float64, 1536)
		vector[0] = 1
		if err := json.NewEncoder(w).Encode(map[string]any{
			"data": []any{map[string]any{"index": 0, "embedding": vector}},
		}); err != nil {
			t.Errorf("write fake embedding response: %v", err)
		}
	}))
	return server, &requests, func() []string {
		seenMu.Lock()
		defer seenMu.Unlock()
		return append([]string(nil), seen...)
	}
}
