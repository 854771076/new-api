---
name: new-api-oauth-integration
description: Guide for integrating an external system with new-api as the OAuth 2.0 / OIDC identity provider, then syncing users and quota and driving top-up and subscription payments through the dashboard APIs.
---

# new-api External Integration Guide

This skill helps you integrate an external system (web app, SaaS, enterprise
tool) with new-api. It covers three surfaces:

1. **Identity** — users log in through new-api OAuth 2.0 / OIDC.
2. **AI relay** — your system calls new-api AI endpoints on the user's behalf.
3. **User and payment system** — your system reads quota and usage, and starts
   top-ups or subscription purchases through the dashboard APIs.

Surfaces 1 and 2 use OAuth tokens. Surface 3 uses a dashboard session or a
personal access token; the two credential families are not interchangeable.

## Prerequisites

1. A new-api instance running and accessible over HTTPS.
2. Admin access to the new-api dashboard to create an OAuth application.
3. One of:
   - a backend (BFF) that can store `client_secret` (confidential client), or
   - a browser SPA / mini-program that uses PKCE with a **public client**
     (`public_client = true`, no `client_secret`). Supabase direct mode uses
     this option.

## Credential Surfaces

new-api exposes several API surfaces and each accepts a different credential.
Sending the wrong credential is the most common integration failure.

| Surface | Endpoints | Accepted credential |
|---------|-----------|---------------------|
| OAuth / OIDC | `/oauth/authorize`, `/oauth/token`, `/oauth/userinfo`, `/oauth/introspect`, `/oauth/revoke`, `/.well-known/openid-configuration` | OAuth access token / client credentials |
| AI relay | `/v1/*` | OAuth access token (`api:read` for GET/HEAD, `api:write` for other methods), or a new-api API token (`sk-...`) |
| Dashboard / user system | `/api/user/*`, `/api/user/topup*`, `/api/user/pay`, `/api/subscription/*` | Browser session or personal access token (`nap_...`) |
| Administration | `/api/user/` admin routes, `/api/subscription/admin/*` | Admin or root session, or admin personal access token |

Consequences for the integration:

- An OAuth access token is accepted **only** by the AI relay and
  `/oauth/userinfo`. Every `/api/*` dashboard endpoint rejects it, including
  quota and payment endpoints.
- Quota, usage, top-up, and subscription calls therefore need a dashboard
  session or a personal access token.
- Personal access tokens are created by the user in the dashboard; the creation
  endpoint requires a browser session and a security proof, and a PAT cannot
  mint another PAT.

## Step 0: Environment Bootstrap and Self-Check

Before touching any app code, install the toolchain and prove the two sides of
the trust chain agree. Run every command in the working copy you are building.

### 0.1 Install the CLIs

```bash
# Bun + Go + Supabase CLI + cloudflared
winget install Oven-sh.Bun GoLang.Go Supabase.CLI Cloudflare.cloudflared   # Windows
brew install oven-sh/bun/bun go supabase/tap/supabase cloudflared          # macOS

# new-api admin CLI (bundled with the newapi-cli skill)
newapi-cli --version
```

`newapi-cli` is the supported way to create the OAuth app from the terminal:

```bash
newapi-cli login --server https://your-gateway.com
newapi-cli oauth-apps create \
  --name "My App" \
  --redirect-uri "https://<project>.supabase.co/auth/v1/callback" \
  --scopes openid,profile,email,api:read,api:write \
  --public-client
```

If `oauth-apps` is unavailable in your CLI version, create the app in the
dashboard instead (Step 1); the result is the same `nac_...` client_id.

### 0.2 Log in and link Supabase

```bash
supabase login
supabase link --project-ref <project-ref>
supabase db push          # applies supabase/migrations (or schema.sql)
supabase functions deploy user-sync
```

### 0.3 Run the self-check

The skill ships `scripts/check-env.ps1` (Windows) and `scripts/check-env.sh`
(macOS/Linux). They verify:

1. `bun`, `go`, `supabase`, `cloudflared`, `newapi-cli` are installed;
2. `GATEWAY_BASE_URL`, `GATEWAY_PUBLIC_URL`, `SUPABASE_URL`,
   `SUPABASE_SECRET_KEY` are set;
3. `discovery.issuer` equals `GATEWAY_PUBLIC_URL`;
4. the Supabase provider `custom:newapi` exists and its issuer matches;
5. `GET <supabase>/auth/v1/authorize?provider=custom:newapi` returns a redirect
   (302/303/307) instead of an error.

```powershell
$env:GATEWAY_BASE_URL = 'http://localhost:3000'
$env:GATEWAY_PUBLIC_URL = 'https://your-public-gateway'
$env:SUPABASE_URL = 'https://<project>.supabase.co'
$env:SUPABASE_SECRET_KEY = '<secret key>'
./scripts/check-env.ps1
```

```bash
GATEWAY_BASE_URL=http://localhost:3000 \
GATEWAY_PUBLIC_URL=https://your-public-gateway \
SUPABASE_URL=https://<project>.supabase.co \
SUPABASE_SECRET_KEY=... ./scripts/check-env.sh
```

Any failure exits non-zero and prints the fix. Do not continue to Step 1 until
the script passes.

## Step 1: Create an OAuth Application

In the new-api admin dashboard, go to **OAuth Apps** and create an application:

- **Name**: Your application's display name
- **Redirect URIs**: your callback. Use `https://<project>.supabase.co/auth/v1/callback`
  for Supabase direct mode, or `https://api.your-app.com/oauth/callback` for a BFF
- **Post-Logout Redirect URIs**: `https://your-app.com/logout-done`
- **Public client**: enable for SPAs and mini-programs (PKCE only, no secret)
- **Scopes**: Select the permissions your app needs:
  - `openid` — required for OIDC (user identity)
  - `profile` — username and display name
  - `email` — email address
  - `api:read` — read-only AI API access (model list, usage)
  - `api:write` — full AI API access (chat, images, etc.)

A confidential client returns `client_id` and `client_secret`; the secret is
shown **only once** — store it in backend environment variables. A **public
client** returns only `client_id`: it never has a secret, authentication relies
on PKCE, and `code_verifier` stays mandatory. Do not enable public mode for a
server-side app that can keep a secret.

## Step 2: Configure Your Backend (BFF)

Install the SDK:

```bash
npm install @new-api/oauth-client
```

Create the OAuth client (server-side only):

```typescript
import { createOAuthClient, generatePKCEPair, generateState } from '@new-api/oauth-client';

export const oauthClient = createOAuthClient({
  baseUrl: process.env.NEW_API_URL!,          // https://ai.example.com
  clientId: process.env.NEW_API_CLIENT_ID!,    // nac_xxx
  clientSecret: process.env.NEW_API_CLIENT_SECRET!,
  redirectUri: process.env.NEW_API_REDIRECT_URI!, // https://api.your-app.com/oauth/callback
});
```

## Step 3: Redirect Users to Authorize

On your login page or "Sign in with NewAPI" button, redirect the user:

```typescript
app.get('/auth/newapi', (req, res) => {
  const { codeVerifier, codeChallenge } = generatePKCEPair();
  const state = generateState();

  // Store codeVerifier + state in the user's session (server-side).
  req.session.oauth = { codeVerifier, state };

  const url = oauthClient.buildAuthorizeUrl({
    scope: 'openid profile email api:read api:write',
    state,
    codeChallenge,
  });
  res.redirect(url);
});
```

## Step 4: Handle the Callback

Your BFF callback endpoint exchanges the code for tokens:

```typescript
app.get('/oauth/callback', async (req, res) => {
  const { code, state } = req.query;
  const { codeVerifier, state: expectedState } = req.session.oauth || {};

  // CSRF check
  if (state !== expectedState) {
    return res.status(400).send('Invalid state');
  }

  // Exchange code for tokens
  const tokens = await oauthClient.exchangeCode(code as string, codeVerifier);

  // Get user info
  const user = await oauthClient.userinfo(tokens.access_token);

  // Create or update your local user record, link via user.sub (new-api user ID)
  // Store tokens securely (encrypted) in your DB linked to the user.

  // Set your own session cookie.
  req.session.userId = findOrCreateUser(user).id;
  req.session.newApiTokens = tokens; // store refresh_token encrypted

  res.redirect('/dashboard');
});
```

## Step 5: Call AI APIs on Behalf of the User

Proxy AI requests through your BFF using the user's access token:

```typescript
app.post('/api/chat', async (req, res) => {
  const tokens = req.session.newApiTokens;

  // Refresh the access token if expired.
  if (isExpired(tokens)) {
    const refreshed = await oauthClient.refreshToken(tokens.refresh_token);
    req.session.newApiTokens = refreshed;
  }

  const aiRes = await fetch(`${process.env.NEW_API_URL}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${req.session.newApiTokens.access_token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(req.body),
  });

  aiRes.body.pipe(res);
});
```

## Step 6: Handle Logout

When the user logs out of your system, also end their new-api session:

```typescript
app.post('/logout', async (req, res) => {
  const tokens = req.session.newApiTokens;
  if (tokens?.refresh_token) {
    await oauthClient.revoke(tokens.refresh_token);
  }
  req.session.destroy();
  res.redirect('/');
});
```

## Supabase Direct Mode (No BFF Required)

When your app uses Supabase as the backend (Auth, PostgREST, Storage, Edge
Functions), you can skip the BFF entirely. Supabase Auth acts as the OIDC
client, and the browser talks directly to new-api for AI calls.

### Architecture

```
Browser / Mini-Program
  ├─ Supabase Auth ──► new-api OIDC Provider (login, JWT)
  ├─ Supabase PostgREST ─► PostgreSQL + RLS (business data)
  └─ new-api /v1/* ─► AI relay (direct, with OAuth token)
```

### Supabase Custom Provider Setup

Create the provider through the Supabase **Auth admin API** (Dashboard:
Auth → Sign In / Providers → Custom Providers). Two rules are easy to get
wrong:

- the identifier **must** start with `custom:` — use `custom:newapi`;
- the gateway app should be a **public client**: Supabase's admin API still
  requires a non-empty `client_secret` field, so send a throwaway placeholder
  (for example `ignored-by-public-client`). new-api ignores the secret for public
  clients and validates PKCE instead. The placeholder is never used as a real
  credential, and the browser refresh call sends no secret at all.

```bash
curl -X POST "https://<project>.supabase.co/auth/v1/admin/custom-providers" \
  -H "apikey: $SUPABASE_SECRET_KEY" -H "Authorization: Bearer $SUPABASE_SECRET_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "provider_type": "oidc",
    "identifier": "custom:newapi",
    "name": "new-api",
    "issuer": "https://your-gateway.com",
    "client_id": "nac_xxxxxxxx",
    "client_secret": "ignored-by-public-client",
    "scopes": ["openid", "profile", "email", "api:read", "api:write"],
    "pkce_enabled": true,
    "email_optional": true
  }'
```

Notes:

- The Auth service fetches the issuer's discovery document when the provider is
  created, so the gateway must already be publicly reachable over HTTPS and
  `ServerAddress` must equal the issuer. `discovery` must list `none` in
  `token_endpoint_auth_methods_supported` (public client).
- `supabase config`/`config push` cannot declare custom OIDC providers, and the
  Management API has no provider CRUD. Do not hand-insert into
  `auth.custom_oauth_providers` with a bare identifier — Auth looks up
  `custom:<identifier>` and keeps returning `custom provider ... not found`.
- `email_optional: true` is required when the gateway user has no email.
- Redirect URL registered in the new-api OAuth app:
  `https://<project>.supabase.co/auth/v1/callback`
- If you must rotate credentials, delete the old provider first
  (`DELETE /auth/v1/admin/custom-providers/custom:newapi`, identifier with the
  `custom:` prefix) and create it again; editing the new-api OAuth app from
  confidential to public does not update Supabase on its own.

**Map claims**: In Supabase Auth settings, ensure `app_metadata` includes
`gateway_user_id` and `client_id`. The `user-sync` Edge Function handles this on
first login.

**RLS**: Every business table must have `client_id text not null` and use
`current_app_client_id()` (security definer function) to isolate rows by
OAuth app. See `references/web/outfit-demo/supabase/schema.sql`.

### Client-Side Flow

The browser uses `@supabase/supabase-js` with the custom provider. The provider
id is the full identifier, `custom:newapi`:

```typescript
import { createClient } from '@supabase/supabase-js';

const supabase = createClient(SUPABASE_URL, SUPABASE_ANON_KEY);

// Login redirects to the gateway authorize endpoint through Supabase.
await supabase.auth.signInWithOAuth({
  provider: 'custom:newapi',
  options: { redirectTo: window.location.origin + '/auth/callback' },
});

// After the callback, Supabase stores the gateway tokens on the session.
const { data: { session } } = await supabase.auth.getSession();
const gatewayToken = session?.provider_token;          // 1 hour TTL
const gatewayRefreshToken = session?.provider_refresh_token;
```

Then, **before** the access token expires, refresh it through the gateway. This
is mandatory: Supabase does not refresh a custom provider's token.

```typescript
async function refreshGatewayToken(refreshToken: string): Promise<string> {
  const res = await fetch(`${GATEWAY_BASE_URL}/oauth/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'refresh_token',
      client_id: GATEWAY_CLIENT_ID,       // no client_secret for a public client
      refresh_token: refreshToken,
    }).toString(),
  });
  if (!res.ok) throw new Error(`gateway refresh failed: ${res.status}`);
  const data = await res.json();
  // Persist data.refresh_token: every refresh rotates and revokes the old one.
  return data.access_token;
}
```

Keep the token in memory (optionally `sessionStorage` keyed by Supabase user
id), never in `user_metadata`. The skill ships a complete implementation in
`references/web/outfit-demo/src/lib/gatewayAuth.ts`:

- parses `exp` from the access token JWT and refreshes when less than 60 s remain;
- deduplicates concurrent refreshes with one in-flight promise;
- retries a `401` from the relay exactly once, then surfaces the error;
- on `invalid_grant`, clears local state and signs the user out.

```typescript
// Direct AI call (no BFF):
const res = await fetch('https://your-gateway.com/v1/chat/completions', {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${await getGatewayAccessToken()}`,
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [...] }),
});
```

### Key Differences from BFF Mode

| Aspect | BFF Mode | Supabase Direct Mode |
|--------|----------|----------------------|
| Token storage | Server-side session / DB | Supabase Auth session + app memory |
| Token refresh | BFF handles automatically | **Your app refreshes** `provider_refresh_token` via `/oauth/token` |
| AI calls | Proxied through BFF | Browser direct to gateway |
| client_secret | Required in BFF env | Not needed (PKCE public client) |
| Data isolation | Application-level | PostgreSQL RLS + client_id |
| Business logic | Your backend | Supabase Edge Functions |

### Starter Template

Use `references/web/outfit-demo/` as the scaffold (the starter plus two business
pages). It includes:
- Pre-configured Supabase client with OIDC login
- `src/lib/gatewayAuth.ts` for gateway token refresh
- `src/lib/gateway.ts` for direct AI calls (streaming + non-streaming, 401 retry)
- `schema.sql` with RLS, `current_app_client_id()`, and seed data
- `user-sync` Edge Function for profile sync

Copy it, fill `.env.local`, and deploy:

```bash
cp -r references/web/outfit-demo my-app
cd my-app
cp .env.example .env.local
# Edit .env.local: SUPABASE_URL, SUPABASE_ANON_KEY, GATEWAY_BASE_URL, GATEWAY_CLIENT_ID
bun install
bun run dev
```

The same scaffold is kept in the repository at `templates/supabase-app-starter/`
without the demo business pages and without pre-filled project values.

### Reference App: outfit-demo

`references/web/outfit-demo/` is a runnable reference app built on the template.
Use it to verify the whole chain (OIDC login → RLS data query → AI relay) and
to copy working page patterns instead of starting from scratch.

It adds two business pages on top of the template:

| Page | File | Shows |
|------|------|-------|
| 服装库 | `src/pages/Wardrobe.tsx` | PostgREST read with category filter, RLS isolation by `client_id` + `auth.uid()` |
| AI 换装 | `src/pages/OutfitGenerate.tsx` | Multi-select from business tables + streaming AI call + write-back to `ai_generations` |

Run it:

```bash
cd references/web/outfit-demo
cp .env.example .env.local   # Supabase URL/anon key are pre-filled; set GATEWAY_BASE_URL
bun install
bun run dev
```

Its Supabase project already has the schema and seed data applied, so the
服装库 page renders 8 official outfits right after login.

#### Local verification notes

- **Set `SESSION_SECRET` before testing.** Without it new-api generates a random
  secret per process, so every restart invalidates existing refresh cookies and
  the next `/oauth/authorize` navigation bounces back to `/sign-in`. Fix the
  value in `.env` once, then restarts keep your browser login.
- `/oauth/authorize` authenticates a browser top-level navigation from the
  refresh cookie (`Path=/`, HttpOnly) because that request carries no
  `Authorization` header. If the gateway and the frontend run on the same host,
  the cookie set by the login API is sent to the authorize URL too.
- A Supabase **cloud** project cannot complete the flow against
  `http://localhost`: its server exchanges the code at the issuer URL, and Supabase
  rejects an issuer that is not the project's public HTTPS address. For a full
  cloud test expose the gateway over HTTPS (tunnel or deployed host), put that
  origin in new-api's server address setting, and only then register the
  Supabase project as a `custom:newapi` provider. Locally you can verify up to
  the consent redirect (`302 /oauth/consent`) without Supabase.

### Model Whitelist per App

Each OAuth App in new-api can restrict which AI models are available:

```sql
-- In new-api admin, set on the OAuth App:
ai_model_whitelist = '["gpt-4o-mini","gpt-4o"]'
```

When set, the relay rejects any model not in the list. Leave empty to allow
all models. This is enforced in `controller/relay.go` via
`service.CheckOAuthAppModelWhitelist()`.

## Scopes Reference

| Scope | Grants access to |
|-------|-----------------|
| `openid` | User ID (sub claim) — required for OIDC |
| `profile` | Username, display name |
| `email` | Email address |
| `api:read` | Read-only AI endpoints (`GET` / `HEAD` / `OPTIONS`, e.g. `/v1/models`) |
| `api:write` | Non-read AI endpoints (chat, images, embeddings, ...). Request both if the app also reads. |

These scopes belong to OAuth access tokens only. Dashboard personal access
tokens use a separate catalog (`profile:read`, `profile:write`,
`account_security:write`, `wallet:read`, `wallet:write`, `billing:read`,
`billing:write`, ...); list it with `GET /api/user/access_tokens/scopes`.

## Security Checklist

- [ ] Confidential clients keep `client_secret` only in backend environment
      variables; public clients have no secret and rely on PKCE.
- [ ] Public-client mode is enabled only for apps that cannot keep a secret
      (SPA, mini-program); server-side apps stay confidential.
- [ ] `redirect_uri` exactly matches a registered URI (BFF callback or
      `https://<project>.supabase.co/auth/v1/callback`).
- [ ] PKCE `code_verifier` is matched on callback; a public client without it is
      rejected.
- [ ] `state` parameter is validated to prevent CSRF.
- [ ] Gateway tokens are kept in memory (optionally `sessionStorage`) and never
      written to `user_metadata` or logged.
- [ ] Refreshed refresh tokens replace the old value; `invalid_grant` ends the
      session instead of retrying.
- [ ] HTTPS is enabled in production (required for `SameSite=None` cookies).
- [ ] OAuth tokens are used only for `/v1/*` and `/oauth/userinfo`; quota and
      payment calls use a dashboard session or a scoped PAT.
- [ ] Personal access tokens are stored encrypted and carry only the scopes the
      integration actually needs.
- [ ] Your system never treats a browser return URL or a `pending` order as
      proof of payment; quota is credited only after new-api confirms the
      provider payment.

## Token Lifecycle

- **Access token**: JWT, 1 hour TTL, used to call AI APIs and userinfo.
- **Refresh token**: Opaque, 30 day TTL, rotated on each use.
- **Authorization code**: Single-use, 60 second TTL.

### The provider token is not refreshed for you

Supabase refreshes its own session, but it does **not** refresh a custom
provider's `provider_token`. After one hour every direct AI call would fail with
`401`. The application must refresh it:

1. on login, read `session.provider_token` and `session.provider_refresh_token`;
2. before each gateway call, check the access token's `exp`; refresh when less
   than 60 s remain;
3. `POST <gateway>/oauth/token` with `grant_type=refresh_token`, `client_id`, and
   `refresh_token` (public client: no `client_secret`);
4. replace the stored refresh token with the rotated value from the response;
5. on `invalid_grant`, clear local state and send the user through login again.

Refresh is also the recovery path for a relay `401`: retry once with a fresh
token, never loop. `references/web/outfit-demo/src/lib/gatewayAuth.ts` is the
reference implementation.

When the user logs out of new-api or changes their password, all their tokens
are immediately invalidated. Handle `401` by re-authenticating; do not keep
retrying with the same token.

## Long-Running Work and Supabase Limits

Supabase request and function limits change the architecture of any job that
takes more than a few seconds.

| Surface | Limit | Consequence |
|---------|-------|-------------|
| PostgREST as `anon` | 3 s statement timeout | Guest reads must stay trivial |
| PostgREST as `authenticated` | 8 s statement timeout | No large synchronous aggregation in the browser |
| PostgREST as `service_role` | No statement timeout | Long SQL only from trusted server code |
| Edge Function wall clock | 150 s (Free) / 400 s (paid) | A function cannot outlive these bounds |
| Edge Function CPU time | 2 s | CPU-heavy loops fail even inside the wall clock |
| Edge Function idle before response | 150 s | No silent work while the client waits |

Recommended patterns:

- **Short work (seconds)**: call the gateway or PostgREST directly from the
  browser. Keep the OAuth token refresh in the request path.
- **Medium work (tens of seconds)**: an Edge Function, under both the wall clock
  and CPU limits. Stream or return a job id.
- **Long work (minutes+)**: do not hold the HTTP request. Insert a job row,
  return its id immediately, run the work outside the request (scheduled
  function / external worker), and let the client poll or subscribe. A gateway
  AI stream longer than the function limit must be consumed by the client or a
  worker, not buffered inside an Edge Function.

## Integrating the User System

The user system lives under `/api/*`, which OAuth access tokens cannot reach.
Use a dashboard session or a personal access token for everything in this
section.

### Identity payload

`GET /oauth/userinfo` with `Authorization: Bearer <OAuth access token>`:

| Field | Present when scope | Meaning |
|-------|--------------------|---------|
| `sub` | always | new-api user ID as a string. Stable identity key. |
| `username` | `profile` | Login name. |
| `name` | `profile` | Display name. |
| `email` | `email` | Email address; may be empty. |

Linking rules:

- Store `sub` as your user's `newapi_user_id`. Never key accounts on `username`
  or `email`: both are mutable and email may be absent.
- OAuth login does not create new-api users. The user must already exist in
  new-api, either self-registered or provisioned by an administrator.
- If your system owns the account lifecycle, create the new-api user first,
  then let the user sign in with OAuth.

Provision a user (admin session, or admin PAT with the matching `user` scope):

```http
POST /api/user/
Content-Type: application/json

{
  "username": "alice",
  "password": "initial-password",
  "display_name": "Alice",
  "role": 1
}
```

`role` values: `1` common user, `10` administrator, `100` root. An administrator
can only create roles lower than its own; creating an administrator requires an
additional security proof.

### Reading quota and usage

OAuth `userinfo` deliberately exposes no quota. Read it with a dashboard
credential:

- User PAT with `profile:read` — `GET /api/user/self`
- Admin session or admin PAT — `GET /api/user/:id` for any user,
  `GET /api/user/?p=1&page_size=20` for the paged list, and
  `GET /api/user/search?keyword=&group=&role=&status=&p=&page_size=` for filtered
  search

`GET /api/user/self` returns the dashboard user DTO. Fields useful to an
external system:

| Field | Meaning |
|-------|---------|
| `id` | User ID (same value as `sub`) |
| `username`, `display_name` | Names |
| `role`, `status` | Role (`1` / `10` / `100`) and account status |
| `email` | Email address |
| `group` | Billing group |
| `quota` | Remaining wallet balance, in quota units |
| `used_quota` | Cumulative consumption, in quota units |
| `request_count` | Relay request count |
| `aff_code`, `aff_count`, `aff_quota`, `aff_history_quota` | Invitation data |
| `permissions` | Dashboard capability flags; do not use as your own authorization source |

### Quota units

`quota` and `used_quota` are integer quota units, not money.

- `QuotaPerUnit` defaults to `500000`, i.e. 1 USD = 500,000 quota units.
- Displayed money = `quota / QuotaPerUnit`, converted with the configured
  exchange rate when the display currency is CNY.
- Payment `amount` fields are in the configured display type (money amount or
  token count), **not** in quota units. Never send a `quota` value as a payment
  `amount`.
- `quota` is the wallet balance only. An active subscription can still cover
  relay usage when the wallet is empty; read `GET /api/subscription/self` to
  judge the full billing state.

### Personal access tokens (PAT)

Users create PATs in the dashboard (Security → Access Tokens). With a browser
session the same call is:

```http
POST /api/user/access_tokens
Content-Type: application/json

{
  "name": "external-crm",
  "scopes": ["profile:read", "wallet:read", "wallet:write"],
  "expires_at": 0
}
```

- The raw token starts with `nap_`, is shown once, and is sent as
  `Authorization: Bearer nap_...`.
- `expires_at` is a Unix timestamp; `0` means no expiry. A non-zero value must
  be at least one hour in the future.
- Creation requires a browser session and a security proof; a PAT cannot create
  another PAT.
- Each dashboard route declares its required scope. Common scopes:
  `profile:read`, `profile:write`, `account_security:write`, `wallet:read`,
  `wallet:write`, `billing:read`, `billing:write`.
- `GET /api/user/access_tokens/scopes` lists the full scope catalog; it also
  requires a browser session.
- A PAT never exceeds the user's role: admin routes still require an
  admin or root account.
- PAT-authenticated requests have no browser `SessionID`, so they cannot list
  or revoke dashboard sessions.

### Session management

When your own frontend embeds the new-api dashboard, these endpoints manage
browser sessions:

- `GET /api/user/sessions` — list active sessions
- `DELETE /api/user/sessions/:sid` — revoke one session
- `POST /api/user/sessions/revoke-others` — revoke every other session

They reject PATs, so they require an actual browser session.

## Integrating the Payment System

Payment endpoints live under `/api/user/*` and `/api/subscription/*`, so they
require a dashboard session or a PAT. A practical PAT scope set is
`wallet:read` + `wallet:write`.

Payment endpoints answer with `{"message": "success" | "error", "data": ...}`
over HTTP 200 rather than the usual `{"success": ...}` envelope. Redemption
(`POST /api/user/topup`) is the exception and uses the standard envelope.

### Compliance gate

Top-up, redemption, subscription purchase, and invitation rewards stay disabled
until an administrator confirms the payment compliance terms. Check first:

```http
GET /api/user/topup/info
Authorization: Bearer nap_...
```

Use `payment_compliance_confirmed` and `payment_compliance_terms_version` from
the response. While it is `false`, payment calls return errors and
`GET /api/subscription/plans` returns an empty list.

### Available channels

`GET /api/user/topup/info` (scope `wallet:read`) also reports what is enabled:

| Field | Meaning |
|-------|---------|
| `enable_online_topup` | epay gateway |
| `enable_stripe_topup` | Stripe |
| `enable_creem_topup` | Creem |
| `enable_waffo_topup` | Waffo |
| `enable_waffo_pancake_topup` | Waffo Pancake |
| `enable_redemption` | Redemption codes |
| `pay_methods` | Methods as `{name, type, color, min_topup}` |
| `min_topup`, `stripe_min_topup`, `waffo_min_topup`, `waffo_pancake_min_topup` | Minimum amount per channel |
| `amount_options`, `discount` | Preset amounts and their discount ratios |
| `waffo_pay_methods`, `creem_products` | Channel-specific methods / products |
| `topup_link` | Optional external top-up URL |

`amount` is an **integer** in the configured display unit. These endpoints do
not accept fractional currency amounts.

### Top-up order APIs

All require scope `wallet:write`. Every one is rate limited, so do not retry
aggressively.

| Channel | Request | Response |
|---------|---------|----------|
| epay gateway | `POST /api/user/pay` `{"amount":10,"payment_method":"alipay"}` | `{"message":"success","url":"https://...","data":{...}}` |
| Stripe | `POST /api/user/stripe/pay` `{"amount":10,"payment_method":"stripe","success_url":"...","cancel_url":"..."}` | `data.pay_link` |
| Creem | `POST /api/user/creem/pay` `{"product_id":"...","payment_method":"creem"}` | `data.checkout_url`, `data.order_id` |
| Waffo | `POST /api/user/waffo/pay` `{"amount":10,"pay_method_index":0}` (`pay_method_index` omitted = auto) | `data.payment_url`, `data.order_id` |
| Waffo Pancake | `POST /api/user/waffo-pancake/pay` `{"amount":10}` | `data.checkout_url`, `data.session_id`, `data.expires_at`, `data.order_id`, `data.token`, `data.token_expires_at` |
| Redemption code | `POST /api/user/topup` `{"key":"REDEEM-CODE"}` | `{"success":true,"data":<credited quota>}` |

For epay, send the user to `url` with the returned `data` parameters. For Stripe,
Creem, and Waffo Pancake, redirect to the returned checkout link.

Amount preview (scope `wallet:read`): `POST /api/user/amount`,
`POST /api/user/stripe/amount`, `POST /api/user/waffo/amount`, and
`POST /api/user/waffo-pancake/amount` with `{"amount":10}` return the payable
money as a string.

### Order status and crediting

- new-api credits quota only after the payment provider confirms the payment
  (epay notify, or Stripe / Creem / Waffo webhooks). A browser return URL is
  not proof of payment.
- Poll `GET /api/user/topup/self` (scope `wallet:read`) for the user's orders.
  Each item contains `trade_no`, `amount`, `money`, `payment_method`,
  `payment_provider`, `status`, `create_time`, `complete_time`.
- `status` values are `pending`, `success`, `failed`, `expired`. Only `success`
  means quota was credited.
- Callbacks are idempotent: a repeated provider notification does not credit
  twice.
- If a real payment is missing from new-api, an administrator can complete the
  order with `POST /api/user/topup/complete` `{"trade_no":"..."}` (admin session,
  or admin PAT with `billing:write`).

### Subscriptions

`GET /api/subscription/plans` (scope `wallet:read`) returns
`[{ "plan": { ... } }]`. Relevant plan fields: `title`, `price_amount`,
`currency`, `duration_unit`, `duration_value`, `total_amount` (quota units,
`0` = unlimited), `allow_balance_pay`, `allow_wallet_overflow`,
`max_purchase_per_user` (`0` = unlimited), `upgrade_group`,
`quota_reset_period`.

Purchase (scope `wallet:write`):

| Method | Request |
|--------|---------|
| Wallet balance | `POST /api/subscription/balance/pay` `{"plan_id":1}` — deducted and activated synchronously |
| epay | `POST /api/subscription/epay/pay` `{"plan_id":1,"payment_method":"alipay"}` |
| Stripe | `POST /api/subscription/stripe/pay` `{"plan_id":1}` |
| Creem | `POST /api/subscription/creem/pay` `{"plan_id":1}` |
| Waffo Pancake | `POST /api/subscription/waffo-pancake/pay` `{"plan_id":1}` |

External channels return the same `url` / `pay_link` / `checkout_url` shapes as
top-ups; balance payment returns `success: true` immediately.

Read subscription state with `GET /api/subscription/self` (scope `wallet:read`):

```json
{
  "billing_preference": "subscription_first",
  "subscriptions": [ ... active ... ],
  "all_subscriptions": [ ... including expired ... ]
}
```

Subscription fields: `plan_id`, `amount_total` and `amount_used` (quota units),
`start_time`, `end_time`, `status` (`active` / `expired` / `cancelled`),
`next_reset_time`, `allow_wallet_overflow`, `upgrade_group`.

`PUT /api/subscription/self/preference` (scope `wallet:write`) accepts one of
`subscription_first`, `wallet_first`, `subscription_only`, `wallet_only`.
Use `wallet_only` to keep users off subscription quota, or `subscription_only`
to prevent falling back to wallet balance.

### Administration

Admin session, or admin PAT with `billing:read` / `billing:write`:

- `GET /api/user/` and `GET /api/user/topup` — user list and all top-up orders
- `GET /api/subscription/admin/plans`, `POST /api/subscription/admin/plans`,
  `PUT /api/subscription/admin/plans/:id`,
  `PATCH /api/subscription/admin/plans/:id`
- `POST /api/subscription/admin/bind` `{"user_id":1,"plan_id":1}` — grant a
  plan without payment
- `GET|POST /api/subscription/admin/users/:id/subscriptions`
- `POST /api/subscription/admin/users/:id/subscriptions/reset`
- `POST /api/subscription/admin/user_subscriptions/:id/invalidate`
- `DELETE /api/subscription/admin/user_subscriptions/:id`
