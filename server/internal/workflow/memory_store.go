package workflow

import (
	"fmt"
	"sync"
)

// MemoryStore is a concurrency-safe reference implementation used by unit tests
// and local experiments. Production wiring uses PostgresStore.
type MemoryStore struct {
	mu         sync.Mutex
	runs       map[string]Run
	runByKey   map[string]string
	processed  map[string]string
	pending    []PendingAction
	nextRun    int64
	nextAction int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runs:      make(map[string]Run),
		runByKey:  make(map[string]string),
		processed: make(map[string]string),
	}
}

func (s *MemoryStore) GetOrCreateRun(event Event, definition Definition) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := definition.Name + "\x00" + event.WorkspaceID + "\x00" + event.IssueID
	if id, ok := s.runByKey[key]; ok {
		run := s.runs[id]
		if run.OwnerAgentID == "" {
			run.OwnerAgentID = event.OwnerAgentID
		}
		if run.ReviewerAgentID == "" {
			run.ReviewerAgentID = event.ReviewerAgentID
		}
		if run.AccountableUserID == "" {
			run.AccountableUserID = event.AccountableUserID
		}
		if run.ProjectID == "" {
			run.ProjectID = event.ProjectID
		}
		s.runs[id] = run
		return run, nil
	}

	s.nextRun++
	id := fmt.Sprintf("workflow-run-%d", s.nextRun)
	run := Run{
		ID:                id,
		WorkflowName:      definition.Name,
		Version:           definition.Version,
		WorkspaceID:       event.WorkspaceID,
		ProjectID:         event.ProjectID,
		IssueID:           event.IssueID,
		State:             definition.InitialState,
		Revision:          1,
		OwnerAgentID:      event.OwnerAgentID,
		ReviewerAgentID:   event.ReviewerAgentID,
		AccountableUserID: event.AccountableUserID,
	}
	s.runs[id] = run
	s.runByKey[key] = id
	return run, nil
}

func (s *MemoryStore) Apply(request ApplyRequest) (ApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if runID, duplicate := s.processed[request.EventID]; duplicate {
		run, ok := s.runs[runID]
		if !ok {
			return ApplyResult{}, fmt.Errorf("processed workflow event references missing run %q", runID)
		}
		return ApplyResult{Run: run, Duplicate: true}, nil
	}

	run, ok := s.runs[request.RunID]
	if !ok {
		return ApplyResult{}, fmt.Errorf("workflow run %q not found", request.RunID)
	}
	if run.Revision != request.ExpectedRevision || run.State != request.From {
		return ApplyResult{}, ErrRevisionConflict
	}

	run.State = request.To
	run.Revision++
	if request.From == "in_review" && request.To == "in_progress" {
		run.ReviewCycles++
	}
	s.runs[run.ID] = run
	s.processed[request.EventID] = run.ID

	for position, action := range request.Actions {
		s.nextAction++
		s.pending = append(s.pending, PendingAction{
			ID:          fmt.Sprintf("workflow-action-%d", s.nextAction),
			RunID:       run.ID,
			EventID:     request.EventID,
			Position:    position,
			Action:      action,
			Status:      "pending",
			MaxAttempts: 5,
		})
	}

	return ApplyResult{Run: run, Applied: true}, nil
}

// PendingActions returns a defensive copy for tests and local adapters.
func (s *MemoryStore) PendingActions() []PendingAction {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]PendingAction, len(s.pending))
	copy(out, s.pending)
	return out
}
