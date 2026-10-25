# Deployment Providers Platform Scope and Acceptance Criteria

**Document Version:** 1.0.0  
**Status:** Approved Specification  
**Role:** Product Manager (`product_manager`)  
**Scope Identifier:** `[autonomous-project-node:deployment_platform_spec]`  
**Target Upstream PR:** `multica-ai/multica:main` from `caglar09/multica:feat/deployment-providers-core`

---

## 1. Executive Summary & Product Vision

The **Deployment Providers Platform** establishes a first-class, provider-neutral deployment control plane inside Multica. It allows engineering teams and automated systems to connect external deployment providers (starting with Vercel as the P0 connector), map external projects and environments to Multica projects as deployment targets, trigger explicit source revision deployments asynchronously, observe external lifecycles through cryptographically signed webhooks and bounded polling reconciliation, and capture auditable evidence and build logs.

Crucially, this subsystem is architected as a **generic, self-contained platform capability** of Multica Core. It operates independently of any specific autonomous workflow engine, preserving strict architectural separation so that upstream contributors can merge the platform capability without carrying experimental autonomous execution dependencies.

---

## 2. Architectural & Functional Separation: Generic Platform vs. Autonomous Project OS

To satisfy upstream architectural standards and prevent dependency contamination, a strict structural boundary is enforced between the **Multica Generic Deployment Platform** and the **Autonomous Project OS**.

```
+-----------------------------------------------------------------------------+
|                                Consumers                                    |
|  +--------------------+  +----------------------+  +---------------------+  |
|  | Web / Desktop UI   |  | CI / CLI Automation  |  | Autonomous Project  |  |
|  | (Settings & Detail)|  | (API Triggers)       |  | OS (Scheduler/Bot)  |  |
|  +---------+----------+  +----------+-----------+  +----------+----------+  |
+------------|------------------------|-------------------------|-------------+
             |                        |                         |
             +--------------------+   |   +---------------------+
                                  |   |   |
                                  v   v   v
+-----------------------------------------------------------------------------+
|                   Multica Generic Deployment Platform                       |
|                                                                             |
|  +-----------------------------------------------------------------------+  |
|  | Public Boundary & Service Layer (server/internal/deployment)          |  |
|  | - Target & Connection Management                                      |  |
|  | - Asynchronous Execution Engine & Lease Dispatch                      |  |
|  | - Normalized State Machine (Terminal state immutability)              |  |
|  | - Idempotency & Deduplication Engine                                  |  |
|  | - Signed Webhook Ingestion & Bounded Reconciler                       |  |
|  +-----------------------------------+-----------------------------------+  |
|                                      |                                      |
|                                      v                                      |
|  +-----------------------------------------------------------------------+  |
|  | Provider Adapter Registry (server/internal/integrations/deployment)   |  |
|  | - Capability Discovery Contract (trigger, status, cancel, logs)       |  |
|  | - Vercel P0 Connector (OAuth/Token, Project Discovery, Webhooks)       |  |
|  | - Future Providers (Render, Heroku, Railway)                          |  |
|  +-----------------------------------+-----------------------------------+  |
|                                      |                                      |
|                                      v                                      |
|  +-----------------------------------------------------------------------+  |
|  | Durable Persistence (Application-level referential integrity, no FKs) |  |
|  | - deployment_provider_connection                                      |  |
|  | - deployment_target                                                   |  |
|  | - deployment_operation                                                |  |
|  | - deployment_event                                                    |  |
|  +-----------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------+
```

### 2.1 Package and Dependency Isolation Rules
1. **Zero Upstream Contamination:**
   The generic deployment engine located in `server/internal/deployment` and adapter implementations in `server/internal/integrations/deployment` **MUST NEVER** import or reference packages belonging to Autonomous Project OS (e.g., `server/internal/projectorchestration`, `server/internal/workflowruntime`, or autonomous state models).
2. **Downward Dependency Flow:**
   Autonomous Project OS may consume the deployment service via its exported public Go interfaces (`deployment.Service`) and REST APIs, treating it as an external dependency. The deployment platform has zero knowledge of callers.
3. **Storage Segregation:**
   The deployment engine persists only its own domain tables (`deployment_provider_connection`, `deployment_target`, `deployment_operation`, `deployment_event`). It does not store workflow nodes, prompt snapshots, plan trees, or autonomous graph states. If an autonomous agent triggers an operation, it records the returned `operation_id` in its own workflow execution logs.
4. **Deterministic Security Boundary:**
   Autonomous agents cannot bypass authorization rules, confirmation requirements, or secret isolation guarantees. Every deployment request must supply a valid caller token and approval reference where required.

---

## 3. Provider-Neutral Core Domain & Capability Matrix

The platform models external deployment services uniformly through capability contracts. Providers expose what they can and cannot do; the core engine enforces validation before attempting dispatch.

### 3.1 Domain Model
- **`deployment_provider_connection`**: Workspace-scoped provider account authentication. Stores metadata, team/account identifiers, and an encrypted secret reference. Credentials are write-only and encrypted via `crypto/secretbox`.
- **`deployment_target`**: Connects a Multica project to an external provider project/service. Defines the target environment (`preview`, `production`, `staging`), branch mapping, root directory, and build parameters.
- **`deployment_operation`**: An immutable, durable requested execution. Tracks idempotency keys, state transitions, lease claims, retry wait backoff, commit references, and captured external evidence.
- **`deployment_event`**: Append-only audit ledger of provider webhooks and polling observations. Deduplicated on `(provider, provider_event_id)`.

### 3.2 Provider Capability Contract
Every provider adapter implements a typed capability query:
```go
type Capability string

const (
    CapTrigger         Capability = "trigger"          // Can trigger deployment from source ref
    CapStatus          Capability = "status"           // Supports external status polling
    CapCancel          Capability = "cancel"           // Can abort an in-flight deployment
    CapWebhook         Capability = "webhook"          // Emits push webhooks
    CapEvidence        Capability = "evidence"         // Yields deployment URLs, inspect links, metadata
    CapLogs            Capability = "logs"             // Streams or fetches build/runtime logs
    CapPromote         Capability = "promote"          // Can promote a preview deployment to production
    CapRollback        Capability = "rollback"         // Can revert production to a previous deployment
    CapTargetDiscovery Capability = "target_discovery" // Enumerates remote projects and environments
    CapTargetCreate    Capability = "target_create"    // Can provision a new remote project
)
```

### 3.3 Provider Capability Matrix
If a consumer requests an operation that requires a capability the provider does not support, the core immediately rejects the operation with a typed error (`ErrCapabilityUnsupported`) before writing durable rows.

| Capability | Vercel (P0) | Render (P1) | Heroku (P2) | Railway (P2) | Custom / Fake |
| :--- | :---: | :---: | :---: | :---: | :---: |
| `trigger` | **Yes** | Yes | Yes | Yes | Yes |
| `status` | **Yes** | Yes | Yes | Yes | Yes |
| `cancel` | **Yes** | Yes | No | Yes | Yes |
| `webhook` | **Yes** (Signed HMAC) | Yes | Yes | Yes | Yes |
| `evidence` | **Yes** | Yes | Yes | Yes | Yes |
| `logs` | **Yes** | Yes | Yes | Yes | Yes |
| `promote` | **Yes** (Alias migration) | No | Yes (Pipeline promote) | No | Yes |
| `rollback` | **Yes** (Instant rollback) | Yes (Sync deploy) | Yes (Release rollback) | Yes | Yes |
| `target_discovery` | **Yes** | Yes | Yes | Yes | Yes |
| `target_create` | **Yes** | Yes | No | Yes | Yes |

---

## 4. Authorization Matrix & Confirmation Dialog Specifications

Deployments touch live production systems, critical domains, and shared cloud resources. Unintended or automated actions must be gated by explicit human approvals and role-based access control.

### 4.1 Role-Based Access Control (RBAC) Matrix

| Action | Workspace Owner | Workspace Admin | Project Member | Viewer | Autonomous Agent (Bot) |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **Connect Provider** | Allowed | Allowed | Denied | Denied | Denied |
| **Revoke Connection** | **Allowed (L3 Dialog)** | **Allowed (L3 Dialog)** | Denied | Denied | Denied |
| **Create Target** | Allowed (L2 Dialog) | Allowed (L2 Dialog) | Allowed (L2 Dialog) | Denied | **Blocked without Human Token** |
| **Update Target** | Allowed | Allowed | Allowed | Denied | Denied |
| **Delete Target** | Allowed (L2 Dialog) | Allowed (L2 Dialog) | Denied | Denied | Denied |
| **Deploy Preview** | Allowed | Allowed | Allowed | Denied | Allowed (Self-service) |
| **Deploy Production** | **Allowed (L2 Dialog)** | **Allowed (L2 Dialog)** | Allowed* (Policy gated) | Denied | **Blocked without Approval** |
| **Promote to Prod** | **Allowed (L3 Dialog)** | **Allowed (L3 Dialog)** | Denied | Denied | **Blocked** |
| **Rollback Prod** | **Allowed (L3 Dialog)** | **Allowed (L3 Dialog)** | Denied | Denied | **Blocked** |
| **Cancel In-Flight** | Allowed (L1 Dialog) | Allowed (L1 Dialog) | Allowed (L1 Dialog) | Denied | Allowed (Originating run only) |
| **View Secrets** | **NEVER** (Write-only) | **NEVER** (Write-only) | **NEVER** | **NEVER** | **NEVER** |

*\*Note: Project Member production deployment can be disabled workspace-wide via policy.*

---

### 4.2 Confirmation Friction Levels & Dialog Specifications

User interactions are categorized into three standardized friction tiers:
1. **Level 1 (Standard Notice):** Single-click acknowledgment for low-risk actions.
2. **Level 2 (Structured High-Friction):** Explicit modal presenting environment diff, commit SHA, and active traffic impact.
3. **Level 3 (Critical Destructive / Typed Acknowledgment):** Two-step modal requiring the user to type a confirmation phrase (e.g., project name, "ROLLBACK", or "REVOKE").

---

#### 4.2.1 Production Deploy Confirmation Dialog (Level 2)
- **Trigger:** Initiating a deployment targeting an environment marked `production`.
- **UI Elements:**
  - Header: ⚠️ **Deploy to Production**
  - Alert Banner: *"You are about to deploy revision `<commit_sha>` to the live production target `<target_name>`."*
  - Context Grid:
    - Target: `Production - Web App`
    - Provider: `Vercel (Team: Acme Corp)`
    - Git Reference: `refs/heads/main` (`a9e82c7`)
    - Initiator: `Çağlar Yıldırım (Admin)`
  - Confirmation Checkbox: `[ ] I understand this will immediately affect live production traffic.`
  - Buttons: `[ Cancel ]` (Neutral) | `[ Confirm & Deploy ]` (Danger Primary, disabled until checkbox checked).
- **Backend Token Flow:** Upon submission, the UI obtains an ephemeral `approval_reference` token (HMAC signed with timestamp, valid for 60 seconds). The backend validates this token before transitioning operation state to `queued`.

---

#### 4.2.2 Promote Preview to Production Dialog (Level 3)
- **Trigger:** Requesting promotion of an existing successful preview deployment to production.
- **UI Elements:**
  - Header: 🚀 **Promote Preview Deployment to Production**
  - Comparison View:
    - Active Production: Deploy `#dep_102` (`f48b91a` deployed 2 days ago)
    - Candidate Preview: Deploy `#dep_109` (`b89be51` deployed 10 minutes ago)
    - Candidate Preview URL: `https://multica-preview-b89be51.vercel.app` (clickable test link)
  - Verification Checkbox: `[ ] I have verified the candidate preview URL in a staging environment.`
  - Type-to-Confirm Input: *"Type the target name `<target_name>` to confirm promotion:"*
  - Buttons: `[ Cancel ]` | `[ Promote to Live Production ]` (Disabled until input exactly matches).

---

#### 4.2.3 Production Rollback Dialog (Level 3)
- **Trigger:** Requesting an instant rollback to a historical production deployment.
- **UI Elements:**
  - Header: 🚨 **Emergency Production Rollback**
  - Impact Statement: *"Rolling back will revert production traffic from `#dep_109` back to `#dep_102`."*
  - Summary Table: Shows target commit, author, deployment date, and health status of target historical release.
  - In-Flight Safety Warning: *"Any currently building or queued deployments for this target will be automatically canceled."*
  - Type-to-Confirm Input: *"Type `ROLLBACK` to authorize instant traffic redirection:"*
  - Buttons: `[ Cancel ]` | `[ Authorize Emergency Rollback ]` (Red destructive button).

---

#### 4.2.4 Target Creation Dialog (Level 2)
- **Trigger:** Mapping an external provider project to a Multica project.
- **UI Elements:**
  - Header: **Connect Deployment Target**
  - Selectors:
    - Provider Connection: Dropdown of active connections (e.g. `Vercel - Production Team`).
    - Remote Project: Searchable dropdown populated via provider `target_discovery` API.
    - Environment Type: Radio selection `[ Preview / Staging ]` vs `[ Production ]`.
  - Production Guard: If `Production` is selected, an explicit disclosure appears: *"Warning: Authorizing this target allows production deployments directly from Multica issues and workflows."*
  - Buttons: `[ Cancel ]` | `[ Link Target ]`.

---

#### 4.2.5 Connection Revocation Dialog (Level 3)
- **Trigger:** Removing a workspace provider connection.
- **UI Elements:**
  - Header: 🗑️ **Revoke Deployment Provider Connection**
  - Destructive Warning: *"Revoking `<connection_name>` will permanently delete all stored credentials and disable `<N>` connected deployment targets. In-flight deployments will fail."*
  - Type-to-Confirm Input: *"Type `REVOKE` to permanently delete these credentials:"*
  - Buttons: `[ Cancel ]` | `[ Revoke and Shred Credentials ]`.
- **Backend Behavior:** Instantly shreds the SecretBox encrypted ciphertext, marks connection `revoked`, and cascades target status to `disabled` in an atomic database transaction.

---

## 5. Vercel P0 Feature Checklist

The Vercel connector serves as the reference P0 implementation, validating the provider-neutral architecture.

### 5.1 Vercel P0 Capabilities
- [x] **Authentication & Connection:**
  - Supports Vercel Personal Access Tokens and OAuth 2.0 access tokens.
  - Workspace-scoped connection management with team ID / user ID binding.
  - Cryptographic validation of tokens via `GET https://api.vercel.com/v2/user` before saving.
  - SecretBox at-rest encryption; write-only API returns masked identifiers (`vercel_tok_****`).
- [x] **Resource Discovery & Target Mapping:**
  - Discovers Vercel teams via `GET https://api.vercel.com/v2/teams`.
  - Discovers Vercel projects via `GET https://api.vercel.com/v9/projects`.
  - Maps Vercel project ID + team ID to Multica `deployment_target`.
- [x] **Deployment Execution:**
  - Triggers preview deployment via `POST https://api.vercel.com/v13/deployments` with git metadata (commit SHA, branch, message).
  - Triggers production deployment (`target: "production"`).
  - Asynchronous dispatch returning local `operation_id` in `< 150ms`.
  - Supports deployment cancellation via `PATCH https://api.vercel.com/v12/deployments/:id/cancel`.
  - Supports instant rollback via Vercel rollback API / alias re-pointing.
- [x] **Signed Webhook Gateway:**
  - Webhook endpoint receiving Vercel event payloads.
  - HMAC SHA1 signature verification via `x-vercel-signature` and configured webhook secret.
  - Deduplication on `(provider="vercel", provider_event_id=payload.id)`.
  - Mapped event lifecycle:
    - `deployment.created` -> `queued` / `dispatching`
    - `deployment.building` -> `building`
    - `deployment.ready` -> `succeeded` (captures live URL and alias)
    - `deployment.error` -> `failed` (captures error message and inspect URL)
    - `deployment.canceled` -> `canceled`
- [x] **Polling Reconciliation Loop:**
  - Background reconciler queries `GET https://api.vercel.com/v13/deployments/:id` for uncompleted operations.
  - Exponential backoff (10s, 30s, 60s, 120s up to 15m timeout).
  - Automatically recovers from missed webhooks or network partitions.
- [x] **Evidence & Log Extraction:**
  - Captures `url` (e.g. `https://my-app-git-feat-team.vercel.app`), `inspectorUrl`, `buildTime`, and error diagnostics.
  - Redacts sensitive environment variables and tokens from all evidence JSON structures.

---

## 6. Implementation Roadmap: Phase 0 Through Phase 8 Exit Gates

The project is structured into 9 strictly phased gates. No phase may be marked complete without meeting its objective, automated verification criteria.

```
Phase 0 [Upstream Sync & Architecture Foundation]
   │
   ▼
Phase 1 [Domain, Database Schema & State Machine]
   │
   ▼
Phase 2 [Provider Contract, Registry & Fake Provider]
   │
   ▼
Phase 3 [Asynchronous Engine, Webhook Gateway & Reconciler]
   │
   ▼
Phase 4 [Generic REST API & Frontend UI Platform]
   │
   ▼
Phase 5 [Vercel P0 Connection & Target Discovery]
   │
   ▼
Phase 6 [Vercel Deployment Lifecycle & Webhooks]
   │
   ▼
Phase 7 [Repository Analysis & Deployment Recipes]
   │
   ▼
Phase 8 [Security Hardening & Upstream PR Submission]
```

---

### Phase 0: Upstream Sync & Architecture Foundation
- **Goal:** Align local worktree with upstream `multica-ai/multica:main` and establish architectural rules.
- **Deliverables:**
  1. Clean git merge from `upstream/main` into `feat/deployment-providers-core`.
  2. Architectural Decision Record (`docs/architecture/ADR-0001-deployment-provider-core.md`) accepted.
  3. Product Scope & Acceptance Criteria (`docs/architecture/deployment-platform-scope-and-acceptance.md`) approved.
- **Objective Exit Gate Criteria:**
  - `git merge-base --is-ancestor upstream/main HEAD` exits 0.
  - ADR and Scope specifications committed and reviewed.
  - Zero compiler errors or untracked legacy artifacts.

---

### Phase 1: Domain, Database Schema & State Machine
- **Goal:** Establish durable data model and deterministic state transition logic.
- **Deliverables:**
  1. SQL migration files 469–474 creating tables `deployment_provider_connection`, `deployment_target`, `deployment_operation`, `deployment_event` and their indexes.
  2. Concurrent unique index for idempotency: `deployment_operation(workspace_id, idempotency_key)`.
  3. Concurrent unique index for deduplication: `deployment_event(provider, provider_event_id)`.
  4. Go domain models (`server/internal/deployment/model.go`) and state machine (`server/internal/deployment/state.go`).
- **Objective Exit Gate Criteria:**
  - Migration follows Multica rules: zero foreign key constraints, explicit application-level cascade cleanup.
  - Database schema tests verify atomic inserts, reads, and updates.
  - State machine tests verify:
    - Legal transitions: `queued -> dispatching -> running -> succeeded`.
    - Terminal state immutability: `succeeded`, `failed`, `canceled` cannot transition to any other state.
    - Out-of-order event rejection: an earlier state observation cannot regress a later state.
  - `go test -v ./server/internal/deployment -run "TestState|TestPostgres"` passes 100%.

---

### Phase 2: Provider Contract, Registry & Deterministic Fake Provider
- **Goal:** Build the provider abstraction layer and a fake provider for deterministic testing.
- **Deliverables:**
  1. Provider interface defining `Capabilities()`, `ValidateConnection()`, `DiscoverTargets()`, `TriggerDeployment()`, `GetStatus()`, `CancelDeployment()`, `VerifyWebhook()`.
  2. Thread-safe provider registry (`server/internal/integrations/deployment/registry.go`).
  3. Comprehensive `FakeProvider` supporting scripted delays, simulated network drops, mock webhooks, and failure modes.
- **Objective Exit Gate Criteria:**
  - Registry permits dynamic registration and lookup by provider slug.
  - Invocations requiring missing capabilities immediately return `ErrCapabilityUnsupported`.
  - Provider contract test suite passes against `FakeProvider` under zero real network I/O.

---

### Phase 3: Asynchronous Engine, Webhook Gateway & Reconciler
- **Goal:** Implement the execution engine that manages durable leases, processes webhooks, and recovers from crashes.
- **Deliverables:**
  1. Asynchronous dispatcher: claims pending operations using time-bounded optimistic leases (`lease_expires_at`, `revision`).
  2. Webhook gateway: verifies signatures, deduplicates payloads, records raw events, and applies state transitions.
  3. Background reconciler: scans non-terminal operations past polling thresholds and queries provider status.
- **Objective Exit Gate Criteria:**
  - Non-blocking trigger endpoint returns `operation_id` in `< 200ms`.
  - Duplicate webhook delivery (`same provider_event_id`) produces exactly one state transition and records duplicate as no-op.
  - Simulated backend crash test: mid-flight operation with expired lease is automatically re-claimed and reconciled to terminal state by recovery worker within 30 seconds.

---

### Phase 4: Generic REST API & Frontend UI Platform
- **Goal:** Provide clean, authenticated REST endpoints and intuitive shared UI components for web and desktop apps.
- **Deliverables:**
  1. Go HTTP endpoints under `/api/workspaces/:wsId/deployment-providers/...` and `/api/projects/:projId/deployments/...`.
  2. Headless hooks and React Query mutations in `packages/core/`.
  3. Settings UI in `packages/views/` for managing workspace connections and credentials.
  4. Project Deployments Timeline component displaying active builds, commit SHA, environment tags, and live URLs.
  5. Confirmation dialog modals adhering to Level 1, 2, and 3 friction specifications.
- **Objective Exit Gate Criteria:**
  - API schemas contain zero plaintext credentials in request responses.
  - Package boundary rules respected: zero `@multica/core` imports in `packages/ui/`, zero `next/*` in `packages/views/`.
  - `pnpm typecheck` and `pnpm lint` pass with zero errors across all workspaces.
  - Playwright test verifies production deployment confirmation modal requires explicit checkbox and button click.

---

### Phase 5: Vercel P0 Connection & Target Discovery
- **Goal:** Integrate real Vercel API authentication and resource discovery.
- **Deliverables:**
  1. Vercel HTTP client with rate-limiting, retry handling, and SecretBox token management.
  2. Connection validation endpoint verifying token against Vercel `/v2/user`.
  3. Target discovery endpoint enumerating Vercel teams and projects.
- **Objective Exit Gate Criteria:**
  - SecretBox encryption unit tests prove secrets are never stored in plaintext in the database.
  - Mocked Vercel API tests verify pagination and error translation (e.g. 401 Unauthorized -> `ErrInvalidCredentials`).
  - Validation endpoint cleanly rejects expired or insufficient-scope tokens before creating database rows.

---

### Phase 6: Vercel Deployment Lifecycle & Signed Webhooks
- **Goal:** End-to-end deployment triggering, webhook ingestion, and live evidence tracking for Vercel.
- **Deliverables:**
  1. Vercel deployment trigger adapter mapping Multica target config to Vercel `/v13/deployments`.
  2. Vercel webhook signature validator (`x-vercel-signature` HMAC verification).
  3. Webhook parser converting Vercel event JSON into normalized deployment events.
  4. Live evidence extraction capturing deployment URL and Vercel inspect link.
- **Objective Exit Gate Criteria:**
  - Webhook replay test verifies tampered signatures return HTTP 401 and are rejected.
  - Valid webhook payloads progress deployment from `queued` to `building` to `succeeded`.
  - Reconciler successfully resolves stuck deployment if webhooks are blocked.
  - Integration test runs full lifecycle (Trigger -> Mock Webhook -> Succeeded) with all evidence captured.

---

### Phase 7: Repository Analysis & Framework Deployment Recipes
- **Goal:** Assist users with automated repository scanning and recommended deployment targets.
- **Deliverables:**
  1. Local repository scanner inspecting project files (`package.json`, `next.config.js`, `vite.config.ts`, `Dockerfile`).
  2. Recommendation engine outputting suggested framework preset, root directory, and build commands.
  3. Non-intrusive UI suggestions during target creation.
- **Objective Exit Gate Criteria:**
  - Correctly detects Next.js, Vite, Remix, Astro, and Go web servers in test fixtures.
  - Zero automated modifications made to user git repositories.
  - Analysis runs strictly read-only and completes in `< 500ms`.

---

### Phase 8: Security Hardening & Upstream Contributor PR Ready
- **Goal:** Final security verification, full pipeline validation, and clean PR packaging for upstream submission.
- **Deliverables:**
  1. Comprehensive secret isolation audit: verifying no tokens appear in DB logs, Sentry, terminal output, or API bodies.
  2. Upstream PR branch preparation with clean git commit history.
  3. Upstream contributor documentation and PR description.
- **Objective Exit Gate Criteria:**
  - `make check` passes completely (Go tests, linting, formatting, TS typecheck, unit tests).
  - Security audit: Grep of repository and test logs confirms zero plaintext secret leaks.
  - Upstream branch check: `git diff upstream/main...HEAD` contains ONLY generic deployment platform code and ZERO autonomous runtime dependencies.
  - Contributor PR checklist completed and verified.

---

## 7. Secret Isolation & Security Architecture

Security of third-party credentials is paramount. A security compromise of provider tokens could grant attackers full access to production infrastructure.

### 7.1 SecretBox Encryption at Rest
All sensitive provider configuration attributes (Personal Access Tokens, OAuth refresh tokens, webhook signing secrets) are encrypted before writing to PostgreSQL using `crypto/secretbox` (XSalsa20-Poly1305 with 24-byte random nonces) using the workspace encryption key.

```
[Plaintext Token] 
       │
       ▼
[Generate 24-byte random nonce]
       │
       ▼
[SecretBox.Seal(nonce, plaintext, workspace_secret)]
       │
       ▼
[Base64(nonce + ciphertext)] ──► Saved to PostgreSQL `auth_config` column
```

### 7.2 Zero Plaintext Exposure Rules
1. **Write-Only API:** 
   APIs accept plaintext credentials during creation or update, but **NEVER** return them. API responses only return masked hints (`vercel_tok_****`), token prefix, expiration, and validation status.
2. **Log Redaction:**
   Standard logging middleware sanitizes authorization headers, request bodies matching credential patterns, and error traces before outputting to stdout or telemetry.
3. **LLM / AI Boundary:**
   Autonomous agents, prompt templates, and AI context assemblers are completely shielded from credential payloads. The deployment subsystem provides execution handles (`operation_id`, `target_id`), never credential objects.
4. **Memory Scrubbing:**
   Plaintext byte buffers used during adapter HTTP requests are zeroed immediately after use.

---

## 8. Reliability, Idempotency & Bounded Reconciliation

External cloud APIs are subject to network drops, transient 5xx errors, rate limits, and delayed or out-of-order webhook delivery. The platform guarantees deterministic execution.

### 8.1 Operation Idempotency Key
Every deployment trigger requires or computes an idempotency key:
$$\text{IdempotencyKey} = \text{SHA256}(\text{ws\_id} + \text{target\_id} + \text{source\_ref} + \text{env} + \text{op\_kind} + \text{approval\_ref})$$
If a client or automated caller retries an identical trigger request, the platform returns the existing active or completed `deployment_operation` without spawning a secondary external deployment.

### 8.2 Event Deduplication
Incoming webhooks are deduplicated at the database boundary:
- A unique index on `deployment_event(provider, provider_event_id)` ensures that duplicate webhook deliveries are inserted once.
- Subsequent deliveries of the same event ID are acknowledged with HTTP 200 OK to satisfy the provider while skipping state machine execution.

### 8.3 Terminal State Immutability
The normalized state machine enforces:
$$\text{Terminal States} = \{\text{succeeded}, \text{failed}, \text{canceled}\}$$
Once an operation enters a terminal state, **no future event, delayed webhook, or background polling observation may alter that state**. Late observations are recorded in `deployment_event` for audit purposes but do not mutate `deployment_operation.status`.

### 8.4 Bounded Reconciliation Loop
To handle lost webhooks or backend process restarts:
- Non-terminal operations are tracked with an exponential backoff interval: $10\text{s}, 30\text{s}, 60\text{s}, 120\text{s}, \dots$ up to a maximum operation timeout of 15 minutes.
- If an operation exceeds the 15-minute deadline without a terminal webhook or status update, the reconciler queries the provider one final time. If still unresolved, it transitions the operation to `failed` with diagnostic reason `TIMEOUT_EXCEEDED`.
- Process crash recovery: Every dispatch lease has a 60-second TTL. If a worker dies, another worker automatically claims the lease after expiry.

---

## 9. Conclusion and Next Steps

This specification establishes the definitive product boundaries, security models, confirmation flows, and phased acceptance gates for the Deployment Providers Platform. All subsequent engineering implementation issues (Phases 1 through 8) must strictly adhere to these criteria.
