package workflowruntime

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
		"in_progress/implementation.completed":    "in_review",
		"in_progress/implementation.failed":       "blocked",
		"in_progress/issue.completed":             "done",
		"in_review/review.completed":              "done",
		"in_review/review.changes_requested":      "in_progress",
		"in_review/review.exhausted":              "blocked",
		"in_review/review.failed":                 "blocked",
		"in_review/issue.completed":               "done",
		"blocked/implementation.retry_completed":  "in_review",
		"blocked/review.retry_completed":          "done",
		"blocked/review.retry_changes_requested":  "in_progress",
		"blocked/issue.retry_requested":           "in_progress",
		"blocked/issue.completed":                 "done",
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

func TestBlockedFailureRecoveryLookbackIncludesCausalFailure(t *testing.T) {
	blockedAt := time.Date(2026, 9, 3, 17, 13, 41, 866686000, time.UTC)
	failedAt := time.Date(2026, 9, 3, 17, 13, 41, 850356000, time.UTC) // 16.33ms before blocked write

	since := blockedAt.Add(-blockedFailureRecoveryLookback)
	if failedAt.Before(since) {
		t.Fatalf("causal failure %s fell before recovery floor %s", failedAt, since)
	}
	if stale := blockedAt.Add(-2 * time.Minute); !stale.Before(since) {
		t.Fatalf("stale failure %s unexpectedly falls inside recovery lookback starting %s", stale, since)
	}
}

func TestBlockedRetryCompletionEvent(t *testing.T) {
	ownerID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	reviewerID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	lineageID := pgtype.UUID{Bytes: [16]byte{9}, Valid: true}
	run := workflow.Run{
		State:           issuestatus.Blocked,
		OwnerAgentID:    util.UUIDToString(ownerID),
		ReviewerAgentID: util.UUIDToString(reviewerID),
	}

	implementationRetry := db.AgentTaskQueue{AgentID: ownerID, RerunOfTaskID: lineageID}
	if got := blockedRetryCompletionEvent(run, implementationRetry); got != "implementation.retry_completed" {
		t.Fatalf("implementation retry event = %q", got)
	}

	reviewRetry := db.AgentTaskQueue{AgentID: reviewerID, RetryOfTaskID: lineageID}
	if got := blockedRetryCompletionEvent(run, reviewRetry); got != "review.retry_completed" {
		t.Fatalf("review retry event = %q", got)
	}

	unrelated := db.AgentTaskQueue{AgentID: ownerID}
	if got := blockedRetryCompletionEvent(run, unrelated); got != "" {
		t.Fatalf("unrelated completion unexpectedly recovered blocked run with %q", got)
	}

	run.State = issuestatus.InProgress
	if got := blockedRetryCompletionEvent(run, implementationRetry); got != "" {
		t.Fatalf("non-blocked run unexpectedly recovered with %q", got)
	}
}

func TestSystemStatusProjection(t *testing.T) {
	if !isSystemStatusProjection(events.Event{ActorType: "system"}) {
		t.Fatal("server-owned status write was not recognized as a system projection")
	}
	if isSystemStatusProjection(events.Event{ActorType: "agent", ActorID: "agent-1"}) {
		t.Fatal("agent status update was incorrectly treated as a system projection")
	}
	if isSystemStatusProjection(events.Event{ActorType: "member", ActorID: "member-1"}) {
		t.Fatal("member status update was incorrectly treated as a system projection")
	}
}

func TestPreserveDurableRunTeam(t *testing.T) {
	run := workflow.Run{
		ID:              "run-1",
		IssueID:         "issue-1",
		OwnerAgentID:    "owner-1",
		ReviewerAgentID: "reviewer-1",
	}
	got, err := preserveDurableRunTeam(run, errors.New("planner offline"))
	if err != nil {
		t.Fatalf("durable run team should survive planner outage: %v", err)
	}
	if got.OwnerAgentID != run.OwnerAgentID || got.ReviewerAgentID != run.ReviewerAgentID {
		t.Fatalf("durable run actors changed: got %#v want %#v", got, run)
	}

	_, err = preserveDurableRunTeam(workflow.Run{}, errors.New("planner offline"))
	if err == nil {
		t.Fatal("missing durable actors should not hide planner failure")
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


func TestDiscoveredProjectNodeKindRoutesSpecialistFamilies(t *testing.T) {
	cases := map[string]projectorchestration.NodeKind{
		"product":      projectorchestration.NodeProduct,
		"architecture": projectorchestration.NodeArchitecture,
		"design":       projectorchestration.NodeDesign,
		"security":     projectorchestration.NodeSecurity,
		"qa":           projectorchestration.NodeQA,
		"review":       projectorchestration.NodeReview,
		"release":      projectorchestration.NodeRelease,
		"backend":      projectorchestration.NodeImplementation,
		"fullstack":    projectorchestration.NodeImplementation,
	}
	for family, want := range cases {
		if got := discoveredProjectNodeKind(family); got != want {
			t.Fatalf("discoveredProjectNodeKind(%q) = %q, want %q", family, got, want)
		}
	}
}

func TestDiscoveredProjectPriority(t *testing.T) {
	cases := map[string]int{
		"urgent": 100,
		"high": 75,
		"medium": 50,
		"low": 25,
		"none": 0,
	}
	for value, want := range cases {
		if got := discoveredProjectPriority(value); got != want {
			t.Fatalf("discoveredProjectPriority(%q) = %d, want %d", value, got, want)
		}
	}
}
