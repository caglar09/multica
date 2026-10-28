package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutonomousProjectBrainEmbeddingMigrations(t *testing.T) {
	columnUp := readBrainEmbeddingMigration(t, "603_autonomous_project_brain_embedding.up.sql")
	columnDown := readBrainEmbeddingMigration(t, "603_autonomous_project_brain_embedding.down.sql")
	indexUp := readBrainEmbeddingMigration(t, "604_autonomous_project_brain_embedding_hnsw_index.up.sql")
	indexDown := readBrainEmbeddingMigration(t, "604_autonomous_project_brain_embedding_hnsw_index.down.sql")

	for _, want := range []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		"ALTER TABLE autonomous_project_brain_entry",
		"ADD COLUMN IF NOT EXISTS embedding vector(1536)",
	} {
		if !strings.Contains(strings.ToLower(columnUp), strings.ToLower(want)) {
			t.Errorf("603 up migration is missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(columnUp), "foreign key") {
		t.Error("brain embedding migration must not add a foreign key")
	}
	if !strings.Contains(strings.ToLower(columnDown), "drop column if exists embedding") {
		t.Error("603 down migration does not remove the embedding column")
	}

	indexUpStatements := brainEmbeddingSQLStatements(indexUp)
	if len(indexUpStatements) != 1 {
		t.Fatalf("604 up migration has %d statements, want one CREATE INDEX CONCURRENTLY statement", len(indexUpStatements))
	}
	indexStatement := strings.ToLower(indexUpStatements[0])
	for _, want := range []string{
		"create index concurrently if not exists",
		"using hnsw",
		"embedding vector_cosine_ops",
	} {
		if !strings.Contains(indexStatement, want) {
			t.Errorf("604 up migration is missing %q", want)
		}
	}
	if strings.Contains(indexStatement, "foreign key") {
		t.Error("brain embedding index migration must not add a foreign key")
	}

	indexDownStatements := brainEmbeddingSQLStatements(indexDown)
	if len(indexDownStatements) != 1 || !strings.Contains(strings.ToLower(indexDownStatements[0]), "drop index concurrently if exists") {
		t.Fatalf("604 down migration = %#v, want one reversible DROP INDEX CONCURRENTLY statement", indexDownStatements)
	}
}

func readBrainEmbeddingMigration(t *testing.T, name string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve brain embedding migration test path")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "migrations", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(contents)
}

func brainEmbeddingSQLStatements(sql string) []string {
	var statements []string
	var current strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--") {
			continue
		}
		current.WriteString(line)
		current.WriteByte(' ')
		for {
			statement, rest, found := strings.Cut(current.String(), ";")
			if !found {
				break
			}
			if statement = strings.TrimSpace(statement); statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
			current.WriteString(rest)
		}
	}
	if statement := strings.TrimSpace(current.String()); statement != "" {
		statements = append(statements, statement)
	}
	return statements
}
