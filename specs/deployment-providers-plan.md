# Deployment Providers Platform Plan

**Status:** Proposed implementation plan  
**Date:** 2026-09-13  
**Base branch:** `main`  
**Initial provider:** Vercel  
**Future providers:** Render, Heroku, Railway, and other provider connectors  
**Scope:** Add deployment as a first-class Multica platform capability that works independently of Autonomous Project OS and can later be consumed by it.

## 1. Purpose

Multica should be able to connect a deployment provider, attach a provider-side project/service to a Multica project, deploy an explicit source revision, observe the external deployment lifecycle, and surface evidence, status, URLs, logs, and rollback/promote capabilities without coupling the feature to a specific deployment vendor or to Autonomous Project OS.

The first implementation is Vercel, but the architecture must make Render, Heroku, Railway, and future providers additive connectors rather than new deployment engines.

This plan is intentionally implemented from `main`. The existing deployment work discussed on `feat/autonomous-project-os` is source material only; its autonomous-specific persistence and runtime ownership must not leak into the generic deployment platform.

## 2. Core architectural decision

Deployment is a Multica platform subsystem, not an Autonomous Project OS subsystem.

The ownership model is:

```text
Multica Core
└── Deployment Platform
    ├── Provider connections
    ├── Deployment targets
    ├── Deployment operations
    ├── Provider events
    ├── Webhook verification
    ├── Reconciliation
    ├── Capability discovery
    └── Provider registry
        ├── Vercel
        ├── Render
        ├── Heroku
        ├── Railway
        └── Custom/provider-specific implementations

Consumers
├── Standard project UI
├── Manual deploy API
├── Future CLI commands
├── Future release workflows
└── Autonomous Project OS
```

A deployment provider implementation must never import or depend on `projectorchestration`, `workflowruntime`, `autonomous_project_*` persistence, autonomous policy objects, or autonomous UI state.

Autonomous Project OS will later call the same generic Deployment Service used by standard projects.

## 3. Product outcome

An authorized user can:

1. Connect a deployment provider account/team/workspace.
2. Verify the connection without exposing credentials.
3. Discover accessible provider resources.
4. Attach an existing provider project/service to a Multica project.
5. Where supported, create a provider project/service after an explicit confirmation.
6. Configure a deployment target with repository/ref, environment, root directory, and provider-specific validated settings.
7. Trigger a preview deployment for an explicit source revision.
8. Trigger or promote to production only after a separate production confirmation.
9. Observe queued/building/deploying/ready/failed/cancelled states without blocking an HTTP request until completion.
10. View provider deployment IDs, deployment URLs, provider dashboard links, timestamps, safe log summaries, and evidence.
11. Retry, cancel, promote, restart, or roll back only when the provider advertises that capability and Multica policy allows it.
12. Disconnect a provider without deleting historical deployment evidence.

AI may inspect repository structure and recommend configuration, but provider access tokens, OAuth refresh tokens, webhook secrets, and secret environment-variable values must never be exposed to the model context.

## 4. Explicit non-goals for V1

The first release does not automatically:

- create managed databases;
- purchase or configure domains;
- edit DNS;
- provision arbitrary cloud infrastructure;
- infer and write secret environment-variable values;
- upload arbitrary local working trees as provider build sources;
- auto-rollback production after a failure;
- treat a successful provider build as proof that the application is healthy;
- make deployment dependent on Autonomous Project OS being enabled.

These can be added later as separately permissioned capabilities.

## 5. Design principles

### 5.1 Provider-neutral core

Core domain types must describe deployment concepts, not Vercel concepts. Terms such as `project`, `service`, `app`, `release`, `build`, `deployment`, and `environment` differ across providers.

Multica should normalize only the concepts it needs to orchestrate safely:

- provider connection;
- deployment target;
- deployment operation;
- operation state;
- source revision;
- environment intent;
- provider capability;
- provider event;
- deployment evidence.

Provider-specific details stay in provider adapters.

### 5.2 Capability-based extensibility

Do not require every provider to implement every operation.

Vercel may support preview and promotion semantics. Heroku has build/release semantics. Railway exposes restart/redeploy/rollback/cancel operations. Render centers deployments around services.

The UI and service layer must use provider capabilities instead of `if provider == ...` branches.

### 5.3 Asynchronous by default

A provider deployment is an external long-running operation. API handlers must not wait for terminal provider state.

Trigger flow:

```text
request
  -> create local deployment operation
  -> enqueue/dispatch provider trigger
  -> receive external operation ID
  -> return current state
  -> advance through signed webhook events
  -> reconcile through bounded polling when events are missing
  -> terminal state
```

### 5.4 Durable and idempotent

Backend restarts, duplicate callbacks, delayed callbacks, retries, and missing callbacks must not create duplicate deployments or regress terminal state.

### 5.5 Secret isolation

Credential storage, decryption, provider calls, and webhook verification are backend-only responsibilities.

### 5.6 Explicit production boundary

Preview and production are separate user intents. A successful preview does not imply permission to promote or deploy production.

## 6. Package boundaries

Recommended backend layout:

```text
server/internal/deployment/
├── domain.go
├── capabilities.go
├── service.go
├── repository.go
├── state_machine.go
├── dispatcher.go
├── reconciler.go
├── webhook.go
├── evidence.go
└── errors.go

server/internal/integrations/deployment/
├── registry.go
├── provider.go
├── fake/
│   └── provider.go
├── vercel/
│   ├── client.go
│   ├── provider.go
│   ├── auth.go
│   ├── targets.go
│   ├── deployments.go
│   ├── webhooks.go
│   ├── mapper.go
│   └── provider_test.go
├── render/        # future
├── heroku/        # future
└── railway/       # future
```

Handlers should depend on `deployment.Service`, not concrete providers.

Provider packages should implement interfaces from the deployment integration boundary and should not know about HTTP handlers, UI routes, or autonomous workflow nodes.

## 7. Domain model

### 7.1 Provider connection

A provider connection represents a workspace-scoped authorization/account context.

Examples:

- Vercel user/team authorization;
- Render workspace authorization;
- Heroku account/team authorization;
- Railway workspace authorization.

A connection does not represent a repository-specific deployment target.

Suggested fields:

```text
deployment_provider_connection
--------------------------------
id
workspace_id
provider
external_account_id
external_scope_id
external_scope_name
display_name
auth_mode
credential_ciphertext
credential_key_version
status
last_validated_at
last_error_code
metadata
created_by
created_at
updated_at
revoked_at
```

Rules:

- credentials are write-only from API/UI perspective;
- plaintext credentials are never returned after creation;
- validation happens before a connection becomes active;
- revocation preserves redacted historical deployment records;
- provider metadata is allow-listed and must never contain tokens.

### 7.2 Deployment target

A deployment target maps a Multica project to a provider-side resource and deployment policy/configuration.

Suggested fields:

```text
deployment_target
-----------------
id
workspace_id
project_id
connection_id
provider
provider_resource_id
provider_resource_name
environment
source_repository
source_branch_policy
root_directory
framework
build_command
output_directory
health_url
provider_config_version
provider_config
status
created_by
created_at
updated_at
archived_at
```

Rules:

- target configuration contains no provider credentials;
- common fields are first-class columns;
- provider configuration is narrowly versioned and validated by the provider implementation;
- there is no unrestricted generic settings blob accepted directly from clients;
- target connection and project must belong to the same workspace.

### 7.3 Deployment operation

Every deploy/promote/rollback/cancel/restart request is a durable operation.

Suggested fields:

```text
deployment_operation
--------------------
id
workspace_id
project_id
target_id
provider
kind
environment
source_repository
source_ref
source_revision
provider_operation_id
provider_release_id
status
deployment_url
dashboard_url
trigger_type
triggered_by
approval_reference
idempotency_key
error_code
error_message
safe_evidence
started_at
finished_at
created_at
updated_at
```

Recommended operation kinds:

```text
deploy
promote
rollback
cancel
restart
redeploy
```

Only capability-supported kinds are exposed for a target.

### 7.4 Provider event

Provider callbacks are durable input records before they mutate authoritative deployment state.

Suggested fields:

```text
deployment_event
----------------
id
connection_id
target_id
operation_id
provider
provider_event_id
provider_event_type
normalized_event_type
external_operation_id
safe_payload
received_at
processed_at
processing_error
```

A uniqueness constraint on `(provider, provider_event_id)` or an equivalent provider-scoped identity prevents webhook replay from applying twice.

## 8. Provider contract

The provider contract should stay small and typed.

```go
type Provider interface {
    Kind() ProviderKind
    Capabilities() CapabilitySet

    ValidateConnection(
        ctx context.Context,
        credentials ConnectionCredentials,
    ) (ConnectionInfo, error)

    ListTargets(
        ctx context.Context,
        connection Connection,
    ) ([]RemoteTarget, error)

    GetTarget(
        ctx context.Context,
        connection Connection,
        externalID string,
    ) (RemoteTarget, error)

    CreateTarget(
        ctx context.Context,
        connection Connection,
        req CreateTargetRequest,
    ) (RemoteTarget, error)

    TriggerDeployment(
        ctx context.Context,
        req DeploymentRequest,
    ) (RemoteOperation, error)

    GetOperation(
        ctx context.Context,
        req OperationLookup,
    ) (RemoteOperation, error)

    ParseWebhook(
        ctx context.Context,
        req WebhookRequest,
    ) (ProviderEvent, error)
}
```

Optional interfaces:

```go
type PromotionProvider interface {
    Promote(context.Context, PromotionRequest) (RemoteOperation, error)
}

type RollbackProvider interface {
    Rollback(context.Context, RollbackRequest) (RemoteOperation, error)
}

type CancellationProvider interface {
    Cancel(context.Context, CancelRequest) (RemoteOperation, error)
}

type RestartProvider interface {
    Restart(context.Context, RestartRequest) (RemoteOperation, error)
}

type LogProvider interface {
    GetLogs(context.Context, LogRequest) (LogResult, error)
}
```

If a future provider does not support `CreateTarget`, `Rollback`, `Promotion`, or signed webhooks, the core engine must still work with its advertised subset.

## 9. Capability model

Suggested capability set:

```go
type CapabilitySet struct {
    CreateTarget bool
    Preview bool
    Production bool
    Promotion bool
    Rollback bool
    Cancellation bool
    Restart bool
    Redeploy bool
    Logs bool
    Webhooks bool
    Reconciliation bool
}
```

The API should expose effective capabilities for a target.

The UI renders actions from capabilities rather than provider names.

## 10. Normalized operation state machine

Recommended generic states:

```text
pending
approval_required
approved
trigger_queued
provider_queued
building
deploying
ready
succeeded
failed
cancelled
rejected
```

Rollback/promotion are separate operations instead of overloading one deployment row with a second lifecycle.

Rules:

- terminal states do not regress because an older callback arrives later;
- unknown provider states are retained as safe evidence but cannot silently mutate core state;
- state transitions are validated centrally;
- provider adapters map provider-specific lifecycle values to normalized states;
- the original provider status may be retained in an allow-listed diagnostic field.

## 11. Idempotency and correlation

Every externally mutating operation gets an idempotency key.

For deployment, derive it from stable identifiers such as:

```text
(target_id, source_revision, environment, operation_kind, approval_reference)
```

Requirements:

- repeated client requests with the same key return/reuse the existing operation;
- provider request retries must not create duplicate deployments where provider idempotency is available;
- the external operation ID is stored immediately when returned;
- callbacks correlate first by authenticated connection/scope plus provider operation ID;
- provider resource/team/project IDs are validated against the target before state mutation.

## 12. Security and credential custody

### 12.1 Reuse existing Multica secret properties

Multica already has integrations that encrypt provider tokens/secrets server-side. Deployment should reuse or extract the same security primitive rather than invent a provider-specific encryption implementation.

The deployment subsystem needs an abstraction similar to:

```go
type SecretBox interface {
    Seal([]byte) ([]byte, error)
    Open([]byte) ([]byte, error)
    KeyVersion() string
}
```

Cloud and self-host may use different backing implementations while preserving the same deployment service contract.

### 12.2 Hard rules

Provider credentials, refresh tokens, webhook secrets, and secret environment values:

- never enter LLM prompts;
- never appear in structured logs;
- never appear in API responses;
- never enter deployment evidence;
- never appear in frontend state after submission;
- are redacted from provider error payloads before persistence.

### 12.3 Authorization

At minimum:

| Action | Required authority | Confirmation |
| --- | --- | --- |
| Connect provider | Workspace admin | Explicit provider authorization/save |
| Reconnect provider | Workspace admin | Explicit save |
| Disconnect provider | Workspace admin | Destructive confirmation |
| Attach existing target | Workspace admin | Explicit save |
| Create provider target | Workspace admin | Dedicated side-effect confirmation |
| Preview deploy | Admin or future delegated deploy role | Revision + target/environment confirmation |
| Production deploy | Workspace admin | Dedicated production confirmation |
| Promote | Workspace admin | Dedicated production confirmation |
| Rollback | Workspace admin | Dedicated confirmation naming source/candidate |
| Secret mutation | Workspace admin | Future dedicated flow; not P0 |

## 13. API surface

Suggested backend APIs. Exact routing should follow existing Multica router conventions.

### Workspace-scoped provider connections

```text
GET    /workspaces/:workspaceID/deployment-connections
POST   /workspaces/:workspaceID/deployment-connections
GET    /workspaces/:workspaceID/deployment-connections/:connectionID
POST   /workspaces/:workspaceID/deployment-connections/:connectionID/validate
DELETE /workspaces/:workspaceID/deployment-connections/:connectionID
```

### Provider discovery

```text
GET  /workspaces/:workspaceID/deployment-connections/:connectionID/targets
POST /workspaces/:workspaceID/deployment-connections/:connectionID/targets
```

`POST` is only available if the provider supports target creation.

### Project deployment targets

```text
GET    /projects/:projectID/deployment-targets
POST   /projects/:projectID/deployment-targets
GET    /projects/:projectID/deployment-targets/:targetID
PATCH  /projects/:projectID/deployment-targets/:targetID
DELETE /projects/:projectID/deployment-targets/:targetID
```

### Deployment operations

```text
GET  /projects/:projectID/deployments
POST /projects/:projectID/deployments
GET  /projects/:projectID/deployments/:operationID
POST /projects/:projectID/deployments/:operationID/promote
POST /projects/:projectID/deployments/:operationID/rollback
POST /projects/:projectID/deployments/:operationID/cancel
POST /projects/:projectID/deployments/:operationID/restart
```

Action endpoints must enforce capability and authorization checks server-side even if the UI hides unsupported actions.

### Provider callbacks

```text
POST /webhooks/deployments/vercel/...
POST /webhooks/deployments/render/...
POST /webhooks/deployments/heroku/...
POST /webhooks/deployments/railway/...
```

Provider-specific webhook verification stays inside the provider adapter/gateway.

## 14. Durable execution model

The existing synchronous request/response pattern is not sufficient for cloud deployment lifecycles.

The generic engine must support:

1. local operation creation;
2. provider trigger dispatch;
3. durable retry after transient provider errors;
4. webhook-driven state advancement;
5. scheduled reconciliation for non-terminal operations;
6. terminal-state finalization;
7. provider error classification;
8. audit/evidence emission.

Do not hold an HTTP request open while Vercel/Render/Heroku/Railway builds complete.

If Multica already has a durable job/dispatch primitive suitable for this workload, reuse it. Otherwise add the smallest deployment-owned durable dispatch mechanism rather than introducing a large workflow dependency.

## 15. Webhook ingestion

Webhook processing order:

```text
receive raw request
  -> identify provider/connection scope
  -> verify signature/secret
  -> validate provider account/team/project scope
  -> derive provider event identity
  -> persist dedupe record
  -> map event
  -> correlate operation
  -> validate allowed state transition
  -> update operation
  -> write safe evidence/audit
```

Requirements:

- invalid signatures return rejection and perform no state mutation;
- duplicate events are safe;
- out-of-order events cannot regress terminal state;
- unmatched authenticated events may be stored as redacted diagnostics but must not create arbitrary deployment operations;
- raw request bodies containing credentials must not be logged.

## 16. Reconciliation

Webhooks are the preferred event stream when available, but they are not sufficient alone.

A reconciler scans non-terminal operations and calls `GetOperation` when:

- no event was received within the expected interval;
- backend restarted during an active deployment;
- webhook delivery failed;
- a provider does not support webhooks;
- an operator explicitly requests reconciliation.

Use provider-aware exponential backoff and deadlines.

Reconciliation must stop after terminal state or a provider-specific maximum age. Timeout should produce a visible diagnostic state/error rather than silently abandoning the operation.

## 17. Evidence and logs

Provider responses must be normalized before persistence.

Safe deployment evidence may include:

- provider operation/deployment ID;
- provider project/service ID;
- source SHA/ref;
- timestamps;
- normalized status;
- provider status string;
- deployment URL;
- provider dashboard URL;
- build duration;
- safe error category/message;
- health-check result when explicitly configured.

Do not store whole provider payloads by default.

Logs should use one of these models:

1. store a safe bounded summary and provider link;
2. retrieve logs on demand and redact before returning;
3. persist only explicitly allow-listed log excerpts.

P0 should prefer safe summary + provider dashboard/log links over copying entire provider logs into Multica.

## 18. UI architecture

Deployment must be available outside the Autonomous Control Center.

### 18.1 Workspace settings

Add **Deployment Providers** to workspace settings.

Connection card should show:

- provider;
- account/team/workspace name;
- connection status;
- last validation;
- reconnect action;
- disconnect action.

Credentials are never displayed.

### 18.2 Project detail

Add a generic **Deployments** project section/tab that works for standard and autonomous projects.

It should contain:

- connected targets;
- add/attach target flow;
- current deployment status;
- deploy action;
- deployment history;
- environment badge;
- source SHA/ref;
- deployment URL;
- provider dashboard link;
- timestamps;
- available capability-driven actions.

Shared deployment views should live in the existing shared views layer where possible; web/desktop wrappers should keep routing/platform concerns outside shared feature components.

### 18.3 Confirmation UX

Do not use one generic confirmation for all mutations.

Dedicated confirmation is required for:

- provider target creation;
- production deployment;
- promotion;
- rollback;
- disconnect;
- future secret/environment changes.

## 19. Repository analysis / AI assistance

AI assistance is advisory.

For a repository and provider target, AI may produce a deployment recipe containing:

- detected framework;
- root directory;
- suggested build command;
- output directory;
- runtime suitability;
- required configuration variable names;
- preview/production recommendation;
- incompatibilities and risks.

AI must not:

- receive provider credentials;
- receive secret variable values;
- create provider resources without a backend authorization/confirmation path;
- declare production healthy based only on provider build success.

Repository detection should be implemented as recipes/rules independent of provider authentication.

`Remix`, `Next.js`, `Vite`, etc. are framework/runtime recipes, not deployment providers.

## 20. Vercel P0

Vercel is the first connector because it provides a clear project/deployment model and is the highest-priority use case for repository-backed frontend/web application deployment.

### 20.1 P0 capabilities

P0 should support:

- connect Vercel;
- validate account/team scope;
- list accessible Vercel projects;
- attach an existing Vercel project;
- create a Vercel project after dedicated confirmation;
- configure repository-backed target metadata;
- trigger preview deployment;
- trigger or promote production through an explicit production path;
- persist external deployment ID and URL;
- verify lifecycle webhooks;
- reconcile missing events;
- expose safe provider links/evidence;
- disconnect/revoke connection;
- expose rollback/promotion only if the concrete Vercel API path is proven safe and supported for the target.

### 20.2 Vercel authentication

Cloud:

- prefer OAuth/provider authorization;
- encrypt refresh/access material server-side;
- associate connection with the authenticated workspace.

Self-host:

- support OAuth where deployable/configured;
- optionally support admin-entered scoped token;
- validate token before saving active connection;
- encrypt immediately;
- never persist plaintext.

Auth method must remain a connection detail; deployment service APIs must not care whether the provider uses OAuth or a token.

### 20.3 Vercel target flow

```text
Connect Vercel
  -> select team/account
  -> discover Vercel projects
  -> choose existing OR create new
  -> map to Multica project
  -> configure repository/ref/root
  -> analyze repository
  -> save target
```

Saving a target must not automatically deploy it.

### 20.4 Vercel deployment flow

```text
Select target
  -> choose preview/production intent
  -> freeze explicit source SHA/ref
  -> show exact deployment plan
  -> request required confirmation
  -> create deployment operation
  -> trigger Vercel
  -> store external deployment ID
  -> follow webhook/reconciliation lifecycle
  -> expose result/evidence
```

A generated preview URL is not equivalent to production promotion.

### 20.5 Vercel suitability guard

Repository analysis must warn or block unsupported recommendations for workloads that are clearly unsuitable for the selected Vercel target, including cases requiring arbitrary persistent processes, unsupported persistent local storage, or architecture assumptions incompatible with the provider.

The provider connector should not silently rewrite application architecture to force compatibility.

## 21. Provider expansion strategy

The generic contract is considered proven only after at least one materially different provider can be implemented without modifying core deployment ownership.

### 21.1 Render

Render is a strong second connector for testing the abstraction because its primary remote object is a service and deployment semantics differ from Vercel.

Expected work:

- connection auth;
- service discovery;
- target mapping;
- deploy trigger;
- status/event ingestion;
- reconciliation;
- logs/provider links;
- optional deploy-hook compatibility.

If Render requires new generic core tables or provider-name conditionals, revisit the abstraction before shipping it.

### 21.2 Heroku

Heroku validates that Multica does not assume `deployment == build`.

Expected work:

- account/team connection;
- app/pipeline target mapping;
- build/release correlation;
- signed app webhooks;
- release-aware rollback;
- capability-driven UI actions.

### 21.3 Railway

Railway validates transport and lifecycle flexibility.

Expected work:

- workspace/project/service/environment discovery;
- GraphQL client;
- deployment trigger;
- restart/redeploy/cancel/rollback mappings where supported;
- logs;
- polling/reconciliation.

The core deployment service must not assume provider APIs are REST.

## 22. Autonomous Project OS integration — later, separate branch

Generic deployment work is developed and reviewed independently on `main`.

Only after the generic platform is stable should `feat/autonomous-project-os` consume it.

The bridge should look like:

```text
Autonomous deploy node
  -> deterministic policy/approval gate
  -> generic Deployment Service
  -> deployment_operation_id
  -> workflow waits on operation state
  -> terminal deployment result/evidence
```

Autonomous persistence may store orchestration correlation such as:

```text
plan_id
node_id
deployment_operation_id
policy_snapshot
approval_reference
```

It must not duplicate provider credentials, external lifecycle ownership, webhook state, provider operation state, or provider event history.

If an existing `autonomous_project_deployment` table remains, it should become an orchestration correlation/audit record pointing to generic `deployment_operation`, not the authoritative deployment subsystem.

## 23. Branch and contributor PR strategy

The repository uses `main`; there is no `master` branch in the fork.

The implementation flow should be:

```text
multica-ai/multica:main
        |
        v
sync caglar09/multica:main
        |
        v
caglar09/multica:feat/deployment-providers-core
        |
        +--> contributor PR -> multica-ai/multica:main
        |
        v
upstream main after merge
        |
        v
feat/autonomous-project-os
        |
        +--> autonomous deployment bridge
```

Do not create the generic deployment implementation branch from `feat/autonomous-project-os`.

Before implementation:

```bash
git fetch upstream
git switch main
git merge --ff-only upstream/main
git push origin main
git switch -c feat/deployment-providers-core
```

If the fork `main` cannot fast-forward because it contains local commits, reconcile that explicitly before starting provider code. Do not hide an unrelated merge inside the deployment PR.

The upstream contributor PR should contain no Autonomous Project OS dependency.

## 24. PR decomposition

Prefer reviewable PRs over one giant provider PR.

### PR 1 — deployment core

Includes:

- deployment ADR/spec;
- generic domain model;
- migrations;
- repositories;
- state machine;
- provider registry;
- provider contract;
- fake provider;
- async operation engine;
- webhook/event infrastructure;
- reconciliation;
- generic API;
- generic project/workspace UI skeleton;
- core tests.

Must not include Vercel-specific behavior beyond test fixtures if avoidable.

### PR 2 — Vercel connector

Includes:

- Vercel auth/validation;
- team/project discovery;
- attach existing project;
- create project flow;
- Vercel deployment trigger;
- lifecycle mapper;
- webhook verification;
- reconciliation;
- provider links/log summaries;
- Vercel target UI;
- sandbox/integration tests.

### PR 3 — autonomous bridge

This is not part of the upstream generic deployment implementation unless upstream accepts Autonomous Project OS itself.

It belongs on `feat/autonomous-project-os` and consumes the merged generic deployment API.

## 25. Migration strategy

All generic deployment migrations must use the next migration sequence from current upstream `main` at implementation time.

Do not reserve or copy migration numbers from `feat/autonomous-project-os`.

When upstream generic deployment changes are later merged/rebased into `feat/autonomous-project-os`, any private branch migration-number collisions must be resolved there by renumbering the branch-only migrations before release.

Generic migration rules:

- additive first;
- avoid destructive rewrite in the initial release;
- indexes created according to existing Multica migration conventions;
- no cascade that could erase deployment audit history unexpectedly;
- workspace/project ownership constraints validated in service code and database where consistent with repository conventions;
- connection deletion should normally be logical/revocation, not historical evidence deletion.

## 26. Implementation phases

### Phase 0 — upstream/main preparation and ADR

1. Ensure the fork `main` is based on current `multica-ai/multica:main` before code work begins.
2. Create `feat/deployment-providers-core` from the synchronized `main`.
3. Add an ADR covering:
   - deployment platform ownership;
   - provider connection vs target vs operation vs event;
   - async lifecycle;
   - secret custody;
   - confirmation boundaries;
   - provider capability model;
   - autonomous integration boundary.
4. Confirm current migration sequence and router/service conventions.
5. Identify the existing encryption primitive to reuse/extract.
6. Identify the existing durable dispatch/job primitive to reuse, if suitable.

**Exit gate:** architecture is documented and no generic deployment package depends on autonomous code.

### Phase 1 — domain, persistence, state machine

1. Add generic domain types and normalized enums.
2. Add connection persistence.
3. Add target persistence.
4. Add operation persistence.
5. Add provider-event persistence and dedupe constraint.
6. Add indexes for workspace/project/target/status/provider-operation lookups.
7. Implement operation transition validator.
8. Add idempotency enforcement.
9. Add repository tests for tenant/workspace isolation.

**Exit gate:** migration tests, repository tests, state-transition tests, idempotency tests, and restart persistence tests pass.

### Phase 2 — provider contract and fake provider

1. Add provider registry.
2. Add typed provider interface.
3. Add capability interfaces/set.
4. Add fake provider supporting controllable queued/building/success/failure transitions.
5. Add provider error classification.
6. Prove a provider can be registered without changing deployment core switch statements.

**Exit gate:** core service tests run entirely against the fake provider.

### Phase 3 — async engine, webhook gateway, reconciler

1. Create operation before provider mutation.
2. Dispatch provider trigger durably.
3. Persist external operation identity immediately.
4. Add webhook verification/correlation pipeline.
5. Add event dedupe.
6. Add out-of-order transition protection.
7. Add reconciliation scheduler/worker.
8. Add retry/backoff and terminal deadlines.
9. Add safe evidence/redaction layer.
10. Add audit events.

**Exit gate:** fake provider tests cover duplicate webhook, delayed webhook, missing webhook, backend restart, transient trigger error, retry, out-of-order events, cancellation, and terminal-state protection.

### Phase 4 — generic API and UI

1. Add workspace deployment-connections APIs.
2. Add provider discovery APIs.
3. Add project deployment-target APIs.
4. Add deployment-operation APIs.
5. Add authorization checks.
6. Add capability serialization.
7. Add workspace Deployment Providers settings UI.
8. Add project Deployments UI.
9. Add deployment history/detail/status timeline.
10. Add confirmation components for high-impact actions.

**Exit gate:** a standard non-autonomous Multica project can use the fake provider end-to-end through UI/API.

### Phase 5 — Vercel connection and target management

1. Implement Vercel API client behind provider interface.
2. Implement connection validation.
3. Implement account/team scope discovery.
4. Implement project discovery.
5. Implement existing-project attach.
6. Implement separately confirmed project creation.
7. Add typed Vercel target configuration validation.
8. Add connection revoke/disconnect handling.
9. Add provider-specific error mapping/redaction.

**Exit gate:** real Vercel account can connect, list projects, attach an existing project, and create a test project only after confirmation.

### Phase 6 — Vercel deployment lifecycle

1. Trigger repository-backed preview deployment for explicit revision/ref.
2. Persist external deployment ID and URL.
3. Implement signed Vercel webhook verification.
4. Map Vercel lifecycle events to normalized operation states.
5. Implement `GetOperation` reconciliation.
6. Add production deployment/promotion path with separate approval intent.
7. Add provider dashboard/log links.
8. Add rollback/promotion/cancel only after exact API semantics are verified and capability-gated.
9. Add safe failure diagnostics.

**Exit gate:** Vercel sandbox acceptance matrix passes.

### Phase 7 — repository analysis and deployment recipes

1. Add framework/repository detector independent of provider credentials.
2. Add Vercel suitability recipe.
3. Recommend root directory/build/output settings.
4. Detect required environment variable names without reading secret values.
5. Surface incompatibility/risk warnings.
6. Require human save/approval for target/deployment mutations.

**Exit gate:** recipe generation cannot mutate provider resources and does not expose secrets to model context.

### Phase 8 — hardening and upstream PR

1. Run full backend/frontend test suites required by repository conventions.
2. Run migration upgrade from representative existing DB.
3. Test credential redaction in logs/API/errors.
4. Test workspace isolation.
5. Test provider rate-limit/transient failures.
6. Test reconnect/revocation behavior.
7. Test backend restart during active deployment.
8. Add docs for cloud and self-host provider configuration.
9. Open contributor PR from fork feature branch to `multica-ai/multica:main`.

**Exit gate:** generic deployment platform + Vercel are reviewable without any Autonomous Project OS dependency.

### Phase 9 — merge into Autonomous Project OS

After upstream/main contains the generic deployment subsystem:

1. update `feat/autonomous-project-os` from upstream `main`;
2. resolve migration-number collisions on the autonomous branch;
3. replace/refactor autonomous deployment adapter to call generic Deployment Service;
4. correlate workflow node to `deployment_operation_id`;
5. keep deterministic autonomous approval/policy enforcement before mutation;
6. wait on generic operation state instead of owning provider polling/webhooks;
7. expose generic evidence in Autonomous Control Center;
8. keep health/observation logic separate from provider build success.

**Exit gate:** manual standard-project deployments and autonomous deployments use the same authoritative deployment subsystem.

### Phase 10 — validate extensibility with second provider

Implement Render next to test the abstraction against a service-oriented provider.

The second provider must not require:

- new provider-name branches in generic UI/service code;
- replacement of connection/target/operation tables;
- a second webhook engine;
- a second reconciliation engine;
- a new deployment history subsystem.

If it does, refactor the generic contract before adding more providers.

Heroku and Railway follow after the contract survives the Render implementation.

## 27. Vercel acceptance matrix

At minimum test:

### Connection

- valid connection;
- invalid token/authorization;
- revoked credentials;
- team/account mismatch;
- reconnect;
- disconnect;
- no plaintext credential exposure.

### Target

- discover existing project;
- attach existing project;
- reject project outside authenticated scope;
- create project only after confirmation;
- root-directory validation;
- repository/ref mismatch;
- duplicate target handling.

### Deployment

- preview success;
- preview build failure;
- explicit source SHA recorded;
- production request rejected without production confirmation;
- approved production succeeds;
- duplicate request idempotency;
- provider trigger transient failure and retry;
- provider hard failure;
- cancelled deployment;
- unsupported action hidden and server-rejected.

### Webhooks

- valid signature;
- invalid signature;
- duplicate event;
- delayed event;
- out-of-order event;
- event from wrong team/project;
- unknown operation ID;
- terminal state cannot regress.

### Reconciliation

- missing webhook recovered by polling;
- backend restart during build;
- provider operation disappears/not found;
- rate limit/backoff;
- reconciliation deadline exceeded produces visible diagnostic.

### Security

- provider credential absent from API response;
- credential absent from logs;
- credential absent from AI prompt/context;
- webhook secret absent from evidence;
- provider error payload redacted;
- cross-workspace IDs cannot access a connection/target/operation.

## 28. Observability

Deployment subsystem should emit structured operational telemetry for:

- connection validation failures;
- provider API latency/errors;
- deployment trigger success/failure;
- active operation counts by normalized state;
- webhook verification failure;
- duplicate webhook count;
- unmatched webhook count;
- reconciliation attempts/results;
- operations exceeding expected duration;
- provider rate-limit responses.

Telemetry must use internal IDs and safe provider identifiers only. Do not log credentials or raw unredacted provider bodies.

## 29. Failure semantics visible to users

Users should be able to distinguish at least:

- Multica authorization failure;
- provider connection invalid/revoked;
- provider API unavailable/rate limited;
- provider target misconfiguration;
- provider build failure;
- deployment runtime failure when provider exposes it;
- webhook delivery/reconciliation delay;
- application health-check failure;
- rollback/promotion unsupported;
- deployment state unknown after reconciliation deadline.

Do not collapse all failures into `deployment failed`.

## 30. Health and observation boundary

Provider success means only that the provider considers the deployment successful/ready according to its lifecycle.

Application health is separate evidence.

A target may optionally define a health URL/check policy. Health checking should produce its own result and must not rewrite provider history.

Initial release must not autonomously roll back because a health check fails. It surfaces the incident/evidence and requires the appropriate confirmation/policy path.

## 31. Rollout strategy

Recommended rollout:

1. core schema/service hidden behind feature flag;
2. fake-provider internal testing;
3. Vercel sandbox/internal account testing;
4. self-host token-based Vercel validation;
5. cloud OAuth validation where configured;
6. limited beta;
7. generic provider UI enabled;
8. production Vercel release;
9. Render connector begins only after Vercel lifecycle is stable.

Feature flags should disable user-visible capability without invalidating persisted operation history.

## 32. Definition of done for generic deployment platform

The deployment platform is considered complete enough for the first provider when:

- standard projects can use it without Autonomous Project OS;
- provider credentials are encrypted and never returned;
- connection, target, operation, and event ownership are separate;
- deployments survive backend restart;
- webhook replay/out-of-order delivery is safe;
- missing webhooks are reconciled;
- source revision is explicit and immutable per operation;
- preview and production are distinct user intents;
- capability-driven actions work without provider-name branching;
- Vercel sandbox passes the acceptance matrix;
- deployment history/evidence is visible in Multica;
- the upstream contributor PR has no autonomous dependency.

## 33. Definition of done for Autonomous Project OS integration

Autonomous integration is complete when:

- Project OS invokes the generic Deployment Service rather than provider code;
- autonomous deploy nodes correlate to generic `deployment_operation` IDs;
- policy/approval remains deterministic and server-enforced;
- workflow execution can wait/recover across restart while the external deployment is non-terminal;
- provider webhook/reconciliation logic remains entirely in the generic subsystem;
- the Autonomous Control Center can display generic deployment state/evidence;
- manual and autonomous deployment histories point to the same authoritative operations.

## 34. Open implementation decisions to resolve before coding each relevant phase

1. Which current Multica encryption primitive should become the reusable deployment secret abstraction?
2. Which current durable dispatch/job mechanism is appropriate for trigger/reconciliation work?
3. Exact RBAC role for preview deploys beyond workspace admin, if any.
4. Exact Vercel OAuth setup for Multica Cloud and whether self-host OAuth callback configuration ships in P0.
5. Whether production is represented as a deployment intent, promotion intent, or both for each Vercel target mode; this must follow verified provider semantics rather than a generic assumption.
6. Whether P0 stores only provider log links/summaries or supports redacted on-demand log retrieval.
7. Exact provider webhook secret ownership and rotation UX.
8. Whether archived targets remain deployable (recommended: no) while retaining history.
9. Whether connection revocation immediately marks active operations `unknown/failed` or lets reconciliation classify them (recommended: preserve state and surface connection-revoked diagnostic).
10. Exact feature flag and entitlement behavior for Cloud vs self-host.

None of these decisions should change the core ownership model described in this document.
