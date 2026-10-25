# Deployment Providers Platform — Setup & Configuration Guide

**Audience:** Platform Operators, Cloud Administrators, and Self-Hosted Multica Users  
**Subsystem:** Deployment Providers Control Plane (Vercel Connector Reference)  

---

## 1. Introduction

Multica provides a provider-neutral deployment control plane enabling engineering teams to connect external cloud hosting platforms (such as Vercel) directly to their workspaces. Once connected, Multica projects can map to remote hosting services as deployment targets, trigger preview or production builds from specific git commits, ingest live status webhooks, and surface build logs and URLs.

This guide details the setup instructions for:
- **Multica Cloud (SaaS):** Multi-tenant OAuth 2.0 Integration App configuration.
- **Self-Hosted Multica:** Personal Access Token (PAT), with optional Vercel Account Webhooks.

---

## 2. Multica Cloud: Vercel Sign in with Vercel OAuth Setup

For Multica Cloud instances, Vercel connects through Vercel's Authorization
Server (Sign in with Vercel). This is a connectable OAuth application, not a
Vercel Marketplace native integration.

### 2.1 Registering the Vercel OAuth App

1. In the Vercel team that owns the app, open **Settings → Apps → Create**.
2. Choose a server-side client authentication method such as
   `client_secret_basic`, then generate a client secret.
3. Register the exact callback URL configured for Multica. For the managed
   service this is normally:
   `https://app.multica.ai/api/integrations/deployment/vercel/callback`.
   The URL must also be supplied to Multica through
   `MULTICA_DEPLOYMENT_VERCEL_OAUTH_REDIRECT_URL`; Multica never derives it
   from request `Host` headers.
4. Enable only the documented Sign in with Vercel permissions required by
   this flow: `openid`, `email`, `profile`, and `offline_access`.
5. Note the generated **Client ID** and **Client Secret**. The OAuth token
   response is stored encrypted by Multica and is never returned to the UI.

Sign in with Vercel does not provision a deployment webhook signing secret.
If webhook ingestion is needed, configure the provider-side secret separately
and save it through the connection's dedicated webhook-secret action. It is
sealed independently from the OAuth access/refresh token envelope.

### 2.2 Configuring Cloud Server Environment Variables

On the Multica Cloud backend instances, set the following environment variables:

```bash
# Vercel OAuth Credentials (not enabled in the self-hosted PAT flow)
MULTICA_DEPLOYMENT_VERCEL_CLIENT_ID="oac_xxxxxxxxxxxx"
MULTICA_DEPLOYMENT_VERCEL_CLIENT_SECRET="sec_xxxxxxxxxxxx"
MULTICA_DEPLOYMENT_VERCEL_OAUTH_REDIRECT_URL="https://app.multica.ai/api/integrations/deployment/vercel/callback"

# Deployment credential encryption (AES-256-GCM SecretBox)
# Format: base64-encoded 32-byte key; generate with: openssl rand -base64 32
MULTICA_DEPLOYMENT_SECRET_KEY="<base64-encoded-32-byte-key>"
# Optional rotation form: "1:<base64-key>,2:<base64-key>"
# MULTICA_DEPLOYMENT_SECRET_KEYRING="1:<base64-key>"
# MULTICA_DEPLOYMENT_SECRET_PRIMARY_KEY="1"
```

### 2.3 User Experience in Multica Cloud

1. A workspace owner or administrator navigates to **Settings → Integrations → Deployment Providers**.
2. Click **Connect Vercel**.
3. The user is redirected to `vercel.com/oauth/authorize` with a one-time state and S256 PKCE challenge.
4. Upon approval, Vercel redirects back to the registered callback endpoint.
5. Multica exchanges the code at `https://api.vercel.com/login/oauth/token`, seals the rotating access/refresh token pair into PostgreSQL, and validates the connection before marking it `active`.
6. Refresh uses the same token endpoint with `grant_type=refresh_token`; revocation uses `https://api.vercel.com/login/oauth/token/revoke`. Neither operation returns credentials to the client.

---

## 3. Self-Hosted Multica: Personal Access Token (PAT) Setup

Self-hosted Multica instances operate in private or single-tenant environments and do not require registering a public Vercel Marketplace App. Administrators configure Vercel using Personal Access Tokens (PAT).

### 3.1 Generating a Vercel Personal Access Token

1. Log in to Vercel and navigate to [Account Settings → Tokens](https://vercel.com/account/tokens).
2. Click **Create Token**.
3. Set a descriptive name (e.g. `Multica-Self-Hosted-Deployments`).
4. Select the appropriate **Scope**:
   - **Full Account:** If managing projects across all teams.
   - **Specific Team:** Recommended if deployments are restricted to a single team.
5. Set an appropriate expiration period according to your organizational security policy.
6. Copy the generated token (`vercel_tok_...`). *Note: Vercel displays this token only once.*

### 3.2 Connecting Vercel in Self-Hosted Multica UI

1. In Multica, navigate to **Settings → Integrations → Deployment Providers** (or `/settings/integrations`).
2. Click **Connect Vercel** (or **Add Provider Connection**).
3. Select **Personal Access Token (PAT)** as the authentication method.
4. Input the configuration:
   - **Connection Name:** e.g., `Engineering Production Vercel`
   - **Personal Access Token:** Paste `vercel_tok_...`
   - **Team ID / Slug (Optional):** Enter your team ID (e.g. `team_xxxx`) if deploying on behalf of a Vercel Team.
5. Click **Verify & Connect**.
   - Multica performs a real-time pre-flight handshake with Vercel's `/v2/user` and `/v2/teams` endpoints.
   - If valid, the token is encrypted at rest using the local `SecretBox` master key and stored. Plaintext tokens are zeroed from memory immediately.

### 3.3 Webhook plan and onboarding

Webhook ingestion is optional. Polling and reconciliation remain the normal
fallback and do not require a webhook secret.

| Vercel plan / connection | Supported setup |
| --- | --- |
| Hobby | PAT + polling/reconciliation. Account Webhooks cannot be created and no account-webhook signing secret is available. |
| Pro or Enterprise team | PAT + optional Vercel Account Webhook + polling/reconciliation. |
| Vercel Integration / Cloud Integration webhook | Separate integration webhook flow verified with the Integration Secret / Client Secret; never use a PAT or Account Webhook secret. |

For a Pro or Enterprise team:

1. First create the PAT connection in Multica.
2. Copy the canonical callback URL shown on the connection:
   `https://<your-multica-domain>/api/webhooks/deployments/{workspaceId}/{connectionId}`
3. In Vercel, create the Account Webhook for the team and copy the secret when it is shown once.
4. In Multica, choose **Configure webhook** for the existing connection and save the secret. Rotation replaces the old secret.

The callback URL and secret are separate values. Multica stores the dedicated
webhook secret independently from the PAT or OAuth token, and never uses a PAT
to verify webhook signatures. See Vercel's [webhook plan availability and
secret handling](https://vercel.com/docs/webhooks).

---

## 4. Attaching Deployment Targets to Projects

Once a connection is established, project administrators can map project repositories to Vercel targets:

1. Open the target project in Multica and switch to the **Deployments** tab.
2. Click **Add Deployment Target** → **Vercel**.
3. Select the active Vercel connection.
4. Multica automatically lists existing Vercel projects via `GET /v9/projects`.
5. Select the remote project and configure environment settings:
   - **Target Environment:** `preview` (default for branches/PRs) or `production` (default for main/releases).
   - **Branch Filter:** Optional branch regex pattern (e.g., `feat/*` or `main`).
   - **Root Directory:** Relative path in repository (e.g. `./apps/web`).
   - **Build & Install Commands:** Custom build command overrides if needed.
6. Click **Save Target**.

---

## 5. Security & Operational Guardrails

### 5.1 Production Deployment Confirmation

To prevent accidental outages:
- Triggering a **production** deployment or promoting a preview build to production is restricted to workspace **Owner** and **Admin** roles.
- The UI surfaces a high-visibility confirmation modal (`ProductionDeployConfirmDialog`) requiring explicit user confirmation before the backend invokes Vercel.
- API callers must pass `"confirmed": true` or a valid `"approval_reference": "..."` in the JSON request body. Requests lacking confirmation are rejected with HTTP 400 Bad Request.

### 5.2 Secret Protection & Zero-Leak Guarantee

- Provider tokens and webhook secrets are stored exclusively in the `auth_config` column as AES-256-GCM encrypted envelopes.
- All API endpoints (`GET /api/workspaces/{id}/deployment-connections`) mask credentials, returning only prefixes (e.g. `vercel_tok_****`).
- The log redaction pipeline automatically filters out bearer tokens, secrets, and auth headers from telemetry, stdout, and error responses.
- Inbound webhooks are verified using constant-time HMAC comparison (`hmac.Equal`) to eliminate timing side-channel attacks.
