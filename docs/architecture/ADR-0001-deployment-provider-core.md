# ADR: Generic Deployment Provider Core

- Status: Accepted
- Scope: Phase 0–3 deployment provider foundation
- Branch: `feat/deployment-providers-core`

## Context

Multica needs a provider-neutral deployment control plane that can later be consumed by Autonomous Project OS without making deployment durability depend on autonomous planning/runtime code. Provider APIs are asynchronous, may acknowledge before completion, emit duplicate or out-of-order webhooks, and may be temporarily unavailable. Server restarts must not lose accepted deployment work.

The core architectural rule remains: policy/LLM layers may decide that a deployment should happen, but deterministic backend code validates provider capabilities, owns durable state, performs state transitions, and records evidence.

## Decision

### Package boundaries

- `server/internal/deployment` owns the provider-neutral domain, lifecycle rules, persistence contract, durable operation engine, retry/reconciliation logic, and webhook application semantics.
- `server/internal/integrations/deployment` owns provider implementations and the provider registry. Provider adapters may translate authentication/configuration and provider-specific payloads, but may not mutate deployment persistence directly.
- Autonomous Project OS may call this core through its public Go boundary in a later integration phase. The deployment core must not import autonomous project packages.

Dependency direction is therefore:

```text
caller / Autonomous Project OS
          |
          v
 internal/deployment  <--- interfaces + deterministic state
          ^
          |
 internal/integrations/deployment  <--- provider adapters/registry
```

### Durable model

Four PostgreSQL tables form the durable source of truth:

- `deployment_provider_connection`: workspace-scoped provider connection metadata and a secret reference; raw credentials are not persisted by this subsystem.
- `deployment_target`: deployable provider target/environment bound to a connection.
- `deployment_operation`: one requested provider action, its idempotency key, attempt/retry/lease state, provider operation identifier, status, revision, request and evidence.
- `deployment_event`: append-only provider observations/webhook events, including a provider event identifier/dedupe key and whether the event changed operation state.

Repository migrations follow existing Multica safety rules: no foreign-key/cascade dependency is introduced, cleanup remains explicit, and concurrent indexes live in a separate migration.

### Operation state machine

The core lifecycle is:

```text
queued -> dispatching -> running -> succeeded
   |           |           |
   |           +----------> failed
   |           +----------> retry_wait -> dispatching
   |           +----------> canceled
   +----------------------> canceled
```

`queued`, `dispatching`, `running`, and `retry_wait` are non-terminal. `succeeded`, `failed`, and `canceled` are terminal. Terminal states never regress. A provider observation may advance an operation but an older/out-of-order event may not move it backwards.

The persistence layer uses an optimistic `revision` for compare-and-set updates. Workers claim due operations with a time-bounded lease. Expired leases are recoverable after process failure.

### Idempotency and delivery semantics

- Trigger idempotency is workspace scoped through a caller-provided `idempotency_key`.
- Provider start calls receive the stable core operation ID and idempotency key, allowing adapters to map retries to provider idempotency primitives.
- Provider events are persisted before application. Duplicate provider event IDs/dedupe keys are accepted as no-ops.
- Webhook delivery is therefore at-least-once at the edge and effectively-once for durable event application.
- Dispatch is at-least-once. Correct adapters must use the supplied stable idempotency values when the upstream supports them.

### Provider contract and capabilities

Providers advertise explicit capabilities (`trigger`, `status`, `cancel`, `webhook`, `evidence`). The core validates a required capability before invoking the provider. Unsupported behavior is a typed error, not silent success.

The provider contract returns normalized results/observations. Provider-specific status strings and payloads are retained as evidence, while only normalized core states participate in deterministic state transitions.

### Retry, reconciliation, and recovery

Transient adapter failures become `retry_wait` with bounded exponential backoff and a maximum attempt count. Permanent/validation/capability failures become `failed` without retry.

A reconciler periodically examines non-terminal operations whose provider status may have changed and polls providers that support `status`. It also makes expired leased work eligible for another worker. This is the restart-recovery mechanism; in-memory goroutines/timers are never the source of truth.

### Webhook trust boundary

Provider adapters own verification/parsing of provider-specific webhook envelopes and return normalized events. The core owns dedupe, persistence, operation lookup, legal transition validation, and evidence recording. An adapter cannot directly mark an operation complete.

### Evidence

Every dispatch/reconciliation/webhook observation may append provider evidence. The latest operation evidence is stored for efficient reads, while `deployment_event` preserves the durable event history needed to explain why state changed.

## Rejected alternatives

1. Reuse Autonomous Project OS deployment rows directly: rejected because it couples a generic integration primitive to one orchestration product and prevents independent reuse/testing.
2. Treat webhooks as authoritative DB mutations: rejected because duplicate/out-of-order delivery would bypass lifecycle validation.
3. Keep retry/reconciliation only in memory: rejected because accepted deployment work would be lost or stranded after restart.
4. Hide unsupported capabilities behind fake success: rejected because deployment must fail closed when the provider cannot perform the requested behavior.
5. Store raw provider credentials in deployment tables: rejected; connections store only a secret/config reference owned by the surrounding credential system.

## Phase exit gates

- Phase 0: this ADR is committed and the package/data ownership rules are explicit.
- Phase 1: schema, domain types, state machine, repository/idempotency semantics, and tests exist.
- Phase 2: capability-aware provider contract, registry, deterministic fake provider, and contract tests exist.
- Phase 3: durable trigger/claim/dispatch, webhook dedupe/application, retry/backoff, reconciliation, evidence, and restart recovery tests exist.
