package workflowruntime

import "testing"

func TestStructuredTestsPassed(t *testing.T) {
	passed := map[string]any{"result": map[string]any{"tests": []any{
		map[string]any{"name": "unit", "status": "passed"},
		map[string]any{"name": "integration", "status": "PASSED"},
	}}}
	if count, ok := structuredTestsPassed("unit_test", passed); !ok || count != 2 {
		t.Fatalf("structuredTestsPassed() = %d, %v; want 2, true", count, ok)
	}

	failed := map[string]any{"result": map[string]any{"tests": []any{
		map[string]any{"name": "unit", "status": "failed"},
	}}}
	if _, ok := structuredTestsPassed("unit_test", failed); ok {
		t.Fatal("failed test evidence must not pass a deterministic gate")
	}
	nonBlockingFailure := map[string]any{"result": map[string]any{
		"tests":    []any{map[string]any{"name": "baseline", "status": "failed", "evidence": "known upstream failure"}},
		"findings": []any{map[string]any{"blocking": false, "evidence": "known upstream failure"}},
	}}
	if count, ok := structuredTestsPassed("unit_test", nonBlockingFailure); !ok || count != 1 {
		t.Fatalf("explicit non-blocking failure = %d, %v; want 1, true", count, ok)
	}
	legacyMigrationFailure := map[string]any{"result": map[string]any{
		"tests":    []any{map[string]any{"name": "migration numeric-prefix lint", "status": "failed", "evidence": "duplicate 468_comment_deleted_at"}},
		"findings": []any{map[string]any{"blocking": false, "category": "migration", "description": "upstream migration prefix 468 is duplicated"}},
	}}
	if _, ok := structuredTestsPassed("unit_test", legacyMigrationFailure); !ok {
		t.Fatal("legacy non-blocking migration evidence must pass")
	}
	nonBlockingSkip := map[string]any{"result": map[string]any{
		"tests":    []any{map[string]any{"name": "full Go suite", "status": "skipped", "evidence": "unrelated process hung"}},
		"findings": []any{map[string]any{"blocking": false, "category": "test", "description": "The broad Go test suite did not complete."}},
	}}
	if _, ok := structuredTestsPassed("unit_test", nonBlockingSkip); !ok {
		t.Fatal("explicit non-blocking skipped test must pass")
	}
	nonBlockingOutsideChange := map[string]any{"result": map[string]any{
		"tests": []any{map[string]any{"name": "package race test", "status": "failed", "evidence": "Existing race outside this change."}},
	}}
	if _, ok := structuredTestsPassed("unit_test", nonBlockingOutsideChange); !ok {
		t.Fatal("explicit outside-change test failure must pass")
	}
	if _, ok := structuredTestsPassed("unit_test", map[string]any{}); ok {
		t.Fatal("missing test evidence must not pass a deterministic gate")
	}
	if _, ok := structuredTestsPassed("security", passed); ok {
		t.Fatal("structured task claims must not replace a security runner")
	}
}

func TestReviewApproved(t *testing.T) {
	if !reviewApproved(map[string]any{"result": map[string]any{"verdict": "approved"}}) {
		t.Fatal("approved verdict was not recognized")
	}
	if reviewApproved(map[string]any{"result": map[string]any{"verdict": "changes_requested"}}) {
		t.Fatal("changes_requested verdict must not pass review")
	}
}
