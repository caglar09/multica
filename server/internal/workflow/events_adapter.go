package workflow

import (
	"log/slog"

	"github.com/multica-ai/multica/server/internal/events"
)

// EventSelector maps an existing Multica domain event to a workflow and
// normalized workflow event. Returning ok=false ignores the event.
type EventSelector func(events.Event) (workflowName string, event Event, ok bool)

// Subscribe connects the deterministic workflow engine to Multica's current
// in-process event bus. The adapter is deliberately thin so the same engine can
// later consume an outbox/Redis stream without changing workflow definitions.
func Subscribe(bus *events.Bus, engine *Engine, selector EventSelector) {
	if bus == nil || engine == nil || selector == nil {
		return
	}

	bus.SubscribeAll(func(source events.Event) {
		workflowName, event, ok := selector(source)
		if !ok {
			return
		}
		if _, err := engine.Handle(workflowName, event); err != nil {
			slog.Error(
				"workflow event handling failed",
				"workflow", workflowName,
				"event_type", source.Type,
				"workspace_id", source.WorkspaceID,
				"error", err,
			)
		}
	})
}
