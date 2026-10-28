package projectorchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestBrainRecall_SemanticMatchWithoutExactKeywords(t *testing.T) {
	fake := &fakeMemoryEmbedder{vector: memoryTestVector()}
	rows := &fakeMemoryRows{values: []any{
		pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		"jwt-login-flow", "architecture_decision", "JWT login flow", `{"summary":"Refresh tokens rotate securely"}`,
		0.9, 0.8, "authoritative_spec", []byte(`[]`), time.Now(), int64(4), 1.0 / 61.0,
	}}
	queryer := &fakeMemoryQueryer{rows: rows}
	memories, err := queryHybridBrainMemories(context.Background(), queryer,
		pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
		RecallOptions{Query: "user authentication", Limit: 8}, fake)
	if err != nil {
		t.Fatalf("queryHybridBrainMemories() error = %v", err)
	}
	if len(memories) != 1 || memories[0].CanonicalKey != "jwt-login-flow" {
		t.Fatalf("semantic-only result = %#v, want the JWT login memory", memories)
	}
	if len(fake.queries) != 1 || fake.queries[0] != "user authentication" {
		t.Fatalf("embedding queries = %#v, want the query without requiring matching keywords", fake.queries)
	}
	if !strings.Contains(queryer.query, "embedding <=> $8::vector") ||
		!strings.Contains(queryer.query, "UNION ALL") ||
		!strings.Contains(queryer.query, "sum(1.0 / (60.0 + rank::float8))") {
		t.Fatalf("hybrid SQL is missing cosine retrieval or RRF fusion: %s", queryer.query)
	}
	if got, ok := queryer.args[6].(bool); !ok || !got {
		t.Fatalf("semantic search enabled argument = %#v, want true", queryer.args[6])
	}
	if got, ok := queryer.args[7].(string); !ok || !strings.HasPrefix(got, "[") {
		t.Fatalf("query vector argument = %#v, want a pgvector literal", queryer.args[7])
	}
}

func TestBrainRecall_EnrichesQueryWithDescription(t *testing.T) {
	query := buildContextMemoryQuery(
		"Fix bug", "Fix bug", "A generic task title needs detail.",
		"The bug concerns short-lived JWT refresh token rotation.",
		[]byte(`[{"id":"AC-1","text":"Reject a reused refresh token."}]`),
		[]byte(`[{"text":"Keep token rotation atomic."}]`),
	)
	for _, want := range []string{
		"Fix bug",
		"A generic task title needs detail.",
		"short-lived JWT refresh token rotation.",
		"Reject a reused refresh token.",
		"Keep token rotation atomic.",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("recall query %q does not include %q", query, want)
		}
	}
	if strings.Count(query, "Fix bug") != 1 {
		t.Fatalf("duplicate issue and plan title was not collapsed: %q", query)
	}
}

func TestBrainRecall_NoProviderDoesNotSendRequests(t *testing.T) {
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "false")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY", "")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL", "")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if _, err := newMemoryEmbedder(MemoryEmbeddingConfig{
		Enabled: true, BaseURL: server.URL,
	}, server.Client()); err == nil {
		t.Fatal("enabled embeddings without an API key must fail closed")
	}

	fake := &fakeMemoryEmbedder{vector: memoryTestVector()}
	embedder, err := memoryEmbedderFromEnvironment()
	if err != nil {
		t.Fatalf("memoryEmbedderFromEnvironment() error = %v", err)
	}
	if embedder != nil {
		t.Fatal("unconfigured embedding provider must remain disabled")
	}
	if _, err := embedMemoryText(context.Background(), embedder, "private project details"); err != nil {
		t.Fatalf("embedMemoryText() error = %v", err)
	}
	if literal, err := PrepareBrainMemoryEmbedding(context.Background(), "private title", []byte(`{"text":"private project details"}`)); err != nil || literal != "" {
		t.Fatalf("unconfigured PrepareBrainMemoryEmbedding() = %q, %v; want no vector and no error", literal, err)
	}
	configured, err := memoryEmbedderForConfig(MemoryEmbeddingConfig{BaseURL: server.URL}, fake)
	if err != nil || configured != nil {
		t.Fatalf("unconfigured config accepted injected embedder: embedder=%#v error=%v", configured, err)
	}
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "true")
	if _, err := memoryEmbedderFromEnvironment(); err == nil {
		t.Fatal("enabled embeddings without an API key must fail closed")
	}
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "false")
	if len(fake.queries) != 0 {
		t.Fatalf("unconfigured provider called injected embedder with %q", fake.queries)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unconfigured provider made %d HTTP requests, want none", got)
	}
}

func TestBrainRecall_EmptyQueryDoesNotReturnFallback(t *testing.T) {
	fake := &fakeMemoryEmbedder{vector: memoryTestVector()}
	queryer := &fakeMemoryQueryer{}
	memories, err := queryHybridBrainMemories(context.Background(), queryer,
		pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
		RecallOptions{Query: "  ", Limit: 8}, fake)
	if err != nil {
		t.Fatalf("queryHybridBrainMemories() error = %v", err)
	}
	if len(memories) != 0 || queryer.query != "" || len(fake.queries) != 0 {
		t.Fatalf("empty query returned an unrelated fallback: memories=%#v query=%q embeddings=%#v", memories, queryer.query, fake.queries)
	}
}

func TestOpenAIEmbeddingAdapterUsesConfiguredModelWithFakeServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Errorf("request = %s %s, want POST /v1/embeddings", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		var request struct {
			Model      string `json:"model"`
			Input      string `json:"input"`
			Dimensions int    `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != defaultMemoryEmbeddingModel || request.Dimensions != defaultMemoryEmbeddingDimensions || request.Input != "test memory" {
			t.Errorf("embedding request = %#v", request)
		}
		vector := strings.TrimSuffix(strings.Repeat("0,", defaultMemoryEmbeddingDimensions), ",")
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[` + vector + `]}]}`))
	}))
	defer server.Close()

	embedder, err := newMemoryEmbedder(MemoryEmbeddingConfig{
		Enabled: true, APIKey: "test-key", BaseURL: server.URL + "/v1",
	}, server.Client())
	if err != nil {
		t.Fatalf("newMemoryEmbedder() error = %v", err)
	}
	vector, err := embedder.Embed(context.Background(), "test memory")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vector) != defaultMemoryEmbeddingDimensions {
		t.Fatalf("embedding dimensions = %d, want %d", len(vector), defaultMemoryEmbeddingDimensions)
	}
}

type fakeMemoryEmbedder struct {
	vector  []float32
	queries []string
}

func (f *fakeMemoryEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	f.queries = append(f.queries, text)
	return append([]float32(nil), f.vector...), nil
}

func memoryTestVector() []float32 {
	vector := make([]float32, defaultMemoryEmbeddingDimensions)
	vector[0] = 1
	return vector
}

type fakeMemoryQueryer struct {
	query string
	args  []any
	rows  pgx.Rows
}

func (q *fakeMemoryQueryer) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	q.query = query
	q.args = append([]any(nil), args...)
	return q.rows, nil
}

type fakeMemoryRows struct {
	values []any
	done   bool
}

func (r *fakeMemoryRows) Close()                                       {}
func (r *fakeMemoryRows) Err() error                                   { return nil }
func (r *fakeMemoryRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeMemoryRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeMemoryRows) Next() bool {
	if r.done || r.values == nil {
		return false
	}
	r.done = true
	return true
}
func (r *fakeMemoryRows) Scan(dest ...any) error {
	if len(dest) != len(r.values) {
		return pgx.ErrNoRows
	}
	for i, value := range r.values {
		switch target := dest[i].(type) {
		case *pgtype.UUID:
			*target = value.(pgtype.UUID)
		case *string:
			*target = value.(string)
		case *float64:
			*target = value.(float64)
		case *[]byte:
			*target = append([]byte(nil), value.([]byte)...)
		case *time.Time:
			*target = value.(time.Time)
		case *int64:
			*target = value.(int64)
		default:
			return pgx.ErrNoRows
		}
	}
	return nil
}
func (r *fakeMemoryRows) Values() ([]any, error) { return append([]any(nil), r.values...), nil }
func (r *fakeMemoryRows) RawValues() [][]byte    { return nil }
func (r *fakeMemoryRows) Conn() *pgx.Conn        { return nil }
func (r *fakeMemoryRows) TypeMap() *pgtype.Map   { return nil }
