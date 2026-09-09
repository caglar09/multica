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
