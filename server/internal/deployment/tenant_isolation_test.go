package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type deploymentQueryRecorder struct{ queries []string }

func (r *deploymentQueryRecorder) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	r.queries = append(r.queries, query)
	return deploymentNoRows{}
}
func (r *deploymentQueryRecorder) Query(_ context.Context, query string, _ ...interface{}) (pgx.Rows, error) {
	r.queries = append(r.queries, query)
	return nil, errors.New("query intentionally not executed")
}
func (r *deploymentQueryRecorder) Exec(_ context.Context, query string, _ ...interface{}) (pgconn.CommandTag, error) {
	r.queries = append(r.queries, query)
	return pgconn.CommandTag{}, nil
}

type deploymentNoRows struct{}

func (deploymentNoRows) Scan(...interface{}) error                    { return pgx.ErrNoRows }
func (deploymentNoRows) RawValues() [][]byte                          { return nil }
func (deploymentNoRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func TestRepositoryQueriesRequireWorkspaceScope(t *testing.T) {
	t.Parallel()
	r := &deploymentQueryRecorder{}
	repo := NewPostgresRepository(r)
	ctx := context.Background()

	_, _ = repo.GetProviderConnection(ctx, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")
	_, _ = repo.GetTarget(ctx, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")
	_, _ = repo.GetOperation(ctx, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")
	_, _ = repo.ListProviderConnections(ctx, "00000000-0000-0000-0000-000000000001")
	_, _ = repo.ListTargets(ctx, "00000000-0000-0000-0000-000000000001")
	_, _ = repo.ListOperations(ctx, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")
	_, _ = repo.ListEvents(ctx, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")

	if len(r.queries) != 7 {
		t.Fatalf("recorded %d queries, want 7", len(r.queries))
	}
	for _, query := range r.queries {
		if !strings.Contains(query, "workspace_id = $1") {
			t.Errorf("query has no workspace predicate: %s", query)
		}
	}
}
