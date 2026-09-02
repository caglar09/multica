# Autonomous workflow engine

This package is the deterministic orchestration boundary for autonomous agents.

## Why it exists

Agents should not be responsible for workflow bookkeeping. Instructions such as
"move the issue to In Review and mention the reviewer when you finish" mix three
different responsibilities:

1. perform the implementation;
2. decide and persist the next workflow state;
3. choose and trigger the next actor.

LLMs are intentionally removed from responsibilities 2 and 3. An agent emits a
domain event describing the outcome of its work. The workflow engine consumes
that event, evaluates a versioned state machine and atomically persists both the
new state and the follow-up actions.

The direction is:

```text
agent result -> domain event -> workflow engine -> durable action -> agent dispatch
```

not:

```text
agent -> change status -> mention another agent -> hope the instruction is followed
```

## Core contracts

- `Definition`: versioned states, transitions, guards and on-enter actions.
- `Event`: normalized event consumed by the engine.
- `Run`: durable state of one workflow instance.
- `Store.Apply`: the critical transactional boundary. Production storage must
  record event idempotency, compare the run revision, transition state and
  enqueue actions in the same database transaction.
- `Subscribe`: adapter from the existing `internal/events.Bus`. The engine is
  not coupled to that bus and can later consume a transactional outbox or Redis
  stream unchanged.

## Example

A first software-development workflow can be modeled as:

```text
todo
  -- implementation.started --> in_progress
  -- implementation.completed(success=true) --> in_review
                                      |
                                      +-- on enter: trigger_agent(role=reviewer)

in_review
  -- review.approved --> done
  -- review.changes_requested --> in_progress
```

The developer agent only returns an implementation outcome. It never receives an
instruction to update issue status or mention a reviewer.

## Production follow-up

The package intentionally starts with a pure engine and a memory store so the
state-machine semantics are independently testable. The next integration slice
should add:

1. PostgreSQL tables for workflow definitions, runs, processed events and pending
   actions.
2. A PostgreSQL `Store` implementation whose `Apply` is one transaction with
   optimistic revision checking.
3. An action dispatcher that claims pending actions with a lease and maps
   `trigger_agent` to Multica's existing agent task/daemon execution path.
4. Domain-event emission from issue status changes and agent task completion.
5. Retry/dead-letter policy and a `needs_human` escalation action.
6. A workflow editor/API only after the execution semantics are proven.

Do not put workflow instructions back into agent prompts. Agent prompts describe
roles and task acceptance criteria; this package owns lifecycle orchestration.
