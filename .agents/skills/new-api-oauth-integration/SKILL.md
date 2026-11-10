---
name: new-api-oauth-integration
description: Standard for building an external Web app against new-api — Next.js App Router, Supabase, Vercel, Tailwind CSS + DaisyUI, plus new-api OAuth 2.0 / OIDC identity, AI relay calls, quota / top-up / subscription billing integration, and an optional admin console (Ant Design + Refine).
---

# new-api External App Development Standard (Web)

This skill is the standard for building an external **Web** application that
uses new-api as its identity provider and AI gateway. It applies to SaaS
products, enterprise internal tools, and demos that must run the complete
chain: stack and project structure, frontend standards, the Supabase backend,
Vercel deployment, and the new-api contracts for identity, AI relay, users,
quota, top-up, and subscriptions. The identity and billing chapters are new-api
product contracts and stay valid when you deviate from the default stack; the
stack chapters are defaults, not requirements.

## Default Stack

| Layer | Default | Notes |
|-------|---------|-------|
| Framework | Next.js (App Router) | Server Components by default; Client Components only for interaction |
| Language | TypeScript strict | No `any`; validate external input at the boundary |
| UI runtime | React 19 | Use the App Router data and streaming model |
| Styling | Tailwind CSS v4 + DaisyUI 5 | Compose DaisyUI components before custom markup |
| Backend | Supabase | Auth, Postgres + RLS, Edge Functions, Storage |
| Deployment | Vercel | Git integration or `vercel --prod` |
| Package manager | bun | `bun install`, `bun run dev`, `bun run build` |
| Identity | new-api OAuth 2.0 / OIDC | Authorization Code + PKCE; confidential BFF or public client |
| Admin console (optional) | Ant Design 5 + `@refinedev/core` | Separate route tree from the consumer app; see **Step 8** |

The consumer app uses Tailwind + DaisyUI. An admin console is the one deliberate
exception: it uses Ant Design and must not share a layout with the consumer app.

You may deviate when an existing platform standard, an offline/on-premise
requirement, a non-Web target, or a regulatory constraint makes the default
stack impossible. When you deviate, only **Step 2 (Identity)** and **Step 4
(Users, Quota, and Payments)** remain normative; keep **Security Checklist**,
**Token Lifecycle**, **Scopes Reference**, and **Model Whitelist per App**, and
preserve the same security properties: server-side secret storage, PKCE, state
validation, token rotation, and no token in logs.

## Reference Index

| Topic | File |
|-------|------|
| Frontend framework, rendering, performance | `references/standards/nextjs.md`, `references/vendor/vercel-react-best-practices/` |
| Next.js role guidance (vendor skill) | `references/vendor/nextjs-react-typescript/SKILL.md` |
| UI components | `references/standards/daisyui.md` |
| UI/UX principles | `references/standards/ui-ux.md`, `references/vendor/ui-ux-pro-max/` |
| Backend on Supabase | `references/standards/supabase.md` |
| Deployment on Vercel | `references/standards/vercel.md` |
| Admin console (Next.js + Ant Design + Refine + Supabase) | `references/standards/admin-dashboard.md` |
| Runnable demo app | `references/web/outfit-demo/README.md` |
| Vendor source provenance | `references/vendor/README.md` |
| Environment self-check | `scripts/check-env.ps1`, `scripts/check-env.sh` |

Read the relevant standard before implementing that layer; this file defines the
integration contract.

## Prerequisites

1. A new-api instance running and accessible over HTTPS.
2. Admin access to the new-api dashboard to create an OAuth application.
3. One of:
   - a backend (BFF) that can store `client_secret` (confidential client), or
   - a browser SPA / mini-program that uses PKCE with a **public client**
     (`public_client = true`, no `client_secret`). Supabase direct mode uses
     this option.

## Credential Surfaces

Each new-api surface accepts a different credential. Sending the wrong one is
the most common integration failure.

| Surface | Endpoints | Accepted credential |
|---------|-----------|---------------------|
| OAuth / OIDC | `/oauth/authorize`, `/oauth/token`, `/oauth/userinfo`, `/oauth/introspect`, `/oauth/revoke`, `/.well-known/openid-configuration` | OAuth access token / client credentials |
| AI relay | `/v1/*` | OAuth access token (`api:read` for GET/HEAD, `api:write` for other methods), or a new-api API token (`sk-...`) |
| Dashboard / user system | `/api/user/*`, `/api/user/topup*`, `/api/user/pay`, `/api/subscription/*` | Browser session or personal access token (`nap_...`) |
| Administration | `/api/user/` admin routes, `/api/subscription/admin/*` | Admin or root session, or admin personal access token |

An OAuth access token is accepted **only** by the AI relay and `/oauth/userinfo`;
every `/api/*` dashboard endpoint rejects it, including quota and payment
endpoints. Quota, usage, top-up, and subscription calls therefore need a dashboard
session or a personal access token. Personal access tokens are created by the
user in the dashboard; creation requires a browser session and a security proof,
and a PAT cannot mint another PAT.

## Step 0: Environment Bootstrap

Before touching app code, install the toolchain and prove the two sides of the
trust chain agree. Run every command in the working copy you are building.

### 0.1 One command with optional auto-install

```text
Windows:      ./scripts/check-env.ps1 -Install
macOS/Linux:  ./scripts/check-env.sh --install
```

Without `-Install` / `--install` the scripts only verify and report the exact
fix. With the flag they install missing CLIs through the platform package
manager or npm (including `supabase` and `vercel`) and then run the same checks.
`newapi-cli` is bundled with the `newapi-cli` skill and is verified, not
installed.

### 0.2 Install the CLIs manually

```bash
winget install Oven-sh.Bun GoLang.Go Supabase.CLI Cloudflare.cloudflared   # Windows
brew install oven-sh/bun/bun go supabase/tap/supabase cloudflared          # macOS
bun add -g vercel                                                          # Vercel CLI
newapi-cli --version                                                       # bundled with the newapi-cli skill
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
dashboard instead (Step 2.1); the result is the same `nac_...` client_id.

### 0.3 Log in and link Supabase

```bash
supabase login
supabase init                    # only for a new supabase/ directory
supabase link --project-ref <project-ref>
supabase db push                 # applies supabase/migrations (or schema.sql)
supabase functions deploy user-sync
```

### 0.4 Run the self-check

`scripts/check-env.ps1` (Windows) and `scripts/check-env.sh` (macOS/Linux)
verify:

1. `bun`, `go`, `supabase`, `cloudflared`, `vercel`, `newapi-cli` are installed;
2. `GATEWAY_BASE_URL`, `GATEWAY_PUBLIC_URL`, `SUPABASE_URL`,
   `SUPABASE_SECRET_KEY` are set;
3. `discovery.issuer` equals `GATEWAY_PUBLIC_URL`;
4. the Supabase provider `custom:newapi` exists and its issuer matches;
5. `GET <supabase>/auth/v1/authorize?provider=custom:newapi` returns a redirect
   (302/303/307) instead of an error.

```bash
GATEWAY_BASE_URL=http://localhost:3000 \
GATEWAY_PUBLIC_URL=https://your-public-gateway \
SUPABASE_URL=https://<project>.supabase.co \
SUPABASE_SECRET_KEY=... ./scripts/check-env.sh
```

On Windows, set the same four values with `$env:NAME = '...'` and run
`./scripts/check-env.ps1`. Any failure exits non-zero and prints the fix; do not
continue to Step 1 until the script passes.

### 0.5 Scaffold the Next.js app

```bash
bunx create-next-app@latest my-app \
  --typescript --app --tailwind --eslint --src-dir --import-alias "@/*"
cd my-app
bun add @supabase/supabase-js @supabase/ssr
bun add -D daisyui@latest
```

Enable DaisyUI in `src/app/globals.css`, then run `bun run dev` (development)
and `bun run build` (production build):

```css
@import "tailwindcss";
@plugin "daisyui";
```

## Step 1: Project Structure

Use the App Router: route handlers own server secrets; pages stay renderable on the server.

```text
my-app/
├─ src/
│  ├─ app/
│  │  ├─ layout.tsx                    # root layout, theme, providers
│  │  ├─ globals.css                   # Tailwind v4 + @plugin "daisyui";
│  │  ├─ page.tsx                      # /
│  │  ├─ login/page.tsx                # /login
│  │  ├─ auth/
│  │  │  ├─ start/route.ts             # begins OAuth (BFF mode)
│  │  │  └─ callback/route.ts          # code exchange (BFF) or page.tsx (public)
│  │  ├─ wardrobe/page.tsx             # /wardrobe
│  │  ├─ history/page.tsx              # /history
│  │  ├─ profile/page.tsx              # /profile
│  │  └─ api/relay/[...path]/route.ts  # optional BFF proxy for /v1/*
│  ├─ components/                      # DaisyUI-composed UI
│  ├─ lib/
│  │  ├─ supabase/client.ts            # browser client (anon key)
│  │  ├─ supabase/server.ts            # server client (cookies)
│  │  ├─ gatewayAuth.ts                # gateway refresh + 401 retry
│  │  └─ gateway.ts                    # AI relay calls
│  └─ types/
├─ supabase/
│  ├─ config.toml
│  ├─ migrations/<timestamp>_init.sql
│  └─ functions/user-sync/index.ts
├─ proxy.ts                          # Supabase session refresh (middleware.ts pre-16)
├─ public/
└─ .env.example · next.config.ts · package.json · tsconfig.json
```

`src/lib/supabase/client.ts` uses only the anon/publishable key and is safe in the
browser; `server.ts` reads cookies and still uses the anon key unless a route
explicitly needs elevated privileges. The `service_role` / secret key is
imported only by trusted server code or an Edge Function, never by a Client
Component and never with a `NEXT_PUBLIC_` prefix. Business data access goes
through Supabase + RLS; if you choose a BFF instead, business tables can live in
your own database, but the identity and billing chapters still apply.

The runnable reference app uses this App Router layout without the `src/`
prefix; see `references/web/outfit-demo/README.md`.

## Step 2: Identity (OAuth / OIDC)

new-api is the OIDC identity provider. Your app never stores new-api passwords.
Authorization Code + PKCE is the only supported flow.

### 2.1 Create an OAuth application

In the new-api admin dashboard, go to **OAuth Apps** and create an application:

- **Name**: Your application's display name
- **Redirect URIs**: your callback. Use `https://<project>.supabase.co/auth/v1/callback`
  for Supabase direct mode, or `https://app.example.com/auth/callback` for a BFF
- **Post-Logout Redirect URIs**: `https://your-app.com/logout-done`
- **Public client**: enable for SPAs and mini-programs (PKCE only, no secret)
- **Scopes**: `openid` (OIDC identity), `profile` (username, display name),
  `email` (email address), `api:read` (read-only AI access), `api:write` (chat,
  images, and other non-read AI endpoints). See **Scopes Reference**.

A confidential client returns `client_id` and `client_secret`; the secret is
shown **only once** — store it in backend environment variables. A **public
client** returns only `client_id`: it never has a secret, authentication relies
on PKCE, and `code_verifier` stays mandatory. Do not enable public mode for a
server-side app that can keep a secret.

### 2.2 Choose an integration mode

BFF mode keeps `client_secret` and tokens server-side and proxies AI calls;
public PKCE mode has no secret, stores tokens in Supabase Auth + app memory,
refreshes via `/oauth/token`, calls the gateway directly, and isolates data with
RLS + `client_id`. Use BFF when you have a server.

### 2.3 Confidential client in a Next.js Route Handler

Install the SDK with `bun add @new-api/oauth-client`, then create the client in
server-only code:

```typescript
// src/lib/oauth.ts
import 'server-only';
import { createOAuthClient, generatePKCEPair, generateState } from '@new-api/oauth-client';

export const oauthClient = createOAuthClient({
  baseUrl: process.env.GATEWAY_BASE_URL!,          // https://ai.example.com
  clientId: process.env.GATEWAY_CLIENT_ID!,        // nac_xxx
  clientSecret: process.env.GATEWAY_CLIENT_SECRET!,
  redirectUri: process.env.GATEWAY_REDIRECT_URI!,  // https://app.example.com/auth/callback
});

export { generatePKCEPair, generateState };
```

`GATEWAY_BASE_URL`, `GATEWAY_CLIENT_ID`, and `GATEWAY_CLIENT_SECRET` correspond
to the older `NEW_API_URL`, `NEW_API_CLIENT_ID`, and `NEW_API_CLIENT_SECRET`
examples. Use one prefix consistently.

Start the flow from a Route Handler; the login page links to `/auth/start`:

```typescript
// src/app/auth/start/route.ts
import { NextResponse } from 'next/server';
import { oauthClient, generatePKCEPair, generateState } from '@/lib/oauth';

export async function GET() {
  const { codeVerifier, codeChallenge } = generatePKCEPair();
  const state = generateState();
  const nonce = crypto.randomUUID();

  const authorizeUrl = new URL(
    oauthClient.buildAuthorizeUrl({
      scope: 'openid profile email api:read api:write',
      state,
      codeChallenge,
    }),
  );
  authorizeUrl.searchParams.set('nonce', nonce);

  const res = NextResponse.redirect(authorizeUrl);
  const cookie = { httpOnly: true, secure: process.env.NODE_ENV === 'production', sameSite: 'lax' as const, path: '/', maxAge: 600 };
  res.cookies.set('oauth_state', state, cookie);
  res.cookies.set('oauth_verifier', codeVerifier, cookie);
  res.cookies.set('oauth_nonce', nonce, cookie);
  return res;
}
```

Use `SameSite=Lax` for the state and verifier cookies. Use
`SameSite=None; Secure` only when your callback is a cross-site POST; a
top-level OIDC redirect works with `Lax`.

The callback validates `state`, exchanges the code, reads `userinfo`, and
creates your own session:

```typescript
// src/app/auth/callback/route.ts
import { NextResponse, type NextRequest } from 'next/server';
import { cookies } from 'next/headers';
import { oauthClient } from '@/lib/oauth';

export async function GET(req: NextRequest) {
  const { searchParams } = new URL(req.url);
  const code = searchParams.get('code');
  const state = searchParams.get('state');
  const cookieStore = await cookies();
  const expectedState = cookieStore.get('oauth_state')?.value;
  const codeVerifier = cookieStore.get('oauth_verifier')?.value;

  // CSRF check — compare with the HttpOnly cookie, not with a query value.
  if (!code || !state || state !== expectedState || !codeVerifier) {
    return NextResponse.redirect(new URL('/login?error=invalid_state', req.url));
  }

  // Exchange code for tokens (server-side; carries client_secret).
  const tokens = await oauthClient.exchangeCode(code, codeVerifier);

  // Link the local account by user.sub; store tokens encrypted server-side.
  const user = await oauthClient.userinfo(tokens.access_token);

  const res = NextResponse.redirect(new URL('/', req.url));
  const cookie = { httpOnly: true, secure: process.env.NODE_ENV === 'production', sameSite: 'lax' as const, path: '/' };
  res.cookies.set('session', await createSession(user, tokens), cookie);
  return res;
}
```

BFF flow:

```text
Browser ──GET /auth/start──► Next.js Route Handler
   │                            ├─ generate PKCE + state + nonce
   │                            ├─ set HttpOnly cookies
   │                            └─ 302 new-api /oauth/authorize
   ▼
new-api consent ──302 /auth/callback?code&state──► Next.js Route Handler
                                  ├─ validate state
                                  ├─ POST /oauth/token (client_secret, code_verifier)
                                  ├─ GET /oauth/userinfo
                                  └─ set your session cookie, redirect /
Browser ──POST /api/relay/...──► Route Handler ──Bearer access_token──► new-api /v1/*
```

The raw contract behind the SDK: authorization uses `response_type=code`,
`client_id`, `redirect_uri`, `scope`, `state`, `nonce`, `code_challenge`, and
`code_challenge_method=S256`; token exchange uses `grant_type=authorization_code`
with `code`, `redirect_uri`, `client_id`, and `code_verifier` (plus
`client_secret` for a confidential client); refresh uses
`grant_type=refresh_token`. Introspection is `POST /oauth/introspect`, revocation
is `POST /oauth/revoke`, and discovery is `GET /.well-known/openid-configuration`.

`state` protects the redirect against CSRF. `nonce` binds the ID token to your
authorization request and prevents replay; validate it if you consume the
`id_token` directly. Keep both values server-side.

### 2.4 Public client with PKCE (Supabase direct mode)

When your app uses Supabase as the backend (Auth, PostgREST, Storage, Edge
Functions), you can skip the BFF entirely. Supabase Auth acts as the OIDC
client, and the browser talks directly to new-api for AI calls.

```text
Browser / Mini-Program
  ├─ Supabase Auth ──► new-api OIDC Provider (login, JWT)
  ├─ Supabase PostgREST ─► PostgreSQL + RLS (business data)
  └─ new-api /v1/* ─► AI relay (direct, with OAuth token)
```

#### Supabase custom provider setup

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

- The Auth service fetches the issuer's discovery document when the provider is created, so the gateway must already be publicly reachable over HTTPS and `ServerAddress` must equal the issuer. `discovery` must list `none` in `token_endpoint_auth_methods_supported` (public client).
- `supabase config`/`config push` cannot declare custom OIDC providers, and the Management API has no provider CRUD. Do not hand-insert into `auth.custom_oauth_providers` with a bare identifier — Auth looks up `custom:<identifier>` and keeps returning `custom provider ... not found`.
- `email_optional: true` is required when the gateway user has no email. The new-api OAuth app registers `https://<project>.supabase.co/auth/v1/callback`.
- To rotate credentials, delete the old provider first (`DELETE /auth/v1/admin/custom-providers/custom:newapi`, identifier with the `custom:` prefix) and create it again; editing the new-api OAuth app from confidential to public does not update Supabase on its own.

**Map claims**: ensure `app_metadata` includes `gateway_user_id` and
`client_id`; the `user-sync` Edge Function handles this on first login.

**RLS**: every business table must have `client_id text not null` and use
`current_app_client_id()` (security definer function) to isolate rows by
OAuth app. See `references/web/outfit-demo/supabase/schema.sql`.

#### PKCE flow from a Client Component

The browser uses `@supabase/ssr` in a Client Component with the provider id
`custom:newapi`:

```typescript
'use client';

import { createBrowserClient } from '@supabase/ssr';

const supabase = createBrowserClient(process.env.NEXT_PUBLIC_SUPABASE_URL!, process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!);

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
  const res = await fetch(`${process.env.NEXT_PUBLIC_GATEWAY_BASE_URL}/oauth/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'refresh_token',
      client_id: process.env.NEXT_PUBLIC_GATEWAY_CLIENT_ID!, // no client_secret for a public client
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
id), never in `user_metadata`. `references/web/outfit-demo/lib/gatewayAuth.ts`
is the reference implementation and:

| Behavior | Detail |
|----------|--------|
| Expiry check | Parses `exp` from the access token JWT; refreshes when less than 60 s remain |
| Concurrency | Deduplicates concurrent refreshes with one in-flight promise |
| Relay `401` | Retries exactly once, then surfaces the error |
| `invalid_grant` | Clears local state and signs the user out |

### 2.5 Starter template and reference app

`references/web/outfit-demo/` is a runnable Next.js App Router app built on the
starter. It includes a pre-configured Supabase client with OIDC login,
`lib/gatewayAuth.ts` for token refresh, `lib/gateway.ts` for AI relay
calls (streaming + non-streaming, 401 retry), `supabase/schema.sql` with RLS,
`current_app_client_id()`, and seed data, plus the `user-sync` Edge Function.

| Route | Shows |
|-------|-------|
| `/login` | "Sign in with new-api" entry point |
| `/auth/callback` | Completes the Supabase session after the gateway redirect |
| `/` | AI outfit generation: business-table multi-select + streaming AI call + write-back to `ai_generations` |
| `/wardrobe` | PostgREST read with category filter, RLS isolation by `client_id` + `auth.uid()` |
| `/history` | Previous `ai_generations` records |
| `/profile` | Gateway user, quota, and account entry points |

Copy it, fill `.env.local`, and run:

```bash
cp -r references/web/outfit-demo my-app
cd my-app
cp .env.example .env.local   # Supabase values are pre-filled; set NEXT_PUBLIC_GATEWAY_*
bun install
bun run dev
bun run build                # verify the production build before deploying
```

The bare scaffold is `templates/supabase-app-starter/`; the demo's Supabase
project already has the schema and seed data applied, so the wardrobe page
renders 8 official outfits after login.

#### Local verification notes

| Symptom | Cause and fix |
|---------|---------------|
| Login bounces back to `/sign-in` after a restart | `SESSION_SECRET` is unset, so new-api generates a random secret per process and invalidates refresh cookies. Set it in `.env` once. |
| `/oauth/authorize` requires authentication | It authenticates a browser top-level navigation from the refresh cookie (`Path=/`, HttpOnly) because the request carries no `Authorization` header. Keep the gateway and frontend on the same host so that cookie is sent. |
| Supabase cloud project cannot finish the flow against `http://localhost` | Its server exchanges the code at the issuer URL and rejects a non-public issuer. Expose the gateway over HTTPS (tunnel or deployed host), put that origin in new-api's server address setting, and only then register the provider. Locally you can verify up to the consent redirect (`302 /oauth/consent`). |

### 2.6 Handle logout

When the user logs out, revoke the refresh token and clear your session. In BFF
mode, call `oauthClient.revoke(session.refreshToken)` in a `POST` Route Handler
and redirect to `APP_ORIGIN`. In public PKCE mode, revoke
`provider_refresh_token` through `oauthClient` or a direct `POST /oauth/revoke`,
then call `supabase.auth.signOut()`.

## Step 3: AI Relay Calls

All AI traffic goes to `/v1/*` on the gateway with an OAuth access token. An
OAuth access token is **not** a dashboard credential: do not send it to
`/api/*`.

### 3.1 Proxy through a Next.js Route Handler

A BFF proxy keeps the token on the server and centralizes retry, logging, and
rate limiting:

```typescript
// src/app/api/relay/[...path]/route.ts
import { type NextRequest } from 'next/server';
import { getGatewayAccessToken } from '@/lib/gatewayAuth';

async function relay(req: NextRequest, path: string[]) {
  const token = await getGatewayAccessToken(); // refreshes when < 60 s remain

  const upstream = await fetch(
    `${process.env.GATEWAY_BASE_URL}/v1/${path.join('/')}`,
    {
      method: req.method,
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': req.headers.get('content-type') ?? 'application/json',
      },
      body: req.method === 'GET' || req.method === 'HEAD' ? undefined : req.body,
      // @ts-expect-error Node streams need half-duplex
      duplex: 'half',
    },
  );

  return new Response(upstream.body, {
    status: upstream.status,
    headers: {
      'Content-Type': upstream.headers.get('content-type') ?? 'application/json',
    },
  });
}

export async function POST(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return relay(req, (await ctx.params).path);
}

// Export the same function as GET when you also proxy model lists.
```

| Rule | Reason |
|------|--------|
| Never log the `Authorization` header, token, request body, or upstream body | Logs are copied into issue trackers and dashboards |
| Log method, path, status, latency, and a request id only | Enough to debug without leaking credentials |
| Never forward cookies, `client_secret`, or dashboard PATs upstream | The gateway must receive only the OAuth token it issued |
| Preserve upstream status and content type | Streaming clients can render errors instead of a broken stream |
| Retry a `401` exactly once after refresh, then surface it | Prevents loops and repeated billing |

### 3.2 Direct call from a public client

In Supabase direct mode the browser calls the gateway itself:

```typescript
// Direct AI call (no BFF):
const res = await fetch('https://your-gateway.com/v1/chat/completions', {
  method: 'POST',
  headers: { Authorization: `Bearer ${await getGatewayAccessToken()}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [...] }),
});
```

`getGatewayAccessToken()` refreshes before expiry, keeps the rotated refresh token, and clears local state on `invalid_grant`; in Next.js it lives in `src/lib/gatewayAuth.ts` and is imported only by Client Components.

## Step 4: Users, Quota, and Payments

The user system lives under `/api/*`, which OAuth access tokens cannot reach.
Use a dashboard session or a personal access token for everything in this
section.

### 4.1 Identity payload

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

This payload is the entire identity an admin console can read from OAuth. The
`id_token` adds only `sub`, `auth_time`, and `nonce`; it carries no name, email,
or role. Admin permissions therefore come from your own store
(`app_metadata.admin_role` / `app_admins`), not from new-api — see **Step 8**.

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

### 4.2 Reading quota and usage

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

### 4.3 Quota units

`quota` and `used_quota` are integer quota units, not money.

`QuotaPerUnit` defaults to `500000` (1 USD = 500,000 quota units), and displayed
money is `quota / QuotaPerUnit`, converted with the configured exchange rate
when the display currency is CNY. Payment `amount` fields are in the configured
display type (money amount or token count), **not** quota units — never send a
`quota` value as a payment `amount`. `quota` is the wallet balance only; an
active subscription can still cover relay usage when the wallet is empty, so
read `GET /api/subscription/self` to judge the full billing state.

### 4.4 Personal access tokens (PAT)

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

| Rule | Detail |
|------|--------|
| Format | The raw token starts with `nap_`, is shown once, and is sent as `Authorization: Bearer nap_...`. |
| Expiry | `expires_at` is a Unix timestamp; `0` means no expiry, and a non-zero value must be at least one hour in the future. |
| Creation | Requires a browser session and a security proof; a PAT cannot create another PAT. |
| Scopes | Each dashboard route declares its required scope: `profile:read`, `profile:write`, `account_security:write`, `wallet:read`, `wallet:write`, `billing:read`, `billing:write`, `usage:read`, and the admin scope `user:write`. `GET /api/user/access_tokens/scopes` lists the full catalog and also requires a browser session. |
| Limits | A PAT never exceeds the user's role, so admin routes still require an admin or root account. PAT requests have no browser `SessionID`, so they cannot list or revoke dashboard sessions. |

### 4.5 Session management

When your own frontend embeds the new-api dashboard, these endpoints manage
browser sessions:

- `GET /api/user/sessions` — list active sessions
- `DELETE /api/user/sessions/:sid` — revoke one session
- `POST /api/user/sessions/revoke-others` — revoke every other session

They reject PATs, so they require an actual browser session.

### 4.6 Payment system

Payment endpoints live under `/api/user/*` and `/api/subscription/*`, so they
require a dashboard session or a PAT. A practical PAT scope set is
`wallet:read` + `wallet:write`.

Payment endpoints answer with `{"message": "success" | "error", "data": ...}`
over HTTP 200 rather than the usual `{"success": ...}` envelope. Redemption
(`POST /api/user/topup`) is the exception and uses the standard envelope.

#### Compliance gate

Top-up, redemption, subscription purchase, and invitation rewards stay disabled
until an administrator confirms the payment compliance terms. Check first:

```http
GET /api/user/topup/info
Authorization: Bearer nap_...
```

Use `payment_compliance_confirmed` and `payment_compliance_terms_version` from
the response. While it is `false`, payment calls return errors and
`GET /api/subscription/plans` returns an empty list.

#### Available channels

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

#### Top-up order APIs

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

#### Order status and crediting

| Rule | Detail |
|------|--------|
| Crediting | Quota is credited only after the provider confirms payment (epay notify, or Stripe / Creem / Waffo webhooks); a browser return URL is not proof. |
| Polling | `GET /api/user/topup/self` (scope `wallet:read`) returns `trade_no`, `amount`, `money`, `payment_method`, `payment_provider`, `status`, `create_time`, `complete_time`. |
| Status | `pending`, `success`, `failed`, `expired`; only `success` means quota was credited. |
| Idempotency | Repeated provider notifications do not credit twice. |
| Missing payment | An administrator can run `POST /api/user/topup/complete` `{"trade_no":"..."}` (admin session, or admin PAT with `billing:write`). |

#### Subscriptions

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

#### Administration

Admin session, or admin PAT with `billing:read` / `billing:write`:

- `GET /api/user/` and `GET /api/user/topup` — user list and all top-up orders
- Plan CRUD: `GET|POST /api/subscription/admin/plans`,
  `PUT|PATCH /api/subscription/admin/plans/:id`
- Grant: `POST /api/subscription/admin/bind` `{"user_id":1,"plan_id":1}` — a
  plan without payment
- User subscriptions: `GET|POST /api/subscription/admin/users/:id/subscriptions`,
  `POST /api/subscription/admin/users/:id/subscriptions/reset`,
  `POST /api/subscription/admin/user_subscriptions/:id/invalidate`, and
  `DELETE /api/subscription/admin/user_subscriptions/:id`

### 4.7 Credits, top-up, and orders are new-api data

Do not build a parallel wallet. In this standard, the app's "credits" are new-api
`quota` (wallet) plus subscription quota, and top-up orders and consumption
records are new-api records. The UI may convert quota units to credits for
display, but there is exactly one balance.

| App object | Source of truth | API |
|------------|-----------------|-----|
| Balance / credits | `quota`, `used_quota` | `GET /api/user/self` (scope `profile:read`) |
| Subscription quota | active subscriptions | `GET /api/subscription/self` (scope `wallet:read`) |
| Top-up orders | payment records | `GET /api/user/topup/self` (scope `wallet:read`) |
| Consumption / usage | request logs | `GET /api/log/self`, `/self/stat`, `/self/search` (scope `usage:read`) |
| Channels and compliance | payment config | `GET /api/user/topup/info` |

Choose the access mode that matches the identity flow:

| Mode | Wallet / orders readable | How |
|------|--------------------------|-----|
| Dashboard session (embedded dashboard or BFF login) | Yes | Browser session cookie on the gateway origin, or a same-site BFF |
| PAT (`profile:read`, `wallet:read`, `billing:read`, `usage:read`) | Yes, for that user | User creates it in the new-api dashboard |
| OAuth access token only (Supabase direct mode) | **No** | `/api/*` rejects OAuth tokens; redirect to the gateway dashboard for top-up and orders |

Rules:

- OAuth-only apps must send the user to the new-api dashboard for top-up
  (`<GATEWAY_PUBLIC_URL>/app.html#/topup`), order history, and subscription
  purchase. Do not reimplement payment, and do not mint a per-user PAT from an
  OAuth token — that requires a dashboard session and a security proof.
- A service-account PAT cannot read another user's wallet: every `/self`
  endpoint returns the token owner's data. Cross-user reads require admin
  endpoints (`GET /api/user/` with `user:read`, `GET /api/user/topup` with
  `billing:read`) and belong in the admin console, never in the consumer app to
  fabricate per-user balances.
- No second ledger: no local `credits`, `wallet`, or `orders` table as the
  authoritative balance. A local mirror keyed by `trade_no` is cache only,
  reconciled from `/api/user/topup/self`; a return URL never marks an order paid.
- Relay calls are billed by new-api automatically. Do not deduct credits again in
  application code after a `/v1/*` call.
- App-level paid actions (unlock a feature, buy a template) are either priced as
  relay usage, or performed by your backend as an audited quota adjustment:
  `POST /api/user/manage` `{"id":<sub>,"action":"add_quota","mode":"subtract","value":<quota>}`
  with an admin PAT (`user:write`). Keep an idempotency key and a business record,
  never expose that token or endpoint to the browser, and refuse to go negative.
- Define the quota-to-credit conversion once, in one module, and format it with
  the project's number/currency helpers.

## Step 5: Backend on Supabase

Detailed conventions live in `references/standards/supabase.md`. These
integration rules are mandatory for any app that stores new-api users or
business data in Supabase.

### 5.1 Client boundaries

| Client | Key | Rule |
|--------|-----|------|
| Browser | anon / publishable | Relies on RLS for authorization |
| Server | `@supabase/ssr` + cookies | Same session visible in Server Components and Route Handlers |
| Session refresh | `proxy.ts` (`middleware.ts` before Next.js 16) | Calls `getUser()` before requests to keep the cookie session fresh and protect routes |
| Trusted server / Edge Function | `service_role` / secret | Bypasses RLS; never expose it and never prefix it with `NEXT_PUBLIC_` |

Do not write an authorization check that only exists in a Client Component or a
PostgREST query filter. RLS is the enforcement point.

### 5.2 RLS and `client_id` isolation

Every business table carries the OAuth app identity:

```sql
alter table public.outfits
  add column client_id text not null;

create or replace function public.current_app_client_id()
returns text
language sql
stable
security definer
set search_path = public
as $$
  select coalesce((auth.jwt() -> 'app_metadata' ->> 'client_id'), '');
$$;

alter table public.outfits enable row level security;

create policy "outfits_owner_app" on public.outfits
for all to authenticated
using (client_id = public.current_app_client_id() and user_id = auth.uid())
with check (client_id = public.current_app_client_id() and user_id = auth.uid());
```

`client_id` must be `not null`; a missing value silently widens or breaks
isolation. `current_app_client_id()` is a `security definer` function so policies
can read `app_metadata` without granting extra table access. Never accept
`client_id` from the request body; derive it from the JWT. The full schema, seed
data, and policy set are in `references/web/outfit-demo/supabase/schema.sql`.

### 5.3 Edge Function authentication

Verify the caller with `supabase.auth.getUser()` using the incoming
`Authorization: Bearer <Supabase JWT>` and reject anonymous or expired sessions.
Derive `gateway_user_id` and `client_id` from `app_metadata`, never from the
request payload. Use the service-role key only inside the function environment,
and only for the narrow operation the function owns. Return a job id for work
that can exceed the function limits (see **Long-Running Work and Supabase
Limits**). The `user-sync` function maps `gateway_user_id` and `client_id` into
`app_metadata` on first login.

### 5.4 All media assets go to Supabase Storage

Mandatory: every avatar, image, generated image, video, audio file, PDF,
attachment, and export is stored in **Supabase Storage**. The database keeps only
`bucket` + `path` (`{client_id}/{user_id}/{uuid}.{ext}`); reads use short-lived
signed URLs from a private bucket, and `storage.objects` RLS enforces `client_id`
and owner. Validate MIME type and size server-side before upload.

Do not store base64 or binary blobs in Postgres, write uploads to the Vercel/Next.js
filesystem, or use a third-party image host/CDN for user content. Schema, bucket
creation, policies, and the full checklist are in
`references/standards/supabase.md` (section 5).

## Step 6: Frontend Standards

Detailed conventions live in `references/standards/nextjs.md`,
`references/standards/daisyui.md`, and `references/standards/ui-ux.md`.

### 6.1 Server and Client boundaries

Server Components are the default: fetch data, read cookies, and call
server-only APIs there. Add `'use client'` only to the smallest interactive
leaf (forms, toggles, streaming readers, browser APIs). Never pass an access
token, refresh token, `client_secret`, or service-role key into a Client
Component; use Route Handlers for OAuth callbacks, token refresh, and any call
that needs a secret. Keep streaming responses unbuffered by returning
`upstream.body` instead of `await upstream.text()`.

### 6.2 Components with DaisyUI

Reuse an existing component before writing markup; check `src/components/` and
the demo pages first. Prefer DaisyUI classes and components (`btn`, `card`,
`navbar`, `drawer`, `modal`, `table`, `tabs`, `alert`, `loading`, `skeleton`)
over ad-hoc CSS. Configure the theme once with `data-theme` on `<html>` and keep
it consistent. Keep business state out of presentational components and pass
typed props. See `references/standards/daisyui.md`.

### 6.3 UI/UX

Every data view needs loading, empty, and error states; a spinner alone is not
an empty state. Destructive actions require confirmation, and payment or logout
actions must show the resulting state, not just close a dialog. Validate forms
on the client for feedback and on the server for correctness. Keep keyboard
navigation, focus rings, labels, and contrast intact. See
`references/standards/ui-ux.md` and `references/vendor/ui-ux-pro-max/`.

### 6.4 Next.js guidance

Follow `references/standards/nextjs.md` for routing, data fetching, caching, and
streaming. Use `references/vendor/vercel-react-best-practices/` for React and
Next.js performance work, and
`references/vendor/nextjs-react-typescript/SKILL.md` for role and architecture
guidance when starting a new application.

## Step 7: Deploy with Vercel

Detailed deployment conventions live in `references/standards/vercel.md`. The
workflow below is the minimum for an app built from this standard.

### 7.1 Link and configure

Add every variable from the table below for production, then deploy:

```bash
vercel login
vercel link
vercel env add NEXT_PUBLIC_SUPABASE_URL production
vercel env add SUPABASE_SECRET_KEY production
vercel --prod
```

| Variable | Exposure | Purpose |
|----------|----------|---------|
| `NEXT_PUBLIC_SUPABASE_URL` | Browser | Supabase project URL |
| `NEXT_PUBLIC_SUPABASE_ANON_KEY` | Browser | Supabase anon/publishable key, protected by RLS |
| `SUPABASE_SECRET_KEY` | Server only | Admin API, Edge Function secrets, migrations |
| `GATEWAY_BASE_URL` | Server; `NEXT_PUBLIC_GATEWAY_BASE_URL` in public mode | Gateway origin used by Route Handlers or the browser |
| `GATEWAY_PUBLIC_URL` | Server | Public issuer; must equal the Supabase provider issuer |
| `GATEWAY_CLIENT_ID` | Browser or server; `NEXT_PUBLIC_GATEWAY_CLIENT_ID` in public mode | `nac_...`, safe to expose |
| `GATEWAY_CLIENT_SECRET` | Server only | Confidential client secret, never `NEXT_PUBLIC_` |
| `APP_ORIGIN` | Server | Stable production origin for redirects |
| `INITIAL_ADMIN_SUB` | Server (optional) | Developer's `sub` (`oauth_apps.created_by`) for the one-time first-owner grant |

### 7.2 Sync the three origins after deployment

A deployment changes the public origin, so update all three places in the same
change window:

1. **Vercel**: the production domain (and `APP_ORIGIN`) that serves the app.
2. **new-api OAuth app**: for BFF mode, add
   `https://app.example.com/auth/callback` to **Redirect URIs** and the logout
   URL to **Post-Logout Redirect URIs**. For Supabase direct mode, the redirect
   URI stays `https://<project>.supabase.co/auth/v1/callback`.
3. **Supabase Auth**: set the Site URL to the Vercel production origin and add
   `https://app.example.com/auth/callback` to the redirect allow list. In direct
   mode, the custom provider issuer must remain the gateway's public HTTPS URL.

Vercel preview deployments get a new origin per deployment, so do not register a
wildcard redirect URI: either keep OAuth disabled on previews or use a stable
custom domain. Changing the Supabase project domain changes the callback URL;
recreate the `custom:newapi` provider after such a change because editing it in
place is not supported. For local acceptance testing, expose the gateway and app
through a Cloudflare quick tunnel
(`cloudflared tunnel --url http://localhost:<port>`), update the same three
places for the session, and remove the tunnel hostname afterwards. Never treat a
preview or tunnel URL as a permanent deployment address.

## Step 8: Admin Dashboard

An admin console is an optional surface with a different stack from the consumer
app: **Next.js App Router + Ant Design 5 + `@refinedev/core` + Supabase**. Full
conventions are in `references/standards/admin-dashboard.md`; the rules below are
the contract.

### 8.1 Identity available to the console

| Source | Fields |
|--------|--------|
| `GET /oauth/userinfo` | `sub` always; `username`, `name` with `profile`; `email` with `email` (may be empty) |
| `id_token` | `sub`, `auth_time`, `nonce`, `iss`, `aud`, `exp`, `nbf`, `iat`, `jti` — **no** name, email, or role |
| Supabase session | provider `sub` inside `user.identities[].identity_data` |
| `/api/user/self` with a dashboard session or PAT | `id`, `username`, `display_name`, `role`, `status`, `email`, `group`, `quota`, `used_quota`, `permissions` |

Key the admin account on `sub`. The **app's** admin role is not the new-api role
(`1` user / `10` admin / `100` root): store it in
`auth.users.app_metadata.admin_role` (service-role writes only) or an
`app_admins` table. Never read a role from `user_metadata`, a query string, or a
request body.

### 8.2 Keep the two styling systems apart

Admin uses Ant Design; the consumer app uses Tailwind + DaisyUI. Render them in
separate route trees (`app/(admin)/` with its own root layout) or separate
deployments — never in one layout, where Tailwind preflight and antd reset fight
over base elements. Do not use `!important` to patch the conflict.

### 8.3 Supabase server and client split

| Client | Key | Use |
|--------|-----|-----|
| `lib/supabase/client.ts` | anon/publishable | Client Components |
| `lib/supabase/server.ts` | anon/publishable + cookies | Server Components and user-scoped Route Handlers |
| `lib/supabase/admin.ts` | `SUPABASE_SECRET_KEY` | privileged Route Handler work only; starts with `import 'server-only';` |

The privileged client bypasses RLS, so every query still filters by the intended
`client_id`. Keep RLS enabled everywhere; if the browser client also reads admin
data, add a `security definer` helper such as `public.is_app_admin()` that reads
`auth.users.app_metadata`.

### 8.4 Guard and data boundary

Every admin Route Handler and the admin layout call the same `requireAdmin()`
helper: `supabase.auth.getUser()`, then check `app_metadata.admin_role`. Client
checks may hide controls but are not the enforcement point; `proxy.ts` /
middleware is not an authorization boundary either.

Refine's `dataProvider` targets `/api/admin/**` only — pass
`manualPagination`, `manualSorting`, `manualFiltering` so the server paginates
and whitelists sort/filter fields. `accessControlProvider` mirrors the server
guard; it never replaces it. Do not let Refine query privileged tables from the
browser.

### 8.5 Ant Design in the App Router

Mount `AntdRegistry` and `ConfigProvider` once in the admin root layout:

```tsx
<AntdRegistry>
  <ConfigProvider locale={zhCN} theme={{ token: { borderRadius: 8 } }}>
    {children}
  </ConfigProvider>
</AntdRegistry>
```

Tables use a stable `rowKey` and explicit `columns`; forms use antd `Form` for
layout plus the same Zod schema on the client and in the Route Handler. Write an
audit row (`actor_id`, `actor_role`, `action`, `resource`, `resource_id`, `ip`,
`user_agent`) for create/update/delete and privileged reads, and never store
tokens or passwords in it.

### 8.6 Bootstrap the first administrator

The first admin cannot come from the UI, an API route, or "first signup wins".
**By default it is the developer who created the OAuth app**: new-api records
that user id in `oauth_apps.created_by`, which equals their login `sub`. Set
`INITIAL_ADMIN_SUB` from `GET /api/oauth-apps/<id>` (admin session) and seed
`app_admins` in the first migration — or allow a one-time, idempotent first-run
grant when no owner exists and the verified `sub` matches that fixed value.
After the person completes one OAuth login:

1. Insert into `app_admins` (preferred) or update
   `auth.users.raw_app_meta_data` with `admin_role = 'owner'`, keyed on the
   provider `sub` in `auth.identities` — not on username or email.
2. Run it with `supabase db query "<sql>"` against the linked project.
3. Have that user sign out/in (or `refreshSession()`), then confirm
   `/api/admin/me` returns `200` for them and `403` for a normal user.
4. Record who granted it and why.

Later grants run through an owner-only admin Route Handler that writes
`app_metadata`/`app_admins` with the privileged client; block self-promotion and
removing the last owner. Full SQL and rules are in
`references/standards/admin-dashboard.md` (section 3.1).

## Security Checklist

- [ ] Confidential clients keep `client_secret` only in backend environment variables; public clients have no secret and rely on PKCE.
- [ ] Public-client mode is enabled only for apps that cannot keep a secret (SPA, mini-program); server-side apps stay confidential.
- [ ] `redirect_uri` exactly matches a registered URI (BFF callback or `https://<project>.supabase.co/auth/v1/callback`).
- [ ] PKCE `code_verifier` is matched on callback; a public client without it is rejected.
- [ ] `state` parameter is validated to prevent CSRF.
- [ ] OIDC `nonce` is generated per authorization request and validated when an `id_token` is consumed.
- [ ] State, verifier, and session cookies use `HttpOnly`, `Secure` in production, and `SameSite=Lax` (or `SameSite=None; Secure` only for a required cross-site callback).
- [ ] Gateway tokens are kept in memory (optionally `sessionStorage`) and never written to `user_metadata` or logged.
- [ ] Refreshed refresh tokens replace the old value; `invalid_grant` ends the session instead of retrying.
- [ ] HTTPS is enabled in production (required for `SameSite=None` cookies).
- [ ] OAuth tokens are used only for `/v1/*` and `/oauth/userinfo`; quota and payment calls use a dashboard session or a scoped PAT.
- [ ] Personal access tokens are stored encrypted and carry only the scopes the integration actually needs.
- [ ] Your system never treats a browser return URL or a `pending` order as proof of payment; quota is credited only after new-api confirms the provider payment.
- [ ] Admin console permissions come from `app_metadata`/`app_admins`, and every admin Route Handler calls the server guard; the browser role is UX only.
- [ ] `SUPABASE_SECRET_KEY` is imported only by a `server-only` module and never exposed through `NEXT_PUBLIC_*`.
- [ ] Admin (Ant Design) and consumer (Tailwind + DaisyUI) UIs never share a layout or styling system.
- [ ] The first admin defaults to the OAuth app developer (`oauth_apps.created_by` → `INITIAL_ADMIN_SUB`) and is granted once out of band; no endpoint or first-signup rule can create an admin.
- [ ] All media assets are stored in Supabase Storage private buckets; only `bucket` + `path` is stored in Postgres, and reads use short-lived signed URLs.
- [ ] Balance, credits, top-up orders, and usage come from new-api (`/api/user/self`, `/api/user/topup/self`, `/api/log/self`); there is no local ledger, and OAuth-only apps redirect to the dashboard instead of reimplementing payment.

## Token Lifecycle

- **Access token**: JWT, 1 hour TTL, used to call AI APIs and userinfo.
- **Refresh token**: Opaque, 30 day TTL, rotated on each use.
- **Authorization code**: Single-use, 60 second TTL.

### The provider token is not refreshed for you

Supabase refreshes its own session, but it does **not** refresh a custom
provider's `provider_token`. After one hour every direct AI call would fail with
`401`. The application must refresh it:

1. on login, read `session.provider_token` and `session.provider_refresh_token`;
2. before each gateway call, check the access token's `exp` and refresh when less than 60 s remain with `POST <gateway>/oauth/token` (`grant_type=refresh_token`, `client_id`, `refresh_token`; public client: no `client_secret`);
3. replace the stored refresh token with the rotated value; on `invalid_grant`, clear local state and send the user through login again.

Refresh is also the recovery path for a relay `401`: retry once with a fresh
token, never loop. In BFF mode, `oauthClient.refreshToken(session.refreshToken)`
returns the rotated pair; store it in place of the old tokens.
`references/web/outfit-demo/lib/gatewayAuth.ts` is the reference implementation.

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

- A gateway AI stream longer than the function limit must be consumed by the
  client or a worker, not buffered inside an Edge Function.

| Work size | Pattern |
|-----------|---------|
| Seconds | Call the gateway or PostgREST directly from the browser; keep token refresh in the request path |
| Tens of seconds | Run an Edge Function under both the wall clock and CPU limits; stream or return a job id |
| Minutes+ | Insert a job row, return its id, run the work outside the request, and let the client poll or subscribe |

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

## Model Whitelist per App

Each OAuth App in new-api can restrict which AI models are available:

```sql
-- In new-api admin, set on the OAuth App:
ai_model_whitelist = '["gpt-4o-mini","gpt-4o"]'
```

When set, the relay rejects any model not in the list. Leave empty to allow
all models. This is enforced in `controller/relay.go` via
`service.CheckOAuthAppModelWhitelist()`.
