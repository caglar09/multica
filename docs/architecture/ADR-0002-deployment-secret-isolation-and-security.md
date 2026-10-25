# ADR-0002: Secret Isolation and Credential Security Specification

- **Status:** Accepted
- **Role:** Security Engineer (`security_engineer`)
- **Stage:** `security` (`security_secret_isolation_design`)
- **Scope Identifier:** `[autonomous-project-node:security_secret_isolation_design]`
- **Target Branch:** `caglar09/multica:feat/deployment-providers-core`
- **Target Upstream PR:** `multica-ai/multica:main`

---

## 1. Context and Problem Statement

The **Deployment Providers Platform** integrates external deployment infrastructure (Vercel, Render, Railway, Cloudflare, AWS) with Multica workspaces and projects. By its nature, the deployment control plane handles highly privileged authentication assets, including:
1. Provider Personal Access Tokens (PATs) and OAuth 2.0 refresh/access tokens.
2. Inbound webhook verification HMAC secrets.
3. Target environment configuration and deployment authorization secrets.

If these secrets are leaked, unauthorized parties could deploy malicious code to production, compromise customer environments, intercept webhook traffic, or hijack cloud resources. Furthermore, Multica is deployed in both **Cloud (multi-tenant SaaS)** and **Self-Hosted (single-tenant / enterprise on-premise)** topologies, and interacts with Autonomous Project OS and LLM workflows.

This specification establishes the authoritative cryptographic interfaces, key versioning and rotation strategies, write-only API contracts, zero-exposure boundaries across logs/prompts, and constant-time HMAC webhook verification standards.

---

## 2. Cryptographic Foundation: SecretBox Architecture & Key Versioning

### 2.1 Cryptographic Primitive: AES-256-GCM

All deployment secrets stored at rest must be encrypted using the established Multica `secretbox` symmetric encryption primitive (`server/internal/util/secretbox`).

- **Algorithm:** AES-256-GCM (Galois/Counter Mode).
- **Key Length:** 32 bytes (256 bits).
- **Nonce:** 12 bytes (96 bits) cryptographically random (`crypto/rand`), generated per encryption operation.
- **Authentication Tag:** 16 bytes (128 bits) computed over the ciphertext to guarantee confidentiality and integrity.
- **Non-Determinism:** Encryption is strictly non-deterministic (`Seal` generates a fresh random nonce per call). Identical plaintexts produce distinct ciphertexts, preventing ciphertext equivalence analysis across workspaces.

### 2.2 Wire Format & Key Versioning Strategy

To support zero-downtime key rotation and key migration without breaking existing stored secrets, the platform defines a backward-compatible wire format:

#### 2.2.1 Format Specification

```text
1. Legacy Unversioned Format (Version 0):
   +-----------------------+---------------------------------------+
   | Nonce (12 bytes)      | Ciphertext + GCM Tag (variable len)   |
   +-----------------------+---------------------------------------+

2. Versioned Wire Format (Version 1+):
   +-----------------------+-------------------+-------------------+---------------------------------------+
   | Magic Byte: 0x56 ('V')| Key ID (1 byte)   | Nonce (12 bytes)  | Ciphertext + GCM Tag (variable len)   |
   +-----------------------+-------------------+-------------------+---------------------------------------+
```

- **Magic Prefix:** `0x56` (ASCII `'V'`) distinguishes versioned envelopes from legacy ciphertexts.
- **Key ID:** `uint8` (0–255), identifying the specific key in the active KeyRing used to seal the payload.
- **Backward Compatibility:** When `Open()` encounters a payload without the magic prefix `0x56`, it transparently falls back to the default or legacy master key. Existing Lark, Slack, VCS, and plugin secrets remain 100% decryptable without requiring bulk database rewrites.
- **Forward Secrecy & Rotation:** When saving or rotating secrets, the platform always seals using the active primary Key ID (`KeyRing.Primary()`).

### 2.3 Cloud and Self-Hosted Deployment Key Management

The SecretBox architecture abstracts key resolution to support both self-hosted and cloud infrastructure:

```text
                             +-------------------------------+
                             |    KeyRing / KeyProvider      |
                             +---------------+---------------+
                                             |
                     +-----------------------+-----------------------+
                     |                                               |
                     v                                               v
     +-------------------------------+               +-------------------------------+
     |     Self-Hosted Strategy      |               |        Cloud Strategy         |
     +-------------------------------+               +-------------------------------+
     | - MULTICA_SECRETBOX_KEY       |               | - AWS KMS / GCP Cloud KMS     |
     | - MULTICA_SECRETBOX_KEYRING   |               | - Envelope Encryption (DEK)   |
     | - Multi-key env format:       |               | - Auto-rotated master key     |
     |   "1:<b64_key>,2:<b64_key>"   |               | - Ephemeral process memory    |
     | - File-based key ring store   |               | - HashiCorp Vault adapter     |
     +-------------------------------+               +-------------------------------+
```

#### 2.3.1 Self-Hosted Deployments
- Single Key: Configured via environment variable `MULTICA_DEPLOYMENT_SECRET_KEY` or `MULTICA_SECRETBOX_KEY` (base64-encoded 32-byte string).
- Multi-Key Ring: Configured via `MULTICA_SECRETBOX_KEYRING="1:<b64_key1>,2:<b64_key2>"` with `MULTICA_SECRETBOX_PRIMARY_KEY="2"`.
- Operator-Initiated Rotation: Operators add the new key with an incremented ID, set it as primary, restart the server, and run `multica admin secrets re-encrypt` to lazily or eagerly re-seal historical rows.

#### 2.3.2 Cloud / Managed SaaS Deployments
- Envelope Encryption: The master encryption key resides in a Hardware Security Module (AWS KMS, Google Cloud KMS, or Azure Key Vault).
- Data Encryption Keys (DEKs): For high-throughput operations, ephemeral 32-byte DEKs are generated via KMS (`kms:GenerateDataKey`), cached in volatile memory with short TTL (e.g., 1 hour), and used with SecretBox.
- Automatic Rotation: KMS key rotation occurs transparently without updating application code or restarting instances.

---

## 3. Credential Write-Only Policy

Under no circumstance shall plaintext secrets be retrieved by clients once submitted.

### 3.1 Write-Only Enforcement Rules

1. **Input Accepted on Mutation:**
   - Plaintext credentials (e.g., Vercel Personal Access Tokens, OAuth tokens, webhook signing secrets) are accepted only in `POST` (create) or `PUT/PATCH` (rotate/update) request bodies.
2. **Never Echoed on Read:**
   - `GET /api/workspaces/:wsId/deployment-providers` and `GET /api/workspaces/:wsId/deployment-providers/:id` **NEVER** return `token`, `secret`, `signing_secret`, `access_token`, or `refresh_token`.
3. **Safe Metadata Masking:**
   - Read responses replace raw secrets with non-sensitive audit metadata:
     ```json
     {
       "id": "019fd560-1111-7000-8000-000000000001",
       "workspace_id": "d4620f6f-db05-4475-811c-9568a3d54e63",
       "provider": "vercel",
       "name": "Production Vercel",
       "enabled": true,
       "has_credentials": true,
       "credential_hint": "vercel_tok_...8f2a",
       "credential_status": "valid",
       "last_validated_at": "2026-09-13T16:00:00Z",
       "created_at": "2026-09-13T12:00:00Z",
       "updated_at": "2026-09-13T16:00:00Z"
     }
     ```
4. **Token Masking Algorithm:**
   - For tokens longer than 12 characters: `token[:min(10, len-4)] + "..." + token[len-4:]`.
   - For shorter tokens: `"***" + token[len-3:]`.
   - The masked string allows operators to cross-reference which account or key was configured in the provider console without exposing enough entropy to enable offline brute-force.
5. **Dedicated Credential Endpoints:**
   - Credential updates reside on dedicated endpoints (e.g., `PUT /api/workspaces/:wsId/deployment-providers/:id/credentials`) rather than shared configuration updates. This ensures credential mutation payloads do not intermingle with routine metadata updates (name, enabled status) in generic audit logs.

---

## 4. Zero Exposure Across Logs, Telemetry, and Autonomous LLM Prompts

### 4.1 Log and Telemetry Sanitization
- **HTTP Middleware Redaction:** All request/response logging middleware strictly sanitizes HTTP headers:
  - `Authorization: Bearer <redacted>`
  - `x-vercel-signature: [REDACTED]`
  - `X-Hub-Signature-256: [REDACTED]`
- **Body Redaction:** Request payloads matching fields `token`, `secret`, `signing_secret`, `credentials`, `password`, or `api_key` are replaced with `"[REDACTED]"` prior to logging to `stdout`, file, or external tracing (OpenTelemetry/Sentry).
- **Error Messages:** System errors returned to callers or written to error logs must never incorporate the raw token in format strings (e.g., `fmt.Errorf("failed to authenticate with token %s: %w", token, err)` is strictly prohibited; must be `fmt.Errorf("failed to authenticate connection %s: %w", connectionID, err)`).

### 4.2 Autonomous Project OS & AI Boundary Isolation
- **No Credentials in Prompts:** Autonomous agents (Mika, subagents, schedulers) operate solely with opaque resource identifiers (`connection_id`, `target_id`, `operation_id`).
- **Context Package Stripping:** When task context packages or handoff envelopes are constructed, the deployment subsystem filters all credential metadata. AI agents cannot inspect or be prompted with provider tokens.
- **Application Layer Dispatch:** Triggers initiated by autonomous workflows submit `target_id` and `commit_sha`. The backend deployment engine resolves credentials from SecretBox inside the Go service runtime immediately prior to executing outbound HTTPS calls.

### 4.3 In-Memory Hygiene
- Plaintext byte buffers used to decrypt tokens or compute HMAC signatures must be explicitly zeroed after use:
  ```go
  defer func() {
      for i := range plaintext {
          plaintext[i] = 0
      }
  }()
  ```

---

## 5. Webhook HMAC Signature Verification Standards

Webhook ingestion endpoints represent untrusted public network entry points. They must be protected against tampering, spoofing, replay attacks, and timing side channels.

### 5.1 Constant-Time Verification Requirement
- All webhook signature verifications **MUST** use `crypto/hmac` and `crypto/subtle.ConstantTimeCompare` (or `hmac.Equal`).
- Plain string comparison (`sig == expectedSig` or `bytes.Equal`) is strictly prohibited to prevent partial-prefix timing attacks.

### 5.2 Provider Verification Specifications

#### 5.2.1 Vercel Webhook Signature Scheme
- **Header:** `x-vercel-signature`
- **Algorithm:** HMAC-SHA1
- **Payload:** Raw request body bytes.
- **Key:** Webhook secret configured in Vercel project integration settings.
- **Verification Rule:**
  ```go
  mac := hmac.New(sha1.New, []byte(secret))
  mac.Write(rawBody)
  expectedSig := hex.EncodeToString(mac.Sum(nil))
  isValid := hmac.Equal([]byte(headerSignature), []byte(expectedSig))
  ```

#### 5.2.2 GitHub / Multica Generic Webhook Signature Scheme
- **Header:** `X-Hub-Signature-256`
- **Prefix:** `sha256=`
- **Algorithm:** HMAC-SHA256
- **Payload:** Raw request body bytes.
- **Verification Rule:**
  ```go
  mac := hmac.New(sha256.New, []byte(secret))
  mac.Write(rawBody)
  want := mac.Sum(nil)
  got, err := hex.DecodeString(strings.TrimPrefix(headerSignature, "sha256="))
  isValid := (err == nil) && hmac.Equal(want, got)
  ```

### 5.3 Replay Protection and Timestamp Verification
- Where providers attach a timestamp header (e.g., `X-Signature-Timestamp` or payload `createdAt`), the engine verifies:
  $$|T_{\text{current}} - T_{\text{header}}| \le 300\text{ seconds (5 minutes)}$$
- Deliveries outside the 5-minute tolerance window are rejected with `HTTP 400 Bad Request` or `HTTP 401 Unauthorized` to prevent replay attacks.
- Even in the absence of a timestamp header, duplicate deliveries are defeated at the storage layer via unique index on `deployment_event(provider, provider_event_id)`.

### 5.4 Raw Request Body Integrity
- Handlers must read the raw body bytes via `io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookPayloadSize))` before JSON unmarshaling.
- Verifying the signature against re-serialized JSON is forbidden, as serializer whitespace or key ordering discrepancies invalidate cryptographic signatures.

---

## 6. Threat Model & Security Controls (STRIDE Matrix)

| Threat Category | Potential Vector | Security Control in Platform |
| :--- | :--- | :--- |
| **Spoofing** | Adversary posts forged webhook events pretending to be Vercel. | Constant-time HMAC signature verification (`x-vercel-signature`) against workspace secretbox secret. |
| **Tampering** | Stored database rows tampered to redirect deployments. | AES-256-GCM 16-byte authentication tag detects any database-level ciphertext alteration. |
| **Repudiation** | Operator denies triggering an unapproved production deployment. | Immutable `deployment_operation` records caller identity, timestamp, commit SHA, and approval reference. |
| **Information Disclosure** | Token leaked via GET endpoint, audit logs, or error responses. | Write-only API contract, log sanitization middleware, token hint masking (`tok_...xyz`). |
| **Denial of Service** | Flooding webhook endpoint with massive malicious payloads. | `http.MaxBytesReader` limits request size to 1MB; fast signature check aborts invalid payloads early. |
| **Elevation of Privilege** | Autonomous agent triggers unauthorized production deployment. | Hard boundary: Production deploys require human token / Level 2 confirmation; agents cannot deploy to production without approval. |

---

## 7. Verification and Testing Mandate

All implementations under this specification must satisfy the following automated test suites:
1. `TestSecretBox_KeyRing`: Validates multi-key rotation, sealing with primary key, opening with older keys, and rejection of unregistered keys.
2. `TestSecretBox_LegacyFallback`: Proves unversioned ciphertexts continue to decrypt seamlessly alongside versioned ciphertexts.
3. `TestWebhook_ConstantTimeVerification`: Proves constant-time HMAC validation rejects tampered bodies, invalid signatures, and empty secrets.
4. `TestCredential_WriteOnlyMasking`: Proves token masking displays safe hints while hiding full entropy.
