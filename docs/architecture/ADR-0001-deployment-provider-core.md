# ADR-0001: Generic Deployment Provider Core and Platform Contracts

- **Status:** Accepted
- **Role:** Solution Architect (`solution_architect`)
- **Stage:** `architecture` (`architecture_adr_design`)
- **Scope Identifier:** `[autonomous-project-node:architecture_adr_design]`
- **Target Branch:** `caglar09/multica:feat/deployment-providers-core`
- **Target Upstream PR:** `multica-ai/multica:main`

---

## 1. Context and Problem Statement

Multica requires an extensible, provider-neutral deployment control plane that can connect third-party hosting platforms (starting with Vercel as the reference P0 implementation, followed by Render, Railway, Heroku, and Cloudflare) to Multica projects.

### 1.1 The Architectural Challenge
Provider APIs are inherently asynchronous, distributed, and heterogeneous:
1. **Asynchronous Execution:** External platforms accept deployment requests and immediately yield external identifiers (e.g. Vercel deployment IDs), while build and provisioning lifecycles complete minutes later. Holding open HTTP connections or using in-memory timers across server restarts leads to lost or stranded operations.
2. **Unreliable Webhook Delivery:** Edge webhooks can be duplicated, delayed, or delivered out of order over the public internet. An older event (e.g., `deployment.building`) arriving after a completion event (e.g., `deployment.ready`) must not regress the operation to an in-flight status or overwrite live URL evidence.
3. **Upstream PR Purity vs. Autonomous Workflows:** Multica Core is open source and intended for broad community use. Experimental autonomous planning, scheduler DAGs, and prompt trees from the Autonomous Project OS must not contaminate the core platform control plane. The generic deployment subsystem must be completely self-contained and independently testable, with zero imports of autonomous runtime or orchestration packages.

---

## 2. Package Boundaries and Architecture Topology

To guarantee clean encapsulation and satisfy upstream submission standards, the deployment control plane is strictly separated into three architectural tiers:

```text
+------------------------------------------------------------------------------------+
|                                    Consumers                                       |
|  +-----------------------+  +-----------------------+  +------------------------+  |
|  | Web / Desktop UI      |  | CLI / Automation      |  | Autonomous Project OS  |  |
|  | (React Views/Hooks)   |  | (REST API Endpoints)  |  | (Planning / Scheduler) |  |
|  +-----------+-----------+  +-----------+-----------+  +-----------+------------+  |
+--------------|--------------------------|--------------------------|---------------+
               |                          |                          |
               +--------------------+     |     +--------------------+
                                    |     |     |
                                    v     v     v
+------------------------------------------------------------------------------------+
|                Core Deployment Domain (server/internal/deployment/)                |
|                                                                                    |
|  - Domain Entities: ProviderConnection, Target, Operation, Event                  |
|  - Provider-Neutral Contracts: Provider, CapabilitySet, RemoteTarget,              |
|    RemoteOperation, ProviderEvent                                                  |
|  - Deterministic State Machine & Monotonic Event Evaluation Matrix                 |
|  - Idempotency & Event Deduplication Engine                                        |
|  - Secret Isolation & Masking (SecretBox KeyRing, Write-Only Policies)             |
|  - Storage Abstraction: DBTX & PostgresRepository (Zero DB Foreign Keys)          |
+------------------------------------------^-----------------------------------------+
                                           |
                                           | (Implements interfaces & registers)
                                           |
+------------------------------------------+-----------------------------------------+
|        Provider Integrations Layer (server/internal/integrations/deployment/)       |
|                                                                                    |
|  - Registry: Thread-safe dynamic provider catalog (sync.RWMutex)                   |
|  - Capability Enforcement: Pre-flight capability negotiation                      |
|  - Concrete Adapters:                                                              |
|      * Vercel P0 Connector (REST Client, Target Mapper, HMAC Webhook Verifier)     |
|      * FakeProvider (Deterministic test double for zero-network testing)           |
|      * Future Adapters (Render, Railway, Heroku)                                   |
+------------------------------------------------------------------------------------+
```

### 2.1 Package Boundary Guarantees
1. **Core Domain (`server/internal/deployment/`):**
   - Owns provider-neutral models, repository contracts, state transition logic, and cryptographic verification helpers.
   - **Zero Upstream Contamination:** Contains **zero imports** of `server/internal/daemon`, `server/internal/scheduler`, `server/internal/dispatch`, `server/internal/database`, `server/internal/service`, or any autonomous runtime/orchestration package.
   - Interacts with PostgreSQL exclusively through a local, minimal database interface (`DBTX`), adhering to Multica's application-level referential integrity rules (no database foreign key constraints).
2. **Integration Adapters (`server/internal/integrations/deployment/`):**
   - Owns provider implementations and the dynamic `Registry`.
   - Adapters translate provider-specific HTTP envelopes, OAuth scopes, and webhook payloads into normalized core domain structures.
   - Adapters never execute database queries directly; they interact with the core domain via exported domain contracts.
3. **Consumer Layer:**
   - Autonomous Project OS and public HTTP handlers import `server/internal/deployment` as an external library.
   - Downward dependency rule: Core never imports consumers; callers adapt to the core service contract.

---

## 3. Provider-Neutral Platform Interfaces

The platform defines generic Go interfaces and data structures that model all deployment operations uniformly across external providers.

### 3.1 Provider Interface (`server/internal/deployment/contract.go`)

```go
// Provider defines the core contract that every deployment provider adapter must satisfy.
type Provider interface {
	// Kind returns the unique slug identifying this provider (e.g., "vercel", "render").
	Kind() string

	// Capabilities returns the feature flags supported by this provider.
	Capabilities() CapabilitySet

	// ValidateConnection verifies that the supplied credentials authenticate against the provider.
	ValidateConnection(ctx context.Context, creds ConnectionCredentials) (ConnectionInfo, error)

	// ListTargets queries remote projects/services available under this connection.
	ListTargets(ctx context.Context, conn ProviderConnection, creds ConnectionCredentials) ([]RemoteTarget, error)

	// GetTarget retrieves metadata for a specific remote project/service.
	GetTarget(ctx context.Context, conn ProviderConnection, creds ConnectionCredentials, externalID string) (RemoteTarget, error)

	// CreateTarget provisions a new remote project or deployment target.
	CreateTarget(ctx context.Context, conn ProviderConnection, creds ConnectionCredentials, req CreateTargetRequest) (RemoteTarget, error)

	// TriggerDeployment initiates a remote deployment for a specific git revision or artifact.
	TriggerDeployment(ctx context.Context, req DeploymentRequest) (RemoteOperation, error)

	// GetOperation queries the external provider for the current status of a deployment.
	GetOperation(ctx context.Context, req OperationLookup) (RemoteOperation, error)

	// CancelOperation aborts an in-flight deployment on the provider.
	CancelOperation(ctx context.Context, req CancelRequest) (RemoteOperation, error)

	// ParseWebhook verifies and normalizes an inbound webhook envelope into a ProviderEvent.
	ParseWebhook(ctx context.Context, req WebhookRequest) (ProviderEvent, error)
}
```

### 3.2 Auxiliary Capability Interfaces
Providers advertising advanced capabilities implement dedicated extension interfaces:

```go
// PromotionProvider is an optional interface for promoting preview deployments to production.
type PromotionProvider interface {
	Promote(ctx context.Context, req PromotionRequest) (RemoteOperation, error)
}

// RollbackProvider is an optional interface for instant production rollback.
type RollbackProvider interface {
	Rollback(ctx context.Context, req RollbackRequest) (RemoteOperation, error)
}

// LogProvider is an optional interface for retrieving deployment build and runtime logs.
type LogProvider interface {
	GetLogs(ctx context.Context, req LogRequest) (LogResult, error)
}

// RestartProvider is an optional interface for restarting a running deployment container.
type RestartProvider interface {
	Restart(ctx context.Context, req RestartRequest) (RemoteOperation, error)
}
```

### 3.3 Core Contract Data Structures

#### 3.3.1 `CapabilitySet`
The capability set specifies supported behaviors without leaking provider-specific quirks:
```go
type CapabilitySet struct {
	Trigger         bool `json:"trigger"`          // Can trigger deployment from source ref
	Status          bool `json:"status"`           // Supports external polling reconciliation
	Cancel          bool `json:"cancel"`           // Can abort an in-flight deployment
	Webhook         bool `json:"webhook"`          // Emits push webhook events
	Evidence        bool `json:"evidence"`         // Yields preview URLs, inspect links, diagnostics
	Logs            bool `json:"logs"`             // Provides build/runtime log streams
	Promote         bool `json:"promote"`          // Supports preview-to-production promotion
	Rollback        bool `json:"rollback"`         // Supports instant rollback
	TargetDiscovery bool `json:"target_discovery"` // Discovers remote projects/teams
	TargetCreate    bool `json:"target_create"`    // Can dynamically create remote projects
}
```

#### 3.3.2 `RemoteTarget`
```go
type RemoteTarget struct {
	ExternalID    string          `json:"external_id"`
	Name          string          `json:"name"`
	Environment   string          `json:"environment"`
	DefaultBranch string          `json:"default_branch,omitempty"`
	WebURL        string          `json:"web_url,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}
```

#### 3.3.3 `RemoteOperation`
```go
type RemoteOperation struct {
	ProviderOperationID string          `json:"provider_operation_id"`
	ExternalStatus      string          `json:"external_status"`
	NormalizedStatus    OperationStatus `json:"normalized_status"`
	DeploymentURL       string          `json:"deployment_url,omitempty"`
	InspectURL          string          `json:"inspect_url,omitempty"`
	Evidence            json.RawMessage `json:"evidence,omitempty"`
	ErrorMessage        string          `json:"error_message,omitempty"`
}
```

#### 3.3.4 `ProviderEvent`
```go
type ProviderEvent struct {
	ProviderEventID     string          `json:"provider_event_id"`
	ProviderEventType   string          `json:"provider_event_type"`
	ProviderOperationID string          `json:"provider_operation_id"`
	ObservedStatus      OperationStatus `json:"observed_status"`
	Evidence            json.RawMessage `json:"evidence,omitempty"`
	RawPayload          json.RawMessage `json:"raw_payload,omitempty"`
}
```

---

## 4. Operation State Machine and Transition Matrix

Operations adhere to a deterministic, finite state machine designed to prevent terminal state corruption, recover from process restarts, and withstand out-of-order webhook delivery.

### 4.1 State Definitions
- **Non-Terminal States:**
  - `queued` (Rank 10): Operation persisted locally; awaiting dispatch lease.
  - `retry_wait` (Rank 15): Transient provider dispatch failure; awaiting exponential backoff delay before re-dispatch.
  - `dispatching` (Rank 20): Worker has acquired optimistic lease and is calling provider API.
  - `running` (Rank 30): Provider accepted deployment; build or container provisioning is underway.
- **Terminal States (Immutable, Rank 100):**
  - `succeeded`: Build and deployment completed successfully; live URL confirmed.
  - `failed`: Build failed, configuration was invalid, or maximum retry attempts were exhausted.
  - `canceled`: Operation explicitly aborted by authorized user before completion.

### 4.2 State Machine Transition Matrix

| From \ To | `queued` | `retry_wait` | `dispatching` | `running` | `succeeded` | `failed` | `canceled` |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **`queued`** | Idempotent | ❌ | **Legal** | ❌ | ❌ | **Legal** | **Legal** |
| **`retry_wait`** | ❌ | Idempotent | **Legal** | ❌ | ❌ | **Legal** | **Legal** |
| **`dispatching`**| ❌ | **Legal (Retry)** | Idempotent | **Legal** | ❌ | **Legal** | **Legal** |
| **`running`** | Out-of-order | **Legal (Retry)** | Out-of-order | Idempotent | **Legal** | **Legal** | **Legal** |
| **`succeeded`** | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | Idempotent | ⛔ Immutable | ⛔ Immutable |
| **`failed`** | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | Idempotent | ⛔ Immutable |
| **`canceled`** | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | ⛔ Immutable | Idempotent |

### 4.3 Handling Out-of-Order Webhook Delivery and Terminal Immutability

In distributed cloud environments, push webhooks may be delivered with network delay, out of sequence, or duplicated. The platform enforces an evaluation pipeline:

```text
Incoming Webhook / Polling Event
               │
               ▼
   [HMAC Signature Verification] ────▶ Invalid ────▶ HTTP 401 Reject
               │ Valid
               ▼
[Deduplication on (provider, event_id)] ──▶ Duplicate ──▶ Record No-Op (applied=false)
               │ First Seen
               ▼
   [Fetch Current Operation State]
               │
               ▼
  [EvaluateTransition(current, observed)]
    ├── Case 1: current.IsTerminal()
    │     ├── observed == current ────▶ ActionIgnoreIdempotent (applied=false)
    │     └── observed != current ────▶ ActionRejectTerminalRegression (applied=false, log warning)
    ├── Case 2: observed == current ──▶ ActionIgnoreIdempotent (applied=false)
    ├── Case 3: CanTransition(current, observed) ──▶ ActionApply (applied=true, update status)
    ├── Case 4: Rank(observed) <= Rank(current) ──▶ ActionIgnoreOutOfOrder (applied=false, record audit)
    └── Case 5: Illegal Forward Jump ────────────▶ ActionRejectIllegal (applied=false, record audit)
```

#### Evaluation Rules:
1. **Terminal State Immutability:** Once an operation enters `succeeded`, `failed`, or `canceled`, its status is frozen permanently. Any incoming event attempting to regress status (e.g. `succeeded -> running`) is rejected with `ActionRejectTerminalRegression`.
2. **Monotonic Forward Progression:** Non-terminal events are evaluated against `StatusRank`. If an event arrives with a rank lower than or equal to current state (e.g. delayed `dispatching` event arriving while already in `running`), it is evaluated as `ActionIgnoreOutOfOrder`. The event is persisted to `deployment_event` for audit purposes, but does not mutate `deployment_operation.status`.
3. **Idempotent Repetition:** Identical duplicate status observations produce `ActionIgnoreIdempotent` with `applied = false`.

---

## 5. Idempotency and Deduplication Strategy

Deployments touch live production traffic and cloud spend. Accidental double triggers or uncoordinated concurrent requests must be defeated deterministically.

### 5.1 Idempotency Key Derivation
Every deployment mutation requires a caller-supplied or deterministically computed idempotency key:

$$\text{IdempotencyKey} = \text{SHA256}(\text{WorkspaceID} \mathbin{\Vert} \text{TargetID} \mathbin{\Vert} \text{CommitSHA} \mathbin{\Vert} \text{Environment} \mathbin{\Vert} \text{OperationKind} \mathbin{\Vert} \text{ApprovalRef})$$

### 5.2 Concurrent Database Unique Indexes
Under Multica migration rules, indexes are created concurrently without foreign key constraints:
- **Operation Idempotency:**
  `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_deployment_operation_idempotency ON deployment_operation (workspace_id, idempotency_key);`
  Subsequent requests sharing the same key safely return the existing `deployment_operation` record without initiating new provider calls.
- **Event Deduplication:**
  `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_deployment_event_dedupe ON deployment_event (provider, provider_event_id);`
  Replayed webhooks with the same `(provider, provider_event_id)` fail the unique constraint or perform an `ON CONFLICT DO NOTHING` no-op.

### 5.3 Optimistic Concurrency & Leases
To coordinate background worker execution without distributed lock managers (ZooKeeper/Redis):
- Operations carry an integer `revision` and a `lease_expires_at` timestamp.
- Workers claim operations using atomic compare-and-set:
  `UPDATE deployment_operation SET status = 'dispatching', lease_owner = $1, lease_expires_at = $2, revision = revision + 1 WHERE id = $3 AND revision = $4;`
- Expired leases are safely reclaimed by the reconciler worker if a backend process crashes mid-flight.

---

## 6. Secret Isolation and Security Boundary

In alignment with **ADR-0002**, deployment secrets are guarded by strict custody controls:

1. **At-Rest Symmetric Encryption:** Credentials, OAuth tokens, and webhook secrets are sealed using AES-256-GCM (`server/internal/util/secretbox`).
2. **Write-Only API Policy:** Plaintext secrets are accepted during connection setup but are **never returned** across REST APIs or GraphQL endpoints. Read payloads return audit metadata and masked hints (e.g. `vercel_tok_...8f2a`).
3. **Zero Prompt & Log Leakage:** Plaintext tokens are never passed to LLM prompts, autonomous task envelopes, or structured telemetry logs.
4. **Constant-Time Webhook Verification:** HMAC signatures are verified using `crypto/hmac` and `crypto/subtle.ConstantTimeCompare` (or `hmac.Equal`) to eliminate timing side-channels.

---

## 7. Zero Dependency Boundary Confirmation and Audit

To confirm compliance with the strict zero-dependency architectural acceptance criterion, an automated AST import analysis was performed across the deployment platform packages:

### 7.1 Import Audit Table

| Package Directory | Allowed Standard Library & External Dependencies | Forbidden Packages (`daemon`, `scheduler`, `dispatch`, `database`, `service`, `projectorchestration`) | Status |
| :--- | :--- | :--- | :---: |
| `server/internal/deployment` | `context`, `crypto/*`, `encoding/*`, `errors`, `fmt`, `sort`, `strings`, `sync`, `time`, `github.com/google/uuid`, `github.com/jackc/pgx/v5` | **0 Forbidden Imports** | **COMPLIANT** |
| `server/internal/integrations/deployment` | `context`, `errors`, `fmt`, `sort`, `sync`, `testing`, `github.com/multica-ai/multica/server/internal/deployment` | **0 Forbidden Imports** | **COMPLIANT** |

### 7.2 Automated Verification Proof
The boundary is continuously protected by `server/internal/deployment/boundary_test.go`:
```go
func TestZeroDependencyBoundary(t *testing.T) { ... }
```
This test parses package ASTs and immediately fails CI if any forbidden autonomous runtime or persistence packages are imported.

---

## 8. Verification and Test Evidence

| Test Suite | Command | Result | Coverage Highlights |
| :--- | :--- | :---: | :--- |
| **State Machine & Lifecycle** | `go test -v ./internal/deployment -run "TestEvaluateTransition\|TestCanTransition\|TestStatusRank"` | **PASS (100%)** | Valid transitions, terminal immutability, out-of-order webhook rejection, idempotent events |
| **Boundary Protection** | `go test -v ./internal/deployment -run "TestZeroDependencyBoundary"` | **PASS (100%)** | Verified zero forbidden imports across platform packages |
| **Provider Registry** | `go test -v ./internal/integrations/deployment -run "TestRegistry"` | **PASS (100%)** | Registration, capability checks, concurrency safety, error translation |
| **Secret & Signature Policies** | `go test -v ./internal/deployment -run "TestMaskToken\|TestVerify"` | **PASS (100%)** | Token masking, HMAC SHA1 / SHA256 constant-time verification |

---

## 9. Consequences and Trade-offs

### Positive
- **Clean Upstream Mergeability:** Self-contained architecture allows `caglar09/multica:feat/deployment-providers-core` to be merged upstream into `multica-ai/multica:main` with zero resistance.
- **Provider Extensibility:** New providers (Render, Railway, Heroku) implement only `deployment.Provider` and register with `deployment.Registry`.
- **Fault Tolerance:** Out-of-order webhooks, duplicate deliveries, and process crashes are absorbed safely by the state machine and bounded reconciler.

### Negative / Trade-offs
- **Application-Level Referential Integrity:** Without database foreign keys, dependent target cleanup must be handled explicitly in application transaction logic.
- **Two-Phase Webhook Ingestion:** Ingesting webhooks into `deployment_event` before mutating `deployment_operation` increases write amplification slightly, but provides an audit trail and deduplication guarantee.
