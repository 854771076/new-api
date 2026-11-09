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
3. A backend (BFF) for your system that can securely store `client_secret`.

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

## Step 1: Create an OAuth Application

In the new-api admin dashboard, go to **OAuth Apps** and create an application:

- **Name**: Your application's display name
- **Redirect URIs**: `https://api.your-app.com/oauth/callback` (your BFF callback)
- **Post-Logout Redirect URIs**: `https://your-app.com/logout-done`
- **Scopes**: Select the permissions your app needs:
  - `openid` — required for OIDC (user identity)
  - `profile` — username and display name
  - `email` — email address
  - `api:read` — read-only AI API access (model list, usage)
  - `api:write` — full AI API access (chat, images, etc.)

Save the returned `client_id` and `client_secret`. The secret is shown **only
once** — store it securely in your backend environment variables.

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

### Supabase Dashboard Setup

1. **Auth → Providers → Custom Provider**:
   - **Provider name**: `new-api`
   - **Discovery URL**: `https://your-gateway.com/.well-known/openid-configuration`
   - **Client ID**: `nac_xxxxxxxx` (from new-api OAuth App)
   - **Client Secret**: (leave empty for PKCE public clients, or set if your
     gateway requires it)
   - **Scopes**: `openid profile email api:read api:write`
   - **Redirect URL**: `https://<project>.supabase.co/auth/v1/callback`

2. **Map claims**: In Supabase Auth settings, ensure `app_metadata` includes
   `gateway_user_id` and `client_id`. The `user-sync` Edge Function handles
   this on first login.

3. **RLS**: Every business table must have `client_id text not null` and use
   `current_app_client_id()` (security definer function) to isolate rows by
   OAuth app. See `templates/supabase-app-starter/supabase/schema.sql`.

### Client-Side Flow

The browser uses `@supabase/supabase-js` with the custom provider:

```typescript
import { createClient } from '@supabase/supabase-js';

const supabase = createClient(SUPABASE_URL, SUPABASE_ANON_KEY);

// Login redirects to new-api authorize endpoint
await supabase.auth.signInWithOAuth({
  provider: 'new-api',
  options: { redirectTo: window.location.origin + '/auth/callback' },
});

// After callback, Supabase session contains the OIDC tokens.
// The access_token is a new-api OAuth token — use it for AI calls:
const { data: { session } } = await supabase.auth.getSession();
const gatewayToken = session?.provider_token; // new-api OAuth access token

// Direct AI call (no BFF):
const res = await fetch('https://your-gateway.com/v1/chat/completions', {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${gatewayToken}`,
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [...] }),
});
```

### Key Differences from BFF Mode

| Aspect | BFF Mode | Supabase Direct Mode |
|--------|----------|----------------------|
| Token storage | Server-side session / DB | Supabase Auth (encrypted) |
| Token refresh | BFF handles automatically | Supabase Auth handles |
| AI calls | Proxied through BFF | Browser direct to gateway |
| client_secret | Required in BFF env | Not needed (PKCE public client) |
| Data isolation | Application-level | PostgreSQL RLS + client_id |
| Business logic | Your backend | Supabase Edge Functions |

### Starter Template

Use `templates/supabase-app-starter/` as the scaffold. It includes:
- Pre-configured Supabase client with OIDC login
- `gateway.ts` for direct AI calls (streaming + non-streaming)
- `schema.sql` with RLS, `current_app_client_id()`, and seed data
- `user-sync` Edge Function for profile sync

Copy the template, fill `.env.local`, and deploy:

```bash
cp -r templates/supabase-app-starter my-app
cd my-app
cp .env.example .env.local
# Edit .env.local: SUPABASE_URL, SUPABASE_ANON_KEY, GATEWAY_BASE_URL, GATEWAY_CLIENT_ID
bun install
bun run dev
```

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

- [ ] `client_secret` is stored only in backend environment variables, never in frontend code.
- [ ] `redirect_uri` points to your **backend** (BFF), not the frontend.
- [ ] PKCE `code_verifier` is stored server-side and matched on callback.
- [ ] `state` parameter is validated to prevent CSRF.
- [ ] Access/refresh tokens are stored encrypted in your database.
- [ ] AI API calls go through your BFF (frontend never holds new-api tokens).
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

When the user logs out of new-api or changes their password, all their tokens
are immediately invalidated. Your BFF should handle `401` responses by
redirecting the user to re-authenticate.

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
