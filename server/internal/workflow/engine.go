package workflow

import (
	"fmt"
	"reflect"
	"strings"
)

// Engine evaluates events against versioned deterministic definitions.
type Engine struct {
	store       Store
	definitions map[string]Definition
}

func New(store Store, definitions ...Definition) (*Engine, error) {
	if store == nil {
		return nil, errorsNew("workflow store is required")
	}
	index := make(map[string]Definition, len(definitions))
	for _, d := range definitions {
		if err := d.Validate(); err != nil {
			return nil, err
		}
		if _, exists := index[d.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate definition %q", ErrInvalidDefinition, d.Name)
		}
		index[d.Name] = d
	}
	return &Engine{store: store, definitions: index}, nil
}

// Result describes the decision made for one event.
type Result struct {
	Run        Run
	Transition *Transition
	Applied    bool
	Duplicate  bool
	Skipped    bool
}

// Handle evaluates one event. It never executes agent work itself; follow-up
// actions are persisted atomically by Store.Apply and consumed by a dispatcher.
func (e *Engine) Handle(workflowName string, event Event) (Result, error) {
	definition, ok := e.definitions[workflowName]
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrDefinitionNotFound, workflowName)
	}
	if strings.TrimSpace(event.ID) == "" {
		return Result{}, errorsNew("workflow event id is required")
	}
	if strings.TrimSpace(event.Type) == "" {
		return Result{}, errorsNew("workflow event type is required")
	}
	if strings.TrimSpace(event.WorkspaceID) == "" || strings.TrimSpace(event.IssueID) == "" {
		return Result{}, errorsNew("workflow event workspace_id and issue_id are required")
	}

	run, err := e.store.GetOrCreateRun(event, definition)
	if err != nil {
		return Result{}, err
	}

	var selected *Transition
	for i := range definition.Transitions {
		tr := &definition.Transitions[i]
		if tr.From != run.State || tr.Event != event.Type {
			continue
		}
		matches, err := conditionsMatch(event.Payload, tr.Conditions)
		if err != nil {
			return Result{}, fmt.Errorf("evaluate transition %s -> %s: %w", tr.From, tr.To, err)
		}
		if matches {
			selected = tr
			break
		}
	}
	if selected == nil {
		return Result{Run: run, Skipped: true}, nil
	}

	actions := cloneActions(definition.States[selected.To].OnEnter)
	applied, err := e.store.Apply(ApplyRequest{
		EventID:          event.ID,
		RunID:            run.ID,
		ExpectedRevision: run.Revision,
		From:             selected.From,
		To:               selected.To,
		Actions:          actions,
	})
	if err != nil {
		return Result{}, err
	}

	return Result{
		Run:        applied.Run,
		Transition: selected,
		Applied:    applied.Applied,
		Duplicate:  applied.Duplicate,
	}, nil
}

func conditionsMatch(payload map[string]any, conditions []Condition) (bool, error) {
	for _, c := range conditions {
		value, exists := lookup(payload, c.Field)
		switch strings.ToLower(strings.TrimSpace(c.Operator)) {
		case "", "equals", "eq":
			if !exists || !reflect.DeepEqual(value, c.Value) {
				return false, nil
			}
		case "not_equals", "neq":
			if exists && reflect.DeepEqual(value, c.Value) {
				return false, nil
			}
		case "exists":
			want, ok := c.Value.(bool)
			if !ok {
				return false, fmt.Errorf("exists condition value must be boolean")
			}
			if exists != want {
				return false, nil
			}
		case "truthy":
			if !exists || !isTruthy(value) {
				return false, nil
			}
		default:
			return false, fmt.Errorf("unsupported condition operator %q", c.Operator)
		}
	}
	return true, nil
}

func lookup(payload map[string]any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	var current any = payload
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func isTruthy(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return value != ""
	case int:
		return value != 0
	case int64:
		return value != 0
	case float64:
		return value != 0
	case nil:
		return false
	default:
		return true
	}
}

func cloneActions(actions []Action) []Action {
	out := make([]Action, len(actions))
	for i, action := range actions {
		out[i] = Action{Type: action.Type}
		if action.Params != nil {
			out[i].Params = make(map[string]string, len(action.Params))
			for k, v := range action.Params {
				out[i].Params[k] = v
			}
		}
	}
	return out
}

func errorsNew(message string) error { return fmt.Errorf("%s", message) }
