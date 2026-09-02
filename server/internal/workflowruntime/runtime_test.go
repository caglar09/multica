package workflowruntime

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/issuestatus"
)

func TestSoftwareDevelopmentWorkflowDefinition(t *testing.T) {
	def := definition()
	if err := def.Validate(); err != nil {
		t.Fatalf("definition validation failed: %v", err)
	}
	if def.InitialState != issuestatus.InProgress {
		t.Fatalf("initial state = %q, want %q", def.InitialState, issuestatus.InProgress)
	}

	wantTransitions := map[string]string{
		"in_progress/workflow.started":           "in_progress",
		"in_progress/implementation.completed":   "in_review",
		"in_progress/implementation.failed":      "blocked",
		"in_review/review.completed":              "done",
		"in_review/review.changes_requested":      "in_progress",
		"in_review/review.exhausted":              "blocked",
		"in_review/review.failed":                 "blocked",
	}
	for _, tr := range def.Transitions {
		key := tr.From + "/" + tr.Event
		want, ok := wantTransitions[key]
		if !ok {
			t.Fatalf("unexpected transition %s -> %s", key, tr.To)
		}
		if tr.To != want {
			t.Fatalf("transition %s target = %q, want %q", key, tr.To, want)
		}
		delete(wantTransitions, key)
	}
	if len(wantTransitions) != 0 {
		t.Fatalf("missing transitions: %v", wantTransitions)
	}
}

func TestIssueEventIDIncludesRevisionAndStatus(t *testing.T) {
	got := issueEventID("workflow-start", issueSnapshot{ID: "issue-1", Revision: 42, Status: "in_progress"})
	want := "workflow-start:issue-1:42:in_progress"
	if got != want {
		t.Fatalf("issueEventID = %q, want %q", got, want)
	}
}

func TestRetryPending(t *testing.T) {
	if !retryPending(map[string]any{"retry_pending": true}) {
		t.Fatal("retry_pending=true was not detected")
	}
	if retryPending(map[string]any{"retry_pending": false}) {
		t.Fatal("retry_pending=false was treated as pending")
	}
	if retryPending(nil) {
		t.Fatal("nil payload was treated as retry pending")
	}
}
