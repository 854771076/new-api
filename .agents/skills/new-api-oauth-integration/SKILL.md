---
name: new-api-oauth-integration
description: Standard for building an external Web app against new-api — Next.js App Router, Supabase, Vercel, Tailwind CSS + DaisyUI, plus new-api OAuth 2.0 / OIDC identity, AI relay calls, quota / top-up / subscription billing integration, and an optional admin console (Ant Design + Refine).
---

# new-api External App Development Standard (Web)

This skill is the standard for building an external **Web** application that uses new-api as its identity provider and AI gateway. It applies to SaaS products, enterprise internal tools, and demos that must run the complete chain: stack and project structure, frontend standards, the Supabase backend, Vercel deployment, and the new-api contracts for identity, AI relay, users, quota, top-up, and subscriptions. The identity and billing chapters are new-api product contracts and stay valid when you deviate from the default stack; the stack chapters are defaults, not requirements.

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

The consumer app uses Tailwind + DaisyUI. An admin console is the one deliberate exception: it uses Ant Design and must not share a layout with the consumer app.

You may deviate when an existing platform standard, an offline/on-premise requirement, a non-Web target, or a regulatory constraint makes the default stack impossible. When you deviate, only **Step 2 (Identity)** and **Step 4 (Users, Quota, and Payments)** remain normative; keep **Security Checklist**, **Token Lifecycle**, **Scopes Reference**, and **Model Whitelist per App**, and preserve the same security properties: server-side secret storage, PKCE, state validation, token rotation, and no token in logs.

## Reference Index

This file is the contract and index; read the matching standard before implementing that layer.

| Topic | File | Read when |
|-------|------|-----------|
| Frontend framework, rendering, caching, performance | `references/standards/nextjs.md` | Writing routes, data fetching, or streaming |
| React/Next.js role and architecture guidance (vendor) | `references/vendor/nextjs-react-typescript/SKILL.md` | Starting a new application |
| React performance rules (vendor) | `references/vendor/vercel-react-best-practices/` | Optimizing components or bundles |
| UI components | `references/standards/daisyui.md` | Building any screen |
| UI/UX principles | `references/standards/ui-ux.md` | Designing states, forms, accessibility |
| UI/UX and design skills (vendor) | `references/vendor/ui-ux-pro-max/` | Needing deeper design guidance |
| Supabase: CLI, Auth, RLS, Edge Functions, Storage, runtime limits | `references/standards/supabase.md` | Touching schema, policies, functions, or media |
| API contract: pagination, response envelope, error codes, i18n keys | `references/standards/api-contract.md` | Writing any Route Handler |
| Deployment on Vercel | `references/standards/vercel.md` | Linking, env vars, promoting, rollback |
| Admin console (Next.js + Ant Design + Refine + Supabase) | `references/standards/admin-dashboard.md` | Building the optional back office |
| Identity: app creation, both client modes, `custom:newapi`, starter, logout | `references/standards/identity.md` | Implementing login (Step 2) |
| AI relay: BFF proxy code, rules, direct calls | `references/standards/relay.md` | Calling `/v1/*` (Step 3) |
| Quota, `userinfo`, PAT, sessions, payments, credits | `references/standards/billing.md` | Reading balance or taking payment (Step 4) |
| Security checklist and token lifecycle, in full | `references/standards/security.md` | Reviewing a flow or handling tokens |
| Local acceptance: Cloudflare Tunnel and Supabase URL config | `references/standards/local-acceptance.md` | Testing the flow locally |
| Runnable demo app | `references/web/outfit-demo/README.md` | Wanting a working reference |
| Vendor source provenance | `references/vendor/README.md` | Auditing vendored material |
| Environment self-check | `scripts/check-env.ps1`, `scripts/check-env.sh` | Step 0.1 / 0.4 |
| One-shot bootstrap | `scripts/bootstrap.ps1`, `scripts/bootstrap.sh` | Step 0.3 |

## Prerequisites

1. A new-api instance running and accessible over HTTPS.
2. Admin access to the new-api dashboard to create an OAuth application.
3. One of:
   - a backend (BFF) that can store `client_secret` (confidential client), or
   - a browser SPA / mini-program that uses PKCE with a **public client** (`public_client = true`, no `client_secret`). Supabase direct mode uses this option.

## Credential Surfaces

Each new-api surface accepts a different credential. Sending the wrong one is the most common integration failure.

| Surface | Endpoints | Accepted credential |
|---------|-----------|---------------------|
| OAuth / OIDC | `/oauth/authorize`, `/oauth/token`, `/oauth/userinfo`, `/oauth/introspect`, `/oauth/revoke`, `/oauth/topup/checkout`, `/oauth/logs`, `/.well-known/openid-configuration` | OAuth access token / client credentials |
| AI relay | `/v1/*` | OAuth access token (`api:read` for GET/HEAD, `api:write` for other methods), or a new-api API token (`sk-...`) |
| Dashboard / user system | `/api/user/*`, `/api/user/topup*`, `/api/user/pay`, `/api/subscription/*` | Browser session or personal access token (`nap_...`) |
| Administration | `/api/user/` admin routes, `/api/subscription/admin/*` | Admin or root session, or admin personal access token |

An OAuth access token is accepted by the AI relay and `/oauth/*` endpoints above; every `/api/*` dashboard endpoint rejects it. Quota, usage, top-up, and subscription calls therefore no longer need a dashboard session or a PAT — the three new `/oauth/userinfo` (with `api:read`), `/oauth/topup/checkout` (with `api:write`), and `/oauth/logs` (with `api:read`) endpoints handle them directly. Personal access tokens (`nap_...`) remain available for trusted backend integrations that need full dashboard scope.

## Step 0: Environment Bootstrap

Install the toolchain and prove both sides of the trust chain agree. Run every command in the working copy you are building.

### 0.1 One command with optional auto-install

`./scripts/check-env.ps1 -Install` (Windows) / `./scripts/check-env.sh --install` (macOS/Linux). Without the flag they only verify and print the exact fix; with it they install missing CLIs through the platform package manager or npm (including `supabase` and `vercel`) and re-run the checks. `newapi-cli` is bundled with the `newapi-cli` skill and is verified, not installed.

### 0.2 Install the CLIs manually

```text
Windows:      winget install Oven-sh.Bun GoLang.Go Supabase.CLI Cloudflare.cloudflared
macOS/Linux:  brew install oven-sh/bun/bun go supabase/tap/supabase cloudflared
Both:         bun add -g vercel   ·   newapi-cli --version   # verified, not installed
```

### 0.3 One-shot bootstrap

```text
Windows:      Copy-Item "<skill-root>/scripts/bootstrap.ps1" .\scripts\bootstrap.ps1; .\scripts\bootstrap.ps1 -ProjectName "My App" [-ClientIdOverride <id>] [-PackRoot <dir>]
macOS/Linux:  cp "<skill-root>/scripts/bootstrap.sh" scripts/bootstrap.sh; bash scripts/bootstrap.sh --project-name "My App" [--client-id <id>] [--pack-root <dir>]
```

Both scripts are project-agnostic (no hardcoded paths) and need `newapi-cli` logged in, the `supabase` CLI, and `bun` only when a pack root is given. They run eight steps: probe login, self user, and OAuth apps (reusing an existing public client) → `supabase link` via that app's project ref → read anon + service_role keys → write `.env.local` → `supabase db push` → check or rebuild `custom:newapi` (DELETE + POST on issuer mismatch) → authorize smoke test (302 = OK) → resolve the first admin's `auth.users` UUID, insert `app_admins(role='owner')`, and write `INITIAL_ADMIN_SUB` back. bootstrap 自动把 scopes 设为 `openid profile email api:read api:write`，Supabase provider scopes 同步更新——这保证登录后你既拿到身份也拿到额度/充值/日志能力。

Manual equivalent: `supabase login`, `supabase init` (new `supabase/` only), `supabase link --project-ref <project-ref>`, `supabase db push`, `supabase functions deploy user-sync`.

### 0.4 Run the self-check

Both scripts verify the six CLIs exist, the four env vars below are set, `discovery.issuer` equals `GATEWAY_PUBLIC_URL`, `custom:newapi` exists with a matching issuer, and `GET <supabase>/auth/v1/authorize?provider=custom:newapi` returns 302/303/307. Any failure exits non-zero with the fix; do not continue to Step 1 until it passes.

```text
macOS/Linux:  GATEWAY_BASE_URL=... GATEWAY_PUBLIC_URL=... SUPABASE_URL=... SUPABASE_SECRET_KEY=... ./scripts/check-env.sh
Windows:      $env:GATEWAY_BASE_URL='...'; $env:GATEWAY_PUBLIC_URL='...'; $env:SUPABASE_URL='...'; $env:SUPABASE_SECRET_KEY='...'; ./scripts/check-env.ps1
```

### 0.5 Scaffold the Next.js app

`bunx create-next-app@latest my-app --typescript --app --tailwind --eslint --src-dir --import-alias "@/*"`, then `cd my-app && bun add @supabase/supabase-js @supabase/ssr && bun add -D daisyui@latest`. Enable DaisyUI in `src/app/globals.css` (`@import "tailwindcss";` + `@plugin "daisyui";`), then run `bun run dev` and `bun run build`.

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
│  └─ lib/
│     ├─ supabase/client.ts            # browser client (anon key)
│     ├─ supabase/server.ts            # server client (cookies)
│     ├─ gatewayAuth.ts                # gateway refresh + 401 retry
│     └─ gateway.ts                    # AI relay calls
├─ supabase/{config.toml, migrations/<timestamp>_init.sql, functions/user-sync/index.ts}
├─ proxy.ts                            # Supabase session refresh (middleware.ts pre-16)
└─ public/ · .env.example · next.config.ts · package.json · tsconfig.json
```

`src/lib/supabase/client.ts` uses only the anon/publishable key and is safe in the browser; `server.ts` reads cookies and still uses the anon key unless a route explicitly needs elevated privileges. The `service_role` / secret key is imported only by trusted server code or an Edge Function, never by a Client Component and never with a `NEXT_PUBLIC_` prefix. Business data access goes through Supabase + RLS; if you choose a BFF instead, business tables can live in your own database, but the identity and billing chapters still apply. The runnable reference app uses this layout without the `src/` prefix; see `references/web/outfit-demo/README.md`.

## Step 2: Identity (OAuth / OIDC)

new-api is the OIDC identity provider; your app never stores new-api passwords. Authorization Code + PKCE is the only supported flow. Full contract and code: `references/standards/identity.md`.

- **Public or confidential, decide once**: a server that can keep a secret uses a confidential client (BFF, `client_secret` server-only); an SPA or mini-program uses a public client (`public_client = true`, no secret, PKCE mandatory). Never enable public mode for a server-side app.
- Generate `state`, `nonce`, and the PKCE `code_verifier` on the server, keep them in HttpOnly / `Secure` / `SameSite=Lax` cookies, and validate `state` and the verifier on the callback.
- Supabase direct mode: the provider identifier must be exactly `custom:newapi`; the gateway must already be publicly reachable over HTTPS with an issuer equal to its public base URL **before** the provider is created; the app's redirect URI is `https://<project>.supabase.co/auth/v1/callback`.
- Supabase does not refresh a custom provider's token: refresh through `POST <gateway>/oauth/token` before the 1 hour expiry, store the rotated refresh token, sign out on `invalid_grant`, and revoke the refresh token on logout.

### Local acceptance

Tunnel the **gateway**, not the frontend; full flow, symptoms, and Supabase URL configuration: `references/standards/local-acceptance.md`. Re-run the append below whenever the tunnel hostname changes.

```powershell
# Windows / PowerShell — append the tunnel callback to the OAuth app
$appId = "3"; $tunnelOrigin = "https://xxx.trycloudflare.com"
$current = newapi-cli api GET "/api/oauth-apps/$appId" | ConvertFrom-Json
$uris = @($current.redirect_uris)
if ($uris -notcontains "$tunnelOrigin/auth/v1/callback") { $uris += "$tunnelOrigin/auth/v1/callback" }
newapi-cli api PUT "/api/oauth-apps/$appId" --data (@{ redirect_uris = $uris } | ConvertTo-Json)
```

```bash
# macOS / Linux (jq) — same append, idempotent
app_id=3; tunnel="https://xxx.trycloudflare.com"; callback="$tunnel/auth/v1/callback"
uris=$(newapi-cli api GET "/api/oauth-apps/$app_id" | jq -c --arg cb "$callback" '(.redirect_uris // []) + [$cb] | unique')
newapi-cli api PUT "/api/oauth-apps/$app_id" --data "{\"redirect_uris\":$uris}"
```

## Step 3: AI Relay Calls

All AI traffic goes to `/v1/*` with the OAuth access token; that token is not a dashboard credential and `/api/*` rejects it. Proxy code and rules: `references/standards/relay.md`.

- BFF: a Route Handler attaches the token and returns `upstream.body` so streams stay unbuffered; public clients call the gateway directly.
- Never log the `Authorization` header, token, request body, or upstream body; log method, path, status, latency, and a request id only.
- Never forward cookies, `client_secret`, or dashboard PATs upstream; preserve the upstream status and content type.
- Retry a `401` exactly once after refresh, then surface it.

## Step 4: Users, Quota, and Payments

The user system lives under `/api/*`, which OAuth tokens cannot reach; quota, usage, top-up, and subscription calls historically needed a dashboard session or a PAT — but new-api now exposes three `/oauth/...` endpoints that accept OAuth access tokens directly. Full contract: `references/standards/billing.md` and `references/standards/identity.md`.

- Link accounts on `sub` from `GET /oauth/userinfo`; `username` and `email` are mutable and email may be empty. OAuth login never creates a new-api user.
- 外部 app 用 OAuth access token 调 `/oauth/userinfo` 拿身份，带 `api:read` 时还能拿到额度；站内充值走 `/oauth/topup/checkout`（modal），日志走 `/oauth/logs`；不要跳 newapi 仪表板。
- PATs (`nap_...`, shown once, scoped, `expires_at` at least 1 hour ahead when set) need a browser session and a security proof; a PAT cannot mint another PAT and never exceeds the user's role.
- `amount` is in the configured display unit — never send a quota value as an `amount`.

## Step 5: Backend on Supabase

Mandatory for any app that stores new-api users or business data in Supabase; conventions in `references/standards/supabase.md`.

| Client | Key | Rule |
|--------|-----|------|
| Browser | anon / publishable | Relies on RLS for authorization |
| Server | `@supabase/ssr` + cookies | Same session in Server Components and Route Handlers |
| Session refresh | `proxy.ts` (`middleware.ts` before Next.js 16) | `getUser()` keeps the cookie session fresh and protects routes |
| Trusted server / Edge Function | `service_role` / secret | Bypasses RLS; never expose it and never prefix it with `NEXT_PUBLIC_` |

- **RLS + `client_id` is the enforcement point**: every business table carries `client_id text not null`, policies compare it to `current_app_client_id()` (security definer) and `auth.uid()`, and `client_id` is derived from the JWT, never from the request body. A check that exists only in a Client Component or a query filter is not authorization.
  - **⚠️ `current_app_client_id()` 必须从 `user_metadata.aud` 取，不要从 `app_metadata.client_id`**：Supabase OAuth 回调自动填充的 `app_metadata` 只有 `provider` / `providers`，**不会自动写入 `client_id`**。new-api OAuth 的 `client_id` 实际落在 JWT 的 `user_metadata.aud` 数组里（上游 OIDC provider 的 `aud` claim）。函数实现模板：
    ```sql
    CREATE OR REPLACE FUNCTION public.current_app_client_id()
      RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path TO 'public' AS $$
      select coalesce(
        auth.jwt() -> 'app_metadata' ->> 'client_id',          -- 兜底：手动设置过的
        (auth.jwt() -> 'user_metadata' -> 'aud' ->> 0),         -- 主路径：new-api OAuth 写入 aud[0]
        ''
      );
    $$;
    ```
    这个坑导致的症状是：**所有 Server Component 查询返回空结果，但 anon client 直接打 storage API 能拿到数据**。修之前先 `select current_app_client_id()` 看返回值是不是你的 OAuth app `client_id`。
- Edge Functions verify the caller with `supabase.auth.getUser()`, derive `gateway_user_id` / `client_id` from `app_metadata`, and use the service-role key only inside the function. Work beyond the function limits returns a job id (see **Long-Running Work and Supabase Limits** in `references/standards/supabase.md`).
- **Media goes to Supabase Storage**: the database keeps only `bucket` + `path` (`{client_id}/{user_id}/{uuid}.{ext}`), reads use short-lived signed URLs from a private bucket, and MIME type and size are validated server-side.
- The app-side `seed-assets.ts` uploads a `pack.json` asset library and upserts by `(client_id, provider_id)`; bootstrap runs it when a pack root is given (see **Seeding the Asset Packs** in `references/standards/supabase.md`).

## API 契约

分页（统一 offset-based）、`{success, message, data}` 响应 envelope、错误码命名、i18n 消息 key：
`references/standards/api-contract.md`。所有 Next.js Route Handler 必须跟 new-api `common/gin.go` 的 `ApiSuccess` / `ApiError` 格式对齐。

## Step 6: Frontend Standards

Conventions: `references/standards/nextjs.md`, `references/standards/daisyui.md`, `references/standards/ui-ux.md`, plus `references/vendor/`.

- Server Components by default; `'use client'` only on the smallest interactive leaf. Never pass an access token, refresh token, `client_secret`, or service-role key into a Client Component.
- Reuse an existing component before writing markup; compose DaisyUI (`btn`, `card`, `navbar`, `drawer`, `modal`, `table`, `tabs`, `alert`, `loading`, `skeleton`) and set the theme once with `data-theme` on `<html>`.
- **Only valid DaisyUI 5 classes**: `input-bordered`, `textarea-bordered`, `select-bordered`, `card-compact`, `card-bordered`, `tabs-boxed`, the removed `join`/`join-item`, and the non-existent `chip-*` silently do nothing. Use `input` / `textarea` / `select` / `card-sm` / `card-border` / `tabs-box`/`tabs-border`, plain `flex gap-*` or `label.input` for grouped inputs, and `btn btn-xs` for filter chips. Run the acceptance commands in `references/standards/daisyui.md#8` before delivery.
- **Compose, do not rebuild**: containers, inputs, alerts, and dialogs must be `card` + `card-body`, `input` / `textarea` / `select`, `alert`, and `dialog.modal` + `modal-box`; Tailwind utilities are for layout only. `src/app/admin/**` (Ant Design + Refine) is the sole exception and never shares a layout with the consumer app.
- **OAuth callback is server-side and single-use**: exchange the code in a Route Handler `GET` (`src/app/auth/callback/route.ts`) with exactly one `exchangeCodeForSession`. Never call it from a client `useEffect` / `useLayoutEffect` — React 19 StrictMode runs the effect twice, the code is redeemed twice, and Supabase returns `invalid flow state, no valid flow state found`. If `auth.getUser()` already has a session, redirect without exchanging.
- Every data view needs loading, empty, and error states; destructive actions need confirmation; payment and logout show the resulting state; forms validate on the client for feedback and on the server for correctness.

## Step 7: Deploy with Vercel

Deployment commands, preview handling, and rollback: `references/standards/vercel.md`.

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

- **Update the three origins together** in one change window: the Vercel production domain (`APP_ORIGIN`), the new-api OAuth app redirect and post-logout URIs, and Supabase Auth (Site URL + redirect allow list). In direct mode the redirect URI stays `https://<project>.supabase.co/auth/v1/callback` and the provider issuer must stay the gateway's public HTTPS URL.
- Preview deployments get a new origin each time: do not register a wildcard redirect URI. Changing the Supabase project domain changes the callback URL, so recreate `custom:newapi` afterwards. Never treat a preview or tunnel URL as a permanent deployment address.

## Step 8: Admin Dashboard

Optional surface with a different stack: Next.js App Router + Ant Design 5 + `@refinedev/core` + Supabase. Conventions: `references/standards/admin-dashboard.md`.

- What OAuth gives the console: `sub` always, `username` / `name` with `profile`, `email` with `email`; the `id_token` carries no name, email, or role. Key the admin account on `sub`.
- **The app's admin role is not the new-api role** (`1` / `10` / `100`): store it in `auth.users.app_metadata.admin_role` (service-role writes only) or in an `app_admins` table; never read a role from `user_metadata`, a query string, or a request body.
- Every admin Route Handler and the admin layout call the same `requireAdmin()`; client checks and `proxy.ts` are not authorization boundaries.
- Ant Design (admin) and Tailwind + DaisyUI (consumer) never share a layout or route tree; `accessControlProvider` mirrors the server guard, never replaces it.
- **The first admin defaults to the developer who created the OAuth app**: `oauth_apps.created_by` equals their `sub`, so set `INITIAL_ADMIN_SUB` and seed `app_admins` once out of band. No endpoint or "first signup wins" rule may create an admin; block self-promotion and removing the last owner.

## Security Checklist

- [ ] Confidential clients keep `client_secret` in backend env vars only; public clients have no secret and rely on PKCE.
- [ ] `redirect_uri` exactly matches a registered URI, and the PKCE verifier from that request is validated on callback.
- [ ] `state` is validated against a server-held value; `nonce` is per request and validated when an `id_token` is consumed.
- [ ] State, verifier, and session cookies are `HttpOnly`, `Secure` in production, and `SameSite=Lax`.
- [ ] Gateway tokens stay in memory and are never logged or written to `user_metadata`; a rotated refresh token replaces the old one.
- [ ] OAuth tokens are used for `/v1/*`, `/oauth/userinfo`, `/oauth/topup/checkout`, and `/oauth/logs` only; every `/api/*` dashboard endpoint rejects them.
- [ ] A browser return URL or `pending` order is never proof of payment; admin permissions come from your own store and the server guard.
- [ ] `SUPABASE_SECRET_KEY` is imported only by `server-only` code and never exposed through `NEXT_PUBLIC_*`.
- [ ] All media lives in Supabase Storage private buckets; only `bucket` + `path` is in Postgres.
- [ ] Balance, credits, orders, and usage come from new-api; there is no local ledger.

Full list: `references/standards/security.md`.

## Token Lifecycle

- **Access token**: JWT, 1 hour TTL, used to call AI APIs and userinfo.
- **Refresh token**: Opaque, 30 day TTL, rotated on each use.
- **Authorization code**: Single-use, 60 second TTL.
- **The provider token is not refreshed for you**: read `provider_token` / `provider_refresh_token` at login, refresh via `POST <gateway>/oauth/token` (`grant_type=refresh_token`) when less than 60 s remain, persist the rotated refresh token, and on `invalid_grant` clear local state and send the user through login again. A relay `401` is retried once with a fresh token, never in a loop.

Details: `references/standards/security.md`.

## Scopes Reference

| Scope | Grants access to |
|-------|-----------------|
| `openid` | User ID (sub claim) — required for OIDC |
| `profile` | Username, display name |
| `email` | Email address |
| `api:read` | Read-only AI endpoints (`GET` / `HEAD` / `OPTIONS`, e.g. `/v1/models`) |
| `api:write` | Non-read AI endpoints (chat, images, embeddings, ...). Request both if the app also reads. |

These scopes belong to OAuth access tokens only. Dashboard personal access tokens use a separate catalog (`profile:read`, `profile:write`, `account_security:write`, `wallet:read`, `wallet:write`, `billing:read`, `billing:write`, ...); list it with `GET /api/user/access_tokens/scopes`.

## Model Whitelist per App

Each OAuth App in new-api can restrict which AI models are available:

```sql
-- In new-api admin, set on the OAuth App:
ai_model_whitelist = '["gpt-4o-mini","gpt-4o"]'
```

When set, the relay rejects any model not in the list. Leave empty to allow all models. This is enforced in `controller/relay.go` via `service.CheckOAuthAppModelWhitelist()`.
