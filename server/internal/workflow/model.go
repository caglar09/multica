// Package workflow provides deterministic, event-driven workflow orchestration.
//
// Workflow owns state transitions and follow-up actions. Agents only perform the
// work assigned to them; they do not decide which status comes next or which
// agent should be mentioned.
package workflow

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrDefinitionNotFound = errors.New("workflow definition not found")
	ErrInvalidDefinition  = errors.New("invalid workflow definition")
	ErrRevisionConflict   = errors.New("workflow revision conflict")
)

// Event is the normalized input consumed by the workflow engine.
//
// Actor metadata is deliberately carried beside Payload: it is orchestration
// provenance, not prompt content. GetOrCreateRun persists the first known owner,
// reviewer and accountable human so every later automatic hop uses a stable
// team even if the issue assignee changes while the workflow is running.
type Event struct {
	ID                string
	Type              string
	WorkspaceID       string
	ProjectID         string
	IssueID           string
	ActorType         string
	ActorID           string
	OwnerAgentID      string
	ReviewerAgentID   string
	AccountableUserID string
	Payload           map[string]any
}

// Definition is a versioned deterministic state machine.
type Definition struct {
	Name         string
	Version      int
	InitialState string
	States       map[string]State
	Transitions []Transition
}

// State describes actions that must be durably enqueued when the state is entered.
type State struct {
	OnEnter []Action
}

// Transition maps one event in one state to exactly one target state.
type Transition struct {
	From       string
	Event      string
	To         string
	Conditions []Condition
}

// Condition is intentionally declarative so definitions can later live in the
// database or YAML without embedding LLM prompts or Go functions.
type Condition struct {
	Field    string
	Operator string
	Value    any
}

// Action describes a side effect to enqueue after a successful transition.
// Built-in runtime actions are set_issue_status and trigger_agent.
type Action struct {
	Type   string
	Params map[string]string
}

// Run is the durable state of one workflow instance.
type Run struct {
	ID                string
	WorkflowName      string
	Version           int
	WorkspaceID       string
	ProjectID         string
	IssueID           string
	State             string
	Revision          int64
	OwnerAgentID      string
	ReviewerAgentID   string
	AccountableUserID string
	ReviewCycles      int
	UpdatedAt         time.Time
}

// PendingAction is a durable side effect attached to a transition.
type PendingAction struct {
	ID         string
	RunID      string
	EventID    string
	Position   int
	Action     Action
	Status     string
	Attempts   int
	MaxAttempts int
	LeaseToken string
}

// ApplyRequest is the atomic write contract. A production Store must record the
// processed event, move the run and enqueue Actions in one transaction.
type ApplyRequest struct {
	EventID          string
	EventType        string
	RunID            string
	ExpectedRevision int64
	From             string
	To               string
	Actions          []Action
}

// ApplyResult reports whether this caller performed the transition.
type ApplyResult struct {
	Run       Run
	Applied   bool
	Duplicate bool
}

// Store is the persistence boundary for the deterministic engine.
type Store interface {
	GetOrCreateRun(event Event, definition Definition) (Run, error)
	Apply(request ApplyRequest) (ApplyResult, error)
}

// Validate rejects ambiguous or incomplete workflow definitions.
func (d Definition) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidDefinition)
	}
	if d.Version < 1 {
		return fmt.Errorf("%w: version must be >= 1", ErrInvalidDefinition)
	}
	if _, ok := d.States[d.InitialState]; !ok {
		return fmt.Errorf("%w: initial state %q is not defined", ErrInvalidDefinition, d.InitialState)
	}

	seen := make(map[string]struct{}, len(d.Transitions))
	for _, tr := range d.Transitions {
		if _, ok := d.States[tr.From]; !ok {
			return fmt.Errorf("%w: transition source %q is not defined", ErrInvalidDefinition, tr.From)
		}
		if _, ok := d.States[tr.To]; !ok {
			return fmt.Errorf("%w: transition target %q is not defined", ErrInvalidDefinition, tr.To)
		}
		if strings.TrimSpace(tr.Event) == "" {
			return fmt.Errorf("%w: transition event is required", ErrInvalidDefinition)
		}
		key := tr.From + "\x00" + tr.Event
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: ambiguous transition from %q on %q", ErrInvalidDefinition, tr.From, tr.Event)
		}
		seen[key] = struct{}{}
	}
	return nil
}
