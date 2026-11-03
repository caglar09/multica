package projectorchestration

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApprovalLedgerWriter(t *testing.T) {
	assertApprovalLedgerHasOneWriter(t)

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL is unset; approval writer test needs a managed database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open approval writer test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connect approval writer test database: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	workspaceID, projectID, actorID := uuidToApprovalPGType(uuid.New()), uuidToApprovalPGType(uuid.New()), uuidToApprovalPGType(uuid.New())
	event := ApprovalEvent{
		WorkspaceID:     workspaceID,
		ProjectID:       projectID,
		IdempotencyKey:  "approval-writer-test",
		SubjectType:     "specification_revision",
		SubjectID:       uuid.NewString(),
		SubjectRevision: pgtype.Int8{Int64: 1, Valid: true},
		SubjectDigest:   strings.Repeat("a", 64),
		Decision:        "approved",
		ActorType:       "human",
		ActorID:         actorID,
		ActorDisplay:    "Ledger test",
		Rationale:       "Test approval idempotency",
		SourceEndpoint:  "/test/approval",
		Origin:          "local",
		OriginRef:       []byte(`{"test":true}`),
	}
	if err := RecordApprovalTx(ctx, tx, event); err != nil {
		t.Fatalf("record initial approval: %v", err)
	}
	if err := RecordApprovalTx(ctx, tx, event); err != nil {
		t.Fatalf("repeat identical approval: %v", err)
	}
	changed := event
	changed.Rationale = "Different approval payload"
	if err := RecordApprovalTx(ctx, tx, changed); err == nil {
		t.Fatal("reusing an idempotency key with a different payload succeeded")
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM autonomous_project_approval_event
		WHERE workspace_id=$1 AND project_id=$2 AND idempotency_key=$3`,
		workspaceID, projectID, event.IdempotencyKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("approval event count = %d, want 1", count)
	}
	t.Log("APPROVAL_LEDGER_WRITER_GATE_PASS")
}

func assertApprovalLedgerHasOneWriter(t *testing.T) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve approval writer test source path")
	}
	serverRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	insertPattern := regexp.MustCompile(`(?is)\binsert\s+into\s+"?autonomous_project_approval_event"?\b`)
	var writers []string
	err := filepath.WalkDir(serverRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".")) {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && insertPattern.MatchString(value) {
				writers = append(writers, path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan Go sources for approval ledger writers: %v", err)
	}
	if len(writers) != 1 || filepath.Clean(writers[0]) != filepath.Clean(filepath.Join(serverRoot, "internal", "projectorchestration", "approval_ledger.go")) {
		t.Fatalf("approval ledger INSERT writers = %v, want only approval_ledger.go", writers)
	}
}

func uuidToApprovalPGType(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}
