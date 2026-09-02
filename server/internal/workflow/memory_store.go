package workflow

import (
	"fmt"
	"sync"
)

// MemoryStore is a concurrency-safe reference implementation used by unit tests
// and local experiments. Production wiring should use a PostgreSQL store whose
// Apply operation is transactional.
type MemoryStore struct {
	mu        sync.Mutex
	runs      map[string]Run
	runByKey  map[string]string
	processed map[string]string
	pending   []PendingAction
	nextRun   int64
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
		return s.runs[id], nil
	}

	s.nextRun++
	id := fmt.Sprintf("workflow-run-%d", s.nextRun)
	run := Run{
		ID:           id,
		WorkflowName: definition.Name,
		Version:      definition.Version,
		WorkspaceID:  event.WorkspaceID,
		ProjectID:    event.ProjectID,
		IssueID:      event.IssueID,
		State:        definition.InitialState,
		Revision:     1,
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
	s.runs[run.ID] = run
	s.processed[request.EventID] = run.ID

	for _, action := range request.Actions {
		s.nextAction++
		s.pending = append(s.pending, PendingAction{
			ID:      fmt.Sprintf("workflow-action-%d", s.nextAction),
			RunID:   run.ID,
			EventID: request.EventID,
			Action:  action,
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
