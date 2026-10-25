# Deployment Providers Platform — Release Manifest

**Release Version:** 1.0.0  
**Target Milestone:** P0 Reference Implementation (Vercel Connector & Generic Engine)  
**Target Upstream PR:** `multica-ai/multica:main` from `caglar09/multica:feat/deployment-providers-core`  
**Status:** Ready for Upstream Submission  

---

## 1. Subsystem Overview

The **Deployment Providers Platform** introduces a first-class, provider-neutral deployment control plane into Multica Core. Engineering teams, automated pipelines, and autonomous agents can configure third-party hosting platforms, associate remote provider projects with Multica projects as deployment targets, execute asynchronous source revision builds, track external lifecycles through cryptographically signed webhooks and polling reconciliation, and capture auditable evidence and build logs.

The platform is designed with strict architectural purity:
1. **Generic Core Platform Capability:** Completely decoupled from autonomous workflows, schedulers, and prompt systems.
2. **Capability-Driven Provider Contracts:** Extensible abstraction for future providers (Render, Railway, Heroku, Cloudflare).
3. **Secret Isolation & Key Versioning:** Write-only credential boundaries, SecretBox AES-256-GCM encryption at rest, and zero credential leakage in logs, API responses, or AI contexts.
4. **Resilient Asynchronous Engine:** Lease-based dispatching, bounded exponential reconciliation, idempotent execution, and terminal state immutability.
5. **Database Standards Compliance:** Application-level referential integrity without database foreign keys, concurrent index builds (`CREATE INDEX CONCURRENTLY`), and ordered SQL migrations.

---

## 2. Core Capabilities Matrix

| Capability | Scope | Description |
|---|---|---|
| **Provider Connection Management** | Workspace | Connect and authenticate provider accounts using write-only OAuth tokens or Personal Access Tokens (PAT). Pre-flight credentials validation prevents inactive or broken connections. |
| **Target Mapping & Discovery** | Project | Discover external provider projects and bind them to Multica projects with target environment definitions (`preview`, `production`, `staging`), branch mappings, and custom build arguments. |
| **Capability Negotiation** | Provider-Neutral | Dynamically queries provider capabilities (`trigger`, `status`, `cancel`, `logs`, `evidence`, `promote`, `webhook`). Unsupported actions are blocked at the service boundary. |
| **Asynchronous Lease Dispatcher** | Core Engine | Workers claim durable operations using 60-second lease tokens (`lease_token`, `lease_expires_at`). Recovers safely across server restarts without duplicated external dispatches. |
| **Normalized State Machine** | Core Engine | Deterministic state transitions (`queued -> dispatching -> running -> succeeded / failed / canceled`). Strict **terminal state immutability** ensures delayed or out-of-order webhooks cannot regress completed operations. |
| **Tuple-Based Idempotency** | Core Engine | Compound idempotency keys (`workspace_id + target_id + source_ref + environment + op_kind + approval_ref`) ensure duplicated requests return existing in-flight or completed operations. |
| **Cryptographic Webhook Ingestion** | Inbound Gateway | Constant-time HMAC-SHA256 signature verification (`hmac.Equal`) preventing timing attacks. Database-level deduplication on `(provider, provider_event_id)` drops redundant edge events. |
| **Bounded Polling Reconciliation** | Core Engine | Exponential backoff polling ($10\text{s}, 30\text{s}, 60\text{s}, 120\text{s}$) recovers missing webhooks. Enforces a 15-minute hard deadline, safely marking dead operations `failed` with `TIMEOUT_EXCEEDED`. |
| **SecretBox KeyRing Isolation** | Security | AES-256-GCM encryption at rest with versioned wire format (`0x56` magic byte + Key ID byte). Decrypts legacy unversioned payloads transparently and supports zero-downtime key rotation. |
| **Production Confirmation Gate** | Security & RBAC | Production deployment and promotion actions require explicit user confirmation flags or approval references, enforced by workspace `owner`/`admin` role guards. |
| **Sanitized Error & Log Masking** | Security | Automated regex-based scrubbing of tokens, bearer auth, and private keys from build logs, error payloads, and provider diagnostics. |

---

## 3. Vercel P0 Connector Specifications

The Vercel connector (`server/internal/integrations/deployment/vercel.go`) serves as the reference production adapter:

- **Authentication Modes:**
  - Cloud: OAuth 2.0 Integration flow with client credentials and refresh token rotation.
  - Self-Hosted: Personal Access Token (PAT) with optional team scope (`teamId` / `slug`).
- **Supported Endpoints:**
  - Pre-flight Validation: `GET /v2/user` (user auth) & `GET /v2/teams` (team verification).
  - Project Discovery: `GET /v9/projects` (lists available Vercel projects).
  - Deployment Trigger: `POST /v13/deployments` (supplies `gitSource`, `project`, `target` environment, and explicit commit SHA).
  - Status Polling: `GET /v13/deployments/{id}` (retrieves build status, readyState, and deployment URL).
  - Cancellation: `PATCH /v12/deployments/{id}/cancel` (terminates active builds).
  - Webhook Delivery: Ingests `deployment.created`, `deployment.building`, `deployment.ready`, `deployment.error`, and `deployment.canceled` events.
- **Webhook Signature Verification:**
  - Verifies `x-vercel-signature` using HMAC-SHA1/SHA256 hex digests with constant-time equality checks.
- **Payload Sanitization:**
  - Error responses from Vercel API are decoded and scrubbed using `RedactEvidenceText` before persistence or propagation to client responses.

---

## 4. Database Schema & Migration Changelog

The subsystem adds four core domain tables and six performance/idempotency indexes following Multica's zero foreign key policy:

### 4.1 Schema Tables

1. **`deployment_provider_connection`**:
   - `id` (`uuid`, PK)
   - `workspace_id` (`uuid`, Indexed)
   - `provider` (`text`, e.g. `'vercel'`)
   - `name` (`text`)
   - `auth_config` (`text`, SecretBox AES-256-GCM encrypted credentials envelope)
   - `status` (`text`, `'active'`, `'invalid'`, `'revoked'`)
   - `account_id`, `account_name`, `team_id`, `team_slug` (`text`, provider identity metadata)
   - `metadata` (`jsonb`)
   - `created_at`, `updated_at` (`timestamptz`)

2. **`deployment_target`**:
   - `id` (`uuid`, PK)
   - `workspace_id` (`uuid`, Indexed)
   - `project_id` (`uuid`, Indexed)
   - `connection_id` (`uuid`, Indexed)
   - `name` (`text`)
   - `environment` (`text`, `'preview'`, `'production'`, `'staging'`)
   - `provider_project_id` (`text`)
   - `provider_project_name` (`text`)
   - `configuration` (`jsonb`, root directory, build/install commands, output directory)
   - `status` (`text`, `'active'`, `'archived'`)
   - `created_at`, `updated_at` (`timestamptz`)

3. **`deployment_operation`**:
   - `id` (`uuid`, PK)
   - `workspace_id` (`uuid`, Indexed)
   - `target_id` (`uuid`, Indexed)
   - `status` (`text`, normalized: `'queued'`, `'dispatching'`, `'running'`, `'succeeded'`, `'failed'`, `'canceled'`)
   - `source_ref` (`text`, git commit SHA / branch)
   - `environment` (`text`)
   - `idempotency_key` (`text`, Indexed)
   - `provider_deployment_id` (`text`)
   - `url` (`text`, live deployment URL)
   - `inspect_url` (`text`, provider dashboard link)
   - `lease_token` (`uuid`)
   - `lease_expires_at` (`timestamptz`)
   - `retry_count` (`int`)
   - `next_reconcile_at` (`timestamptz`)
   - `evidence` (`jsonb`, sanitized build output and diagnostic logs)
   - `created_at`, `updated_at` (`timestamptz`)

4. **`deployment_event`**:
   - `id` (`uuid`, PK)
   - `workspace_id` (`uuid`, Indexed)
   - `deployment_operation_id` (`uuid`, Indexed)
   - `provider` (`text`)
   - `provider_event_id` (`text`)
   - `event_type` (`text`)
   - `status` (`text`)
   - `payload` (`jsonb`, raw sanitized provider webhook data)
   - `created_at` (`timestamptz`)

### 4.2 Migration Sequence & Renumbering Strategy

The deployment schema migrations are partitioned into single-statement, rollback-safe files:

| Step | Migration File Stem | Purpose | Type |
|---|---|---|---|
| 01 | `469_deployment_provider_connection` | Creates `deployment_provider_connection` table | DDL |
| 02 | `470_deployment_target` | Creates `deployment_target` table | DDL |
| 03 | `471_deployment_operation` | Creates `deployment_operation` table | DDL |
| 04 | `472_deployment_event` | Creates `deployment_event` table | DDL |
| 05 | `473_deployment_operation_idempotency_idx` | `CREATE UNIQUE INDEX CONCURRENTLY idx_deployment_operation_idempotency` | Index |
| 06 | `474_deployment_event_dedupe_idx` | `CREATE UNIQUE INDEX CONCURRENTLY idx_deployment_event_dedupe` | Index |
| 07 | `475_deployment_operation_status` | `CREATE INDEX CONCURRENTLY idx_deployment_operation_workspace_status` | Index |
| 08 | `476_deployment_operation_idempotency_fields` | Adds compound target/env idempotency columns | DDL |
| 09 | `478_deployment_operation_idempotency_tuple` | `CREATE UNIQUE INDEX CONCURRENTLY idx_deployment_operation_tuple` | Index |
| 10 | `479_deployment_event_status` | `CREATE INDEX CONCURRENTLY idx_deployment_event_operation_created` | Index |

> **Upstream Rebase Renumbering Note:**  
> In `multica-ai/multica:main`, migrations `469` (`issue_status_lifecycle_categories`) and `470` (`issue_status_icon`) have been merged. When merging or rebasing this branch into upstream `main`, the deployment migration files increment by an offset of +2 (from `469-479` to `471-480`), satisfying `TestMigrationNumericPrefixesAreUnique`.

---

## 5. API Routes & User Interface Endpoints

### 5.1 REST Handlers (`server/internal/handler/deployment.go`)

- `POST /api/workspaces/{workspace_id}/deployment-connections` — Register provider connection (pre-flight verified).
- `GET /api/workspaces/{workspace_id}/deployment-connections` — List connections (masked secrets).
- `DELETE /api/workspaces/{workspace_id}/deployment-connections/{connection_id}` — Disconnect provider.
- `GET /api/workspaces/{workspace_id}/deployment-connections/{connection_id}/projects` — Remote project discovery.
- `POST /api/workspaces/{workspace_id}/deployment-targets` — Create project deployment target.
- `GET /api/workspaces/{workspace_id}/projects/{project_id}/deployment-targets` — List targets for project.
- `DELETE /api/workspaces/{workspace_id}/deployment-targets/{target_id}` — Remove target.
- `POST /api/workspaces/{workspace_id}/deployments/trigger` — Trigger deployment (requires admin confirmation for production).
- `POST /api/workspaces/{workspace_id}/deployments/{operation_id}/cancel` — Abort in-flight build.
- `POST /api/workspaces/{workspace_id}/deployments/{operation_id}/promote` — Promote preview build to production.
- `GET /api/workspaces/{workspace_id}/deployments/{operation_id}` — Read live status, URLs, and evidence.
- `GET /api/workspaces/{workspace_id}/projects/{project_id}/deployments` — History list with pagination.
- `POST /api/webhooks/deployments/{workspaceId}/{connectionId}` — Public unauthenticated, per-connection webhook ingestion endpoint.

### 5.2 UI Components & Views (`packages/views/deployments`)

- **Settings Integration Tab:** `DeploymentProvidersTab` with Vercel connection cards, PAT/OAuth modal triggers, and disconnect confirmations.
- **Project Deployments Tab:** `ProjectDeploymentsSection` displaying targets, latest build statuses, deployment URLs, inspect links, and build duration.
- **Target Setup & Config Dialog:** `VercelTargetAttachDialog` and `TargetEnvironmentConfig` supporting root directory and branch overrides.
- **Safe Action Dialogs:** `ProductionDeployConfirmDialog`, `PromoteDeploymentConfirmDialog`, and `RollbackDeploymentConfirmDialog` with explicit approval confirmations.

---

## 6. Verification and Security Audit Summary

1. **Boundary Test (`server/internal/deployment/boundary_test.go`):**  
   Asserts zero imports of autonomous runtime, workflow engine, or scheduler packages.
2. **Acceptance Matrix (`server/internal/integrations/deployment/vercel_acceptance_matrix_test.go`):**  
   Validates 100% of Vercel connection, target, deployment, webhook, reconciliation, and security matrix scenarios.
3. **Security Audit Suite (`server/internal/deployment/security_audit_test.go` & `server/internal/handler/deployment_security_test.go`):**  
   Comprehensive test suite confirming SecretBox key versioning, write-only API boundaries, HMAC timing safety, and evidence redaction.
4. **Cross-Tenant Isolation (`server/internal/deployment/tenant_isolation_test.go`):**  
   Confirms workspace boundaries cannot be crossed by forged IDs.
