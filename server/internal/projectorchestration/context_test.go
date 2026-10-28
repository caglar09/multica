package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestContextCompiler(t *testing.T) {
	t.Run("preserves authored summary and hierarchical details with dynamic budget", func(t *testing.T) {
		goal := "Build reliable project context"
		authoredSummary := "Keep project constraints visible to every worker."
		longNonGoal := strings.Repeat("x", 2400)
		specification := marshalSpecification(t, map[string]any{
			"summary": authoredSummary,
			"requirements": []any{
				map[string]any{"id": "REQ-17", "text": "Retain requirement identity."},
			},
			"constraints": []string{"Do not discard safety limits."},
			"non_goals":   []string{longNonGoal},
		})
		specificationItems, err := authoritativeSpecificationItems(goal, specification, "plan-1")
		if err != nil {
			t.Fatalf("authoritativeSpecificationItems() error = %v", err)
		}

		items := testContextItems(specificationItems, `{"autonomy":"assisted"}`)
		pkg := NewBoundedContext("implementation", NodeImplementation, "node-1", 1, 1, "", items)
		if err := validateAuthoritativeContext(pkg); err != nil {
			t.Fatalf("validateAuthoritativeContext() error = %v", err)
		}

		specificationSection := findContextSection(t, pkg, "specification")
		if specificationSection.TokenBudget <= DefaultContextSectionBudgets["specification"] {
			t.Fatalf("specification budget = %d, want more than base budget %d", specificationSection.TokenBudget, DefaultContextSectionBudgets["specification"])
		}
		allocatedBudget := 0
		for _, section := range pkg.Sections {
			allocatedBudget += section.TokenBudget
		}
		if allocatedBudget != pkg.TotalTokenBudget {
			t.Fatalf("sum of section budgets = %d, want total budget %d", allocatedBudget, pkg.TotalTokenBudget)
		}
		if specificationSection.Omitted != 0 {
			t.Fatalf("specification omitted %d items, want 0", specificationSection.Omitted)
		}

		var rendered []string
		refs := map[string]bool{}
		for _, item := range specificationSection.Items {
			rendered = append(rendered, item.Text)
			refs[item.Ref] = true
		}
		joined := strings.Join(rendered, "\n")
		if !strings.Contains(joined, "Goal: "+goal) || !strings.Contains(joined, "Summary: "+authoredSummary) {
			t.Fatalf("compiled specification did not preserve the goal and authored summary: %q", joined)
		}
		if !strings.Contains(joined, longNonGoal) {
			t.Fatal("compiled specification did not preserve the complete non-goal")
		}
		if !strings.Contains(joined, "Retain requirement identity.") || !strings.Contains(joined, "Do not discard safety limits.") {
			t.Fatal("compiled specification did not preserve requirement and constraint details")
		}
		for _, ref := range []string{"plan-1:requirements[REQ-17]", "plan-1:constraints[0]", "plan-1:non_goals[0]"} {
			if !refs[ref] {
				t.Errorf("compiled specification is missing hierarchical reference %q", ref)
			}
		}
	})

	t.Run("fails closed when an oversized specification cannot fit", func(t *testing.T) {
		specification := marshalSpecification(t, map[string]any{
			"summary":   "Preserve this authored summary.",
			"non_goals": []string{strings.Repeat("x", 2400)},
		})
		specificationItems, err := authoritativeSpecificationItems("Retain all project scope", specification, "plan-2")
		if err != nil {
			t.Fatalf("authoritativeSpecificationItems() error = %v", err)
		}

		items := testContextItems(specificationItems, `{"autonomy":"assisted"}`)
		for _, name := range []string{"brain", "predecessor_artifacts", "structured_handoffs", "unresolved_findings", "repository_facts"} {
			items[name] = []ContextItem{{Source: "test", Text: strings.Repeat("x", DefaultContextSectionBudgets[name]*4)}}
		}
		pkg := NewBoundedContext("implementation", NodeImplementation, "node-2", 1, 1, "", items)
		if got := findContextSection(t, pkg, "specification").TokenBudget; got != DefaultContextSectionBudgets["specification"] {
			t.Fatalf("specification budget = %d, want %d when other sections use their reserves", got, DefaultContextSectionBudgets["specification"])
		}
		if err := validateAuthoritativeContext(pkg); !errors.Is(err, ErrAuthoritativeContextTruncated) {
			t.Fatalf("validateAuthoritativeContext() error = %v, want ErrAuthoritativeContextTruncated", err)
		}
	})

	t.Run("fails closed when policy is truncated", func(t *testing.T) {
		specificationItems, err := authoritativeSpecificationItems("A valid project goal", []byte(`{"summary":"Valid summary"}`), "plan-3")
		if err != nil {
			t.Fatalf("authoritativeSpecificationItems() error = %v", err)
		}
		items := testContextItems(specificationItems, strings.Repeat("p", (DefaultContextSectionBudgets["policy"]+1)*4))
		pkg := NewBoundedContext("implementation", NodeImplementation, "node-3", 1, 1, "", items)
		if err := validateAuthoritativeContext(pkg); !errors.Is(err, ErrAuthoritativeContextTruncated) {
			t.Fatalf("validateAuthoritativeContext() error = %v, want ErrAuthoritativeContextTruncated", err)
		}
	})

	t.Run("fails closed when a required section has no retained content", func(t *testing.T) {
		specificationItems, err := authoritativeSpecificationItems("A valid project goal", []byte(`{"summary":"Valid summary"}`), "plan-4")
		if err != nil {
			t.Fatalf("authoritativeSpecificationItems() error = %v", err)
		}
		pkg := NewBoundedContext("implementation", NodeImplementation, "node-4", 1, 1, "", testContextItems(specificationItems, ""))
		if err := validateAuthoritativeContext(pkg); !errors.Is(err, ErrAuthoritativeContextTruncated) {
			t.Fatalf("validateAuthoritativeContext() error = %v, want ErrAuthoritativeContextTruncated", err)
		}
	})

	t.Run("rejects empty, malformed, and excessively deep specifications", func(t *testing.T) {
		for name, raw := range map[string][]byte{
			"empty":     nil,
			"null":      []byte(`null`),
			"malformed": []byte(`{"summary":`),
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := authoritativeSpecificationItems("", raw, "plan-5"); err == nil {
					t.Fatal("authoritativeSpecificationItems() error = nil, want an explicit failure")
				}
			})
		}

		var nested any = "leaf"
		for range 66 {
			nested = map[string]any{"child": nested}
		}
		deepSpecification := marshalSpecification(t, map[string]any{"goal": "Keep nested detail", "detail": nested})
		if _, err := authoritativeSpecificationItems("", deepSpecification, "plan-6"); !errors.Is(err, ErrAuthoritativeContextTruncated) {
			t.Fatalf("deep specification error = %v, want ErrAuthoritativeContextTruncated", err)
		}
		if _, err := authoritativeSpecificationItems("", []byte(`{"requirements":[]}`), "plan-7"); !errors.Is(err, ErrAuthoritativeContextTruncated) {
			t.Fatalf("specification without a goal or summary error = %v, want ErrAuthoritativeContextTruncated", err)
		}
	})

	t.Run("returns the error before durable context or handoff writes", func(t *testing.T) {
		tx := &contextCompilerTestTx{}
		issue := db.Issue{
			ID:          testContextUUID(1),
			WorkspaceID: testContextUUID(2),
			ProjectID:   testContextUUID(3),
		}
		_, err := CompileWorkflowContext(context.Background(), tx, issue, testContextUUID(4), testContextUUID(5), "implementation")
		if !errors.Is(err, ErrAuthoritativeContextTruncated) {
			t.Fatalf("CompileWorkflowContext() error = %v, want ErrAuthoritativeContextTruncated", err)
		}
		if len(tx.execStatements) != 0 {
			t.Fatalf("unexpected writes before failed compilation: %q", tx.execStatements)
		}
		if tx.queryRowCount != 2 {
			t.Fatalf("QueryRow calls before failure = %d, want 2 and no context compilation or snapshot queries", tx.queryRowCount)
		}
	})

	t.Run("rejects truncated durable context before handoff attachment", func(t *testing.T) {
		persisted := ContextPackage{Sections: []ContextSection{
			{Name: "specification", Omitted: 1},
			{Name: "policy", Items: []ContextItem{{Text: "policy"}}, UsedTokens: 2},
		}}
		tx := &contextCompilerTestTx{existingContext: marshalSpecification(t, persisted)}
		issue := db.Issue{ProjectID: testContextUUID(3)}
		_, err := CompileWorkflowContext(context.Background(), tx, issue, testContextUUID(4), testContextUUID(5), "implementation")
		if !errors.Is(err, ErrAuthoritativeContextTruncated) {
			t.Fatalf("CompileWorkflowContext() error = %v, want ErrAuthoritativeContextTruncated", err)
		}
		if len(tx.execStatements) != 0 {
			t.Fatalf("truncated durable context was attached to a handoff: %q", tx.execStatements)
		}
	})

	t.Run("rejects over-budget durable context before handoff attachment", func(t *testing.T) {
		persisted := ContextPackage{
			TotalTokenBudget: 1,
			UsedTokens:       2,
			Sections: []ContextSection{
				{Name: "specification", Items: []ContextItem{{Text: "goal"}}, UsedTokens: 1},
				{Name: "policy", Items: []ContextItem{{Text: "policy"}}, UsedTokens: 1},
			},
		}
		tx := &contextCompilerTestTx{existingContext: marshalSpecification(t, persisted)}
		issue := db.Issue{ProjectID: testContextUUID(3)}
		_, err := CompileWorkflowContext(context.Background(), tx, issue, testContextUUID(4), testContextUUID(5), "implementation")
		if err == nil || !strings.Contains(err.Error(), "hard token budget") {
			t.Fatalf("CompileWorkflowContext() error = %v, want hard token budget error", err)
		}
		if len(tx.execStatements) != 0 {
			t.Fatalf("over-budget durable context was attached to a handoff: %q", tx.execStatements)
		}
	})

	t.Run("reuses complete durable context without recompiling", func(t *testing.T) {
		specificationItems, err := authoritativeSpecificationItems("A durable project goal", []byte(`{"summary":"Keep the persisted prompt stable."}`), "plan-8")
		if err != nil {
			t.Fatalf("authoritativeSpecificationItems() error = %v", err)
		}
		want := NewBoundedContext("implementation", NodeImplementation, "node-8", 8, 9, "revision-8", testContextItems(specificationItems, `{"autonomy":"assisted"}`))
		tx := &contextCompilerTestTx{existingContext: marshalSpecification(t, want)}
		issue := db.Issue{ProjectID: testContextUUID(3)}
		got, err := CompileWorkflowContext(context.Background(), tx, issue, testContextUUID(4), testContextUUID(5), "implementation")
		if err != nil {
			t.Fatalf("CompileWorkflowContext() error = %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("replayed context = %#v, want exact persisted package %#v", got, want)
		}
		if len(tx.execStatements) != 1 || !strings.Contains(tx.execStatements[0], "UPDATE autonomous_project_handoff") {
			t.Fatalf("valid durable context was not reattached exactly once: %q", tx.execStatements)
		}
	})
}

func marshalSpecification(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func testContextItems(specification []ContextItem, policy string) map[string][]ContextItem {
	items := map[string][]ContextItem{
		"specification":         specification,
		"policy":                {},
		"brain":                 {},
		"predecessor_artifacts": {},
		"structured_handoffs":   {},
		"unresolved_findings":   {},
		"repository_facts":      {},
	}
	if policy != "" {
		items["policy"] = []ContextItem{{Source: "project_policy", Text: policy}}
	}
	return items
}

func findContextSection(t *testing.T, pkg ContextPackage, name string) ContextSection {
	t.Helper()
	for _, section := range pkg.Sections {
		if section.Name == name {
			return section
		}
	}
	t.Fatalf("context package is missing %q section", name)
	return ContextSection{}
}

func testContextUUID(value byte) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte{value}, Valid: true}
}

func TestRecordTaskCommitPersistsObservedRevisionPendingQuality(t *testing.T) {
	tx := &contextCompilerTestTx{}
	commit := TaskCommit{
		WorkspaceID: testContextUUID(1), ProjectID: testContextUUID(2), PlanID: testContextUUID(3),
		NodeID: testContextUUID(4), IssueID: testContextUUID(5), TaskID: testContextUUID(6), AgentID: testContextUUID(7),
		BaseSHA: "base-sha", CommitSHA: "commit-sha", BranchName: "task/issue-1",
		Worktree: "/repo/.worktrees/task-1", RepoPath: "/repo", ChangedFiles: []string{"server/main.go"},
	}
	if err := RecordTaskCommit(context.Background(), tx, commit); err != nil {
		t.Fatalf("RecordTaskCommit() error = %v", err)
	}
	if len(tx.execStatements) != 1 || !strings.Contains(tx.execStatements[0], "INSERT INTO autonomous_project_change_set") ||
		!strings.Contains(tx.execStatements[0], "'changes_requested',NULL") ||
		!strings.Contains(tx.execStatements[0], "ON CONFLICT (source_task_id) DO NOTHING") {
		t.Fatalf("change-set insert = %q, want idempotent changes_requested insert", tx.execStatements)
	}
	if got := tx.execArgs[0][12]; got != "commit-sha" {
		t.Fatalf("persisted commit SHA argument = %#v, want commit-sha", got)
	}
	if got, ok := tx.execArgs[0][11].([]byte); !ok || string(got) != `["server/main.go"]` {
		t.Fatalf("persisted changed-file argument = %#v, want encoded server/main.go", tx.execArgs[0][11])
	}
}

func TestCurrentContextRepositoryRevisionUsesRecordedCommitBeforeQualityReady(t *testing.T) {
	tx := &contextCompilerTestTx{repositoryRevision: "commit-sha"}
	got, err := currentContextRepositoryRevision(context.Background(), tx, testContextUUID(1), testContextUUID(2))
	if err != nil {
		t.Fatalf("currentContextRepositoryRevision() error = %v", err)
	}
	if got != "commit-sha" {
		t.Fatalf("repository revision = %q, want the recorded commit SHA", got)
	}
	if len(tx.queryRowStatements) != 1 || !strings.Contains(tx.queryRowStatements[0], "'ready','changes_requested'") || !strings.Contains(tx.queryRowStatements[0], "ORDER BY created_at DESC, id DESC") {
		t.Fatalf("repository revision query does not include pre-quality change sets: %q", tx.queryRowStatements)
	}
}

type contextCompilerTestTx struct {
	pgx.Tx
	existingContext    []byte
	repositoryRevision string
	execStatements     []string
	execArgs           [][]any
	queryRowStatements []string
	queryRowCount      int
}

func (tx *contextCompilerTestTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	tx.queryRowCount++
	tx.queryRowStatements = append(tx.queryRowStatements, query)
	switch {
	case strings.Contains(query, "SELECT context_package"):
		if tx.existingContext != nil {
			return contextCompilerTestRow{scan: func(dest ...any) error {
				*dest[0].(*[]byte) = append([]byte(nil), tx.existingContext...)
				return nil
			}}
		}
		return contextCompilerTestRow{err: pgx.ErrNoRows}
	case strings.Contains(query, "FROM autonomous_project_plan p"):
		return contextCompilerTestRow{scan: func(dest ...any) error {
			*dest[0].(*pgtype.UUID) = testContextUUID(6)
			*dest[1].(*int64) = 1
			*dest[2].(*string) = "A project with a missing specification"
			*dest[3].(*[]byte) = nil
			*dest[4].(*[]byte) = []byte(`{"autonomy":"assisted"}`)
			*dest[5].(*pgtype.UUID) = pgtype.UUID{}
			for _, value := range dest[6:] {
				*value.(*pgtype.Text) = pgtype.Text{}
			}
			return nil
		}}
	case strings.Contains(query, "FROM autonomous_project_change_set"):
		return contextCompilerTestRow{scan: func(dest ...any) error {
			*dest[0].(*string) = tx.repositoryRevision
			return nil
		}}
	case strings.Contains(query, "FROM autonomous_project_brain_state"):
		return contextCompilerTestRow{err: pgx.ErrNoRows}
	default:
		return contextCompilerTestRow{err: fmt.Errorf("unexpected context compiler query: %s", strings.TrimSpace(query))}
	}
}

func (tx *contextCompilerTestTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tx.execStatements = append(tx.execStatements, query)
	tx.execArgs = append(tx.execArgs, append([]any(nil), args...))
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

func (tx *contextCompilerTestTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected context compiler row query")
}

type contextCompilerTestRow struct {
	err  error
	scan func(...any) error
}

func (row contextCompilerTestRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	return row.scan(dest...)
}
