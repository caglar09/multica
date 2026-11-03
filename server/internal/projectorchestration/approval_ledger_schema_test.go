package projectorchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApprovalLedgerIDUsesDedicatedConcurrentUniqueIndexMigration(t *testing.T) {
	migrationsDir := filepath.Join("..", "..", "migrations")
	tableMigration, err := os.ReadFile(filepath.Join(migrationsDir, "611_autonomous_project_approval_event.up.sql"))
	if err != nil {
		t.Fatalf("read approval event table migration: %v", err)
	}
	if !strings.Contains(string(tableMigration), "id               UUID NOT NULL DEFAULT gen_random_uuid(),") {
		t.Fatal("approval event id must be non-null and default-generated in the table migration")
	}
	if strings.Contains(strings.ToUpper(string(tableMigration)), "PRIMARY KEY") {
		t.Fatal("approval event table migration must not create an inline primary-key index")
	}

	indexMigration, err := os.ReadFile(filepath.Join(migrationsDir, "618_autonomous_project_approval_event_id.up.sql"))
	if err != nil {
		t.Fatalf("read approval event id index migration: %v", err)
	}
	const wantUp = "CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_autonomous_project_approval_event_id\n    ON autonomous_project_approval_event (id);"
	if strings.TrimSpace(string(indexMigration)) != wantUp {
		t.Fatalf("approval event id index migration = %q, want exactly %q", strings.TrimSpace(string(indexMigration)), wantUp)
	}

	indexRollback, err := os.ReadFile(filepath.Join(migrationsDir, "618_autonomous_project_approval_event_id.down.sql"))
	if err != nil {
		t.Fatalf("read approval event id index rollback: %v", err)
	}
	if strings.TrimSpace(string(indexRollback)) != "DROP INDEX CONCURRENTLY IF EXISTS uq_autonomous_project_approval_event_id;" {
		t.Fatalf("approval event id index rollback = %q, want concurrent index drop", strings.TrimSpace(string(indexRollback)))
	}
}

func TestApprovalLedgerSchema(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL is unset; approval ledger schema test needs a managed database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open approval ledger test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connect approval ledger test database: %v", err)
	}

	var table string
	if err := pool.QueryRow(ctx, "SELECT to_regclass('autonomous_project_approval_event')::text").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table == "" {
		t.Fatal("approval event table missing; apply migrations before running this test")
	}
	var foreignKeys int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'autonomous_project_approval_event'::regclass AND contype = 'f'`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 0 {
		t.Fatalf("approval event table has %d foreign keys, want none", foreignKeys)
	}
	var indexes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema()
		AND tablename = 'autonomous_project_approval_event'
		AND indexname IN (
			'uq_autonomous_project_approval_event_id',
			'uq_autonomous_project_approval_event_idempotency',
			'idx_autonomous_project_approval_event_subject'
		)`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 3 {
		t.Fatalf("approval event unique and lookup indexes = %d, want 3", indexes)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	workspaceID, projectID, actorID := uuid.New(), uuid.New(), uuid.New()
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	insert := `INSERT INTO autonomous_project_approval_event
		(workspace_id, project_id, idempotency_key, subject_type, subject_id, subject_revision,
		 subject_digest, decision, actor_type, actor_id, actor_display, policy_ref, rationale,
		 source_endpoint, origin, origin_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`
	args := []any{workspaceID, projectID, "local-human", "specification_revision", "revision-1", int64(1), digest,
		"approved", "human", actorID, "Project owner", nil, "Reviewed requirements", "/approve", "local", `{}`}
	var eventID uuid.UUID
	if err := tx.QueryRow(ctx, insert+" RETURNING id", args...).Scan(&eventID); err != nil {
		t.Fatalf("insert local human approval: %v", err)
	}
	idInsert := `INSERT INTO autonomous_project_approval_event
		(id, workspace_id, project_id, idempotency_key, subject_type, subject_id, subject_revision,
		 subject_digest, decision, actor_type, actor_id, actor_display, policy_ref, rationale,
		 source_endpoint, origin, origin_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`
	duplicateIDArgs := []any{eventID, workspaceID, projectID, "duplicate-event-id", "specification_revision", "revision-duplicate-id", int64(1), digest,
		"approved", "human", actorID, "Project owner", nil, "Reviewed requirements", "/approve", "local", `{}`}
	assertApprovalLedgerError(t, ctx, tx, "duplicate_event_id", idInsert, duplicateIDArgs, "23505")

	importedArgs := []any{workspaceID, projectID, "imported-human", "plan", "plan-1", int64(2), digest,
		"approved", "human", nil, "Source owner", nil, "Source approval", "/import", "imported", `{"source":"bundle"}`}
	if _, err := tx.Exec(ctx, insert, importedArgs...); err != nil {
		t.Fatalf("preserve imported human approval without local actor id: %v", err)
	}
	systemArgs := []any{workspaceID, projectID, "system-policy", "plan", "plan-2", int64(3), digest,
		"approved", "system", nil, "Policy engine", "low-risk-replan-v1", "Eligible low-risk replan", "/plan", "local", `{}`}
	if _, err := tx.Exec(ctx, insert, systemArgs...); err != nil {
		t.Fatalf("insert policy approval: %v", err)
	}
	manualIssueArgs := []any{workspaceID, projectID, "manual-issue", "manual_issue", uuid.NewString(), nil, digest,
		"approved", "human", actorID, "Project owner", nil, "Human-created project issue", "/issues", "local", `{}`}
	if _, err := tx.Exec(ctx, insert, manualIssueArgs...); err != nil {
		t.Fatalf("insert manual issue provenance: %v", err)
	}

	assertApprovalLedgerError(t, ctx, tx, "invalid_human_actor", insert,
		[]any{workspaceID, projectID, "missing-human-id", "specification_revision", "revision-2", int64(2), digest,
			"approved", "human", nil, "Owner", nil, "", "/approve", "local", `{}`}, "23514")
	assertApprovalLedgerError(t, ctx, tx, "invalid_system_policy", insert,
		[]any{workspaceID, projectID, "missing-policy", "plan", "plan-3", int64(4), digest,
			"approved", "system", nil, "System", nil, "", "/plan", "local", `{}`}, "23514")
	assertApprovalLedgerError(t, ctx, tx, "blank_system_policy", insert,
		[]any{workspaceID, projectID, "blank-policy", "plan", "plan-5", int64(6), digest,
			"approved", "system", nil, "System", "  ", "", "/plan", "local", `{}`}, "23514")
	assertApprovalLedgerError(t, ctx, tx, "imported_actor_id", insert,
		[]any{workspaceID, projectID, "imported-local-id", "plan", "plan-6", int64(7), digest,
			"approved", "human", actorID, "Source owner", nil, "", "/import", "imported", `{}`}, "23514")
	assertApprovalLedgerError(t, ctx, tx, "invalid_digest", insert,
		[]any{workspaceID, projectID, "bad-digest", "plan", "plan-4", int64(5), "bad",
			"approved", "system", nil, "System", "policy:test", "", "/plan", "local", `{}`}, "23514")
	assertApprovalLedgerError(t, ctx, tx, "missing_local_rationale", insert,
		[]any{workspaceID, projectID, "missing-rationale", "plan", "plan-7", int64(8), digest,
			"approved", "system", nil, "System", "policy:test", "  ", "/plan", "local", `{}`}, "23514")
	assertApprovalLedgerError(t, ctx, tx, "duplicate_idempotency", insert,
		args, "23505")
	assertApprovalLedgerError(t, ctx, tx, "update_is_forbidden",
		"UPDATE autonomous_project_approval_event SET rationale='edited' WHERE id=$1", []any{eventID}, "55000")
	assertApprovalLedgerError(t, ctx, tx, "delete_requires_purge",
		"DELETE FROM autonomous_project_approval_event WHERE id=$1", []any{eventID}, "55000")
	assertApprovalLedgerError(t, ctx, tx, "truncate_is_forbidden",
		"TRUNCATE autonomous_project_approval_event", nil, "55000")

	if _, err := tx.Exec(ctx, "SET LOCAL multica.approval_purge = 'on'"); err != nil {
		t.Fatalf("enable transaction-local approval purge: %v", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM autonomous_project_approval_event WHERE id=$1", eventID); err != nil {
		t.Fatalf("delete approval event during explicit purge: %v", err)
	}
	t.Log("APPROVAL_LEDGER_SCHEMA_GATE_PASS")
}

func assertApprovalLedgerError(t *testing.T, ctx context.Context, tx pgx.Tx, name, query string, args []any, code string) {
	t.Helper()
	if _, err := tx.Exec(ctx, "SAVEPOINT "+name); err != nil {
		t.Fatalf("%s savepoint: %v", name, err)
	}
	_, err := tx.Exec(ctx, query, args...)
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", name)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("%s error = %v, want PostgreSQL code %s", name, err, code)
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); err != nil {
		t.Fatalf("%s rollback to savepoint: %v", name, err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		t.Fatalf("%s release savepoint: %v", name, err)
	}
}
