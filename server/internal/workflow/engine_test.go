package workflow

import (
	"errors"
	"sync"
	"testing"
)

func TestEngineTransitionsAndQueuesReviewer(t *testing.T) {
	store := NewMemoryStore()
	engine, err := New(store, softwareDevelopmentWorkflow())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	first, err := engine.Handle("software-development", Event{
		ID:          "evt-1",
		Type:        "implementation.started",
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
	})
	if err != nil {
		t.Fatalf("start implementation: %v", err)
	}
	if first.Run.State != "in_progress" {
		t.Fatalf("state = %q, want in_progress", first.Run.State)
	}

	result, err := engine.Handle("software-development", Event{
		ID:          "evt-2",
		Type:        "implementation.completed",
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
		Payload:     map[string]any{"success": true},
	})
	if err != nil {
		t.Fatalf("complete implementation: %v", err)
	}
	if result.Run.State != "in_review" {
		t.Fatalf("state = %q, want in_review", result.Run.State)
	}

	actions := store.PendingActions()
	if len(actions) != 1 {
		t.Fatalf("pending actions = %d, want 1", len(actions))
	}
	if got := actions[0].Action.Type; got != "trigger_agent" {
		t.Fatalf("action type = %q, want trigger_agent", got)
	}
	if got := actions[0].Action.Params["role"]; got != "reviewer" {
		t.Fatalf("review role = %q, want reviewer", got)
	}
}

func TestEngineRejectsFailedGuard(t *testing.T) {
	store := NewMemoryStore()
	engine, err := New(store, softwareDevelopmentWorkflow())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	_, err = engine.Handle("software-development", Event{
		ID: "evt-1", Type: "implementation.started", WorkspaceID: "ws-1", IssueID: "issue-1",
	})
	if err != nil {
		t.Fatalf("start implementation: %v", err)
	}
	result, err := engine.Handle("software-development", Event{
		ID:          "evt-2",
		Type:        "implementation.completed",
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
		Payload:     map[string]any{"success": false},
	})
	if err != nil {
		t.Fatalf("handle failed completion: %v", err)
	}
	if !result.Skipped || result.Run.State != "in_progress" {
		t.Fatalf("result = %+v, want skipped in_progress", result)
	}
}

func TestEngineProcessesEventIdempotently(t *testing.T) {
	store := NewMemoryStore()
	engine, err := New(store, softwareDevelopmentWorkflow())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	event := Event{
		ID: "evt-start", Type: "implementation.started", WorkspaceID: "ws-1", IssueID: "issue-1",
	}
	if _, err := engine.Handle("software-development", event); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	completed := Event{
		ID: "evt-complete", Type: "implementation.completed", WorkspaceID: "ws-1", IssueID: "issue-1",
		Payload: map[string]any{"success": true},
	}
	if _, err := engine.Handle("software-development", completed); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	second, err := engine.Handle("software-development", completed)
	if err != nil {
		t.Fatalf("duplicate completion: %v", err)
	}

	// The run has already moved, so a redelivery is naturally skipped before
	// Apply. Most importantly, no second action is enqueued.
	if !second.Skipped {
		t.Fatalf("duplicate result = %+v, want skipped", second)
	}
	if got := len(store.PendingActions()); got != 1 {
		t.Fatalf("pending actions = %d, want 1", got)
	}
}

func TestMemoryStorePreventsConcurrentDoubleTransition(t *testing.T) {
	store := NewMemoryStore()
	def := softwareDevelopmentWorkflow()
	run, err := store.GetOrCreateRun(Event{WorkspaceID: "ws", IssueID: "issue"}, def)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Apply(ApplyRequest{
				EventID: "evt-concurrent-" + string(rune('a'+i)),
				RunID: run.ID, ExpectedRevision: run.Revision, From: "todo", To: "in_progress",
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	var conflicts int
	for err := range errs {
		if errors.Is(err, ErrRevisionConflict) {
			conflicts++
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("revision conflicts = %d, want 1", conflicts)
	}
}

func TestDefinitionRejectsAmbiguousTransition(t *testing.T) {
	def := softwareDevelopmentWorkflow()
	def.Transitions = append(def.Transitions, Transition{
		From: "todo", Event: "implementation.started", To: "in_review",
	})
	if err := def.Validate(); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("Validate() error = %v, want ErrInvalidDefinition", err)
	}
}

func softwareDevelopmentWorkflow() Definition {
	return Definition{
		Name:         "software-development",
		Version:      1,
		InitialState: "todo",
		States: map[string]State{
			"todo":        {},
			"in_progress": {},
			"in_review": {
				OnEnter: []Action{{
					Type:   "trigger_agent",
					Params: map[string]string{"role": "reviewer"},
				}},
			},
			"done": {},
		},
		Transitions: []Transition{
			{From: "todo", Event: "implementation.started", To: "in_progress"},
			{
				From:  "in_progress",
				Event: "implementation.completed",
				To:    "in_review",
				Conditions: []Condition{{
					Field: "success", Operator: "equals", Value: true,
				}},
			},
			{
				From: "in_review", Event: "review.approved", To: "done",
				Conditions: []Condition{{Field: "approved", Operator: "equals", Value: true}},
			},
			{From: "in_review", Event: "review.changes_requested", To: "in_progress"},
		},
	}
}
