---
name: new-api-app-framework
description: Build external Web apps against new-api. Prefer existing Supabase login with backend-held per-user API keys and PAT management; retain optional OAuth/OIDC. Covers relay, quota, payments, and the reference outfit app.
---

# new-api App Development Framework (Web)

This skill is the standard for building an external **Web** application that uses new-api as its AI gateway and billing backend. Prefer the app's existing Supabase identity system and a trusted backend that holds each user's API key. new-api OAuth/OIDC remains an optional identity integration. The stack chapters are defaults; credential separation, user/App ownership, and billing contracts apply across stacks.

## Integration Choice — Proxy First

**Recommend server-side API-key proxy mode first**, especially when the app already uses Supabase Auth. The user signs in to Supabase; the app backend validates the session, resolves the trusted new-api user mapping, and calls `/v1/*` with that user's `sk-...` key. Management and checkout use a scoped admin `nap_...` PAT on the backend. A PAT is not an AI key, and an OAuth BFF that proxies an OAuth token is a different integration mode.

Choose new-api OAuth/OIDC when the user explicitly wants new-api sign-in or delegated authorization. Both modes may coexist; retain working OAuth integrations during migration. Read **Integration choice and current capability gate** in `references/standards/identity.md` before choosing endpoints or running bootstrap.

**Product boundary:** dual mode provides independent user populations, not account linking. Proxy users have dedicated gateway user IDs and assets; never reuse an OAuth user's mapping or merge by email/Supabase UUID. Proxy mode supports App wallet balance, top-ups and one-time quota products only, **not subscriptions**. Enforce this at reads, checkout, admin grants and relay funding; OAuth subscriptions remain available to OAuth users. A key's quota/window limit caps spending and does not create a subscription or wallet funds. Verify the deployed capability gate below.

A natural person using multiple Apps has a separate proxy gateway user ID per App. The proxy account has immutable App ownership: never create another App's key for that account or reuse it across Apps. Existing OAuth/platform account behavior is unchanged. Provisioning must implement the atomic, recoverable identity contract in `references/standards/identity.md`; existing ordinary user creation is insufficient. Rotation must preserve the shared spending budget described in `references/standards/relay.md`. Use the separate normalized wallet contract in `references/standards/billing.md`; legacy OAuth platform/App fields keep their meanings. That reference also defines signed debt display, the OAuth order-read surface, stable checkout IDs with recoverable idempotency across supported payment branches, and proxy-account ownership filtering; verify these capabilities before treating them as available.

**Current capability gate:** this branch implements proxy provisioning, immutable App-bound personal keys, admin-PAT checkout, normalized wallets, exact credit display and a proxy-first Supabase reference. This does not mean an older deployed gateway has these contracts. Probe the configured App and use the routes in `references/standards/api-contract.md`; fail explicitly if unavailable. Never silently fall back to OAuth or a platform AI key. Proxy payments currently support Waffo Pancake top-ups and one-time products. Limited-key rotation is rejected until shared budget continuity is available. Real hosted-payment acceptance still requires sandbox credentials; local tests are not a release guarantee.

## Timed membership gates in proxy mode

Prefer proxy mode for existing Supabase users. After verifying an App-owned membership payment, the trusted backend may create/update a target-owned quota rule and atomically issue a key with `quota_rule_id`. Use the specified-user contracts in `references/standards/api-contract.md`; never use the PAT actor's self rules. Dates only restrict new calls: **fund the same user/App wallet separately**. Proxy users still have `subscription_supported=false`; do not call new-api subscription purchase/grant APIs.

`valid_from` and `valid_until` are nullable UTC Unix seconds, with interval `[start,end)`. Keep the rule enabled to enforce expiry. Disabling/unbinding removes its restrictions. Calendar budgets are per key and reset at natural day/week/month boundaries in UTC or Asia/Shanghai, not one month after purchase. Extending dates does not reset usage. Preserve null versus omitted PUT fields, and use server `validity_status` for display.

Rule-bound keys (even disabled rules) cannot rotate without shared budget continuity. Cancel every pending replacement with `cancel-rotation` before binding the original key; cancellation never changes the original key or its budget. Cancelled operation IDs cannot be replayed for credentials or committed/re-enabled. A legacy task reservation without a snapshot may settle/refund but cannot reserve extra work (`reservation_snapshot_unavailable`). A pre-submit task stranded in `SUBMITTING` requires reconciliation of its upstream outcome; do not automatically retry or refund it.

The reference App exposes only guarded admin rule/cancel operations at `/api/admin/gateway-proxy/<native-user-uuid>/...`: it resolves the protected mapping and pins the configured App. Ordinary consumers cannot edit paid membership gates. Admin PATs are globally authorized today; `app_id` is an ownership parameter, not a PAT resource restriction.

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
| Identity | Existing Supabase Auth | Preferred proxy mode keeps login in the app; OAuth/OIDC is optional |
| AI access | Backend-held per-user API key | Validate the app session; verify the key's actual user/App funding scope |
| Admin console (optional) | Ant Design 5 + `@refinedev/core` | Separate route tree from the consumer app; see **Step 8** |

The consumer app uses Tailwind + DaisyUI. An admin console is the one deliberate exception: it uses Ant Design and must not share a layout with the consumer app.

You may adapt the stack to an existing platform or deployment requirement. Keep **Step 2 (Identity)**, **Step 4 (Users, Quota, and Payments)**, and the mode-applicable security rules. Server-only secrets and trusted ownership checks apply to both modes; PKCE, OAuth state/nonce validation, and provider-token refresh apply when OAuth is enabled.

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
| Proxy-first choice, capability gate, optional OAuth clients and `custom:newapi` | `references/standards/identity.md` | Selecting an integration or implementing login (Step 2) |
| AI relay: BFF proxy code, rules, direct calls | `references/standards/relay.md` | Calling `/v1/*` (Step 3) |
| Quota, `userinfo`, PAT, sessions, payments, credits | `references/standards/billing.md` | Reading balance or taking payment (Step 4) |
| Security checklist and token lifecycle, in full | `references/standards/security.md` | Reviewing a flow or handling tokens |
| Local acceptance: Cloudflare Tunnel and Supabase URL config | `references/standards/local-acceptance.md` | Testing the flow locally |
| Runnable demo app | `references/web/outfit-demo/README.md` | Wanting a working reference |
| Vendor source provenance | `references/vendor/README.md` | Auditing vendored material |
| OAuth environment self-check | `scripts/check-env.ps1`, `scripts/check-env.sh` | OAuth mode, Step 0.1 / 0.4 |
| OAuth one-shot bootstrap | `scripts/bootstrap.ps1`, `scripts/bootstrap.sh` | OAuth mode, Step 0.3 |

## Prerequisites

1. A new-api instance running and accessible over HTTPS.
2. Proxy mode: an existing app login, a trusted backend for per-user keys and a scoped admin PAT, a stable user mapping, and verified server support for the intended wallet/checkout operations.
3. OAuth mode only: permission to register an OAuth application, plus either a confidential backend that stores `client_secret`, or a public PKCE client with no secret. Supabase OIDC direct mode is the latter; it is not Supabase-native proxy mode.

## Credential Surfaces

Each new-api surface accepts a different credential. Sending the wrong one is the most common integration failure.

| Surface | Endpoints | Accepted credential |
|---------|-----------|---------------------|
| OAuth / OIDC | `/oauth/authorize`, `/oauth/token`, `/oauth/userinfo`, `/oauth/introspect`, `/oauth/revoke`, `/oauth/topup/checkout`, `/oauth/subscription/plans`, `/oauth/subscription/checkout`, `/oauth/products`, `/oauth/products/checkout`, `/oauth/logs`, `/.well-known/openid-configuration` | OAuth access token / client credentials |
| AI relay | `/v1/*` | OAuth access token (`api:read` for GET/HEAD, `api:write` for other methods), or a new-api API token (`sk-...`) |
| Dashboard / user system | `/api/user/*`, `/api/user/topup*`, `/api/user/pay`, `/api/subscription/*` | Browser session or personal access token (`nap_...`) |
| Administration | `/api/user/` admin routes, `/api/subscription/admin/*`, `/api/app-products*` | Admin or root session, or admin personal access token |

An OAuth access token is accepted by the AI relay and `/oauth/*` endpoints above; every `/api/*` dashboard endpoint rejects it. App-scoped plans, products, top-up, quota, and usage therefore need no dashboard session or PAT — `/oauth/userinfo` (identity and quota, `api:read`), `/oauth/subscription/plans` and `/oauth/products` (`api:read`), `/oauth/subscription/checkout`, `/oauth/products/checkout`, and `/oauth/topup/checkout` (`api:write`), and `/oauth/logs` (`api:read`) cover them. Personal access tokens (`nap_...`) remain available for trusted backend integrations that need full dashboard scope.

## End-to-End Development Workflow

Select the integration first, then complete the applicable path. Existing scripts and CLI commands must be checked against the target server; the proxy path does not automatically create an OIDC provider.

1. **Confirm the server contract and App identity.** Prefer proxy mode; verify the user mapping, key funding scope, and specified-user management/checkout support. For OAuth, register the client and confirm `client_id` and redirects. Create a repository only when that is part of the user's task.
2. **Reuse or configure Supabase and deployment.** Preserve existing users and project bindings. Proxy mode uses native app login and server-only credentials; OAuth mode additionally runs Step 0's provider/issuer bootstrap checks. Reuse existing infrastructure when available.
3. **Build and verify the selected path.** Test login, user/App isolation, AI consumption, and payment results before deployment. Proxy mode must demonstrate that the key charges the same wallet the checkout funds.
4. **Configure the selected mode's payments.** Proxy mode uses verified specified-user top-ups and one-time quota products; do not create or grant subscriptions to proxy users. OAuth mode may configure App subscription plans/products using the existing supported CLI and payment matrix. Wire callbacks to the same user's App wallet and confirm the actual order state.
5. **Go live when authorized.** Promote the verified deployment and align the app origin, Supabase URL configuration, and payment return URLs. Update OAuth redirect and post-logout URIs only for apps using OAuth.

### newapi-cli updates must sync the skills (mandatory)

The gateway distributes the CLI and **both** `newapi-cli` and `new-api-app-framework` skills through `/api/cli/manifest` and `/api/cli/download/:name`. npm publishing has stopped. Installer downloads time out after three minutes; Windows disables slow progress rendering. Prefer the administrator avatar menu **CLI installation & usage** for server-specific installation commands.

- Every CLI command or behavior change MUST update both skill entrypoints in the same change.
- Run `go run ./cmd/newapi-cli-assets <version>` before the gateway build. Docker/release builds do this automatically. The manifest checksums pin all six binaries and both complete skill trees to one version; verify `skills.zip` before shipping.
- `newapi-cli self-update --run --server https://your-gateway` updates the native CLI and both global skills together, preserving login profiles. Skills live in `~/.agents/skills`; the CLI lives in `~/.newapi-cli/bin`. Existing skill directories are replaced; keep local customizations elsewhere. No npm or GitHub download is involved.

## Step 0: Environment Bootstrap

Install the applicable toolchain. Bootstrap defaults to proxy prerequisite validation and migrations, with process environment variables configured as in the reference README. Use `-Mode oauth` / `--mode oauth` explicitly for the provider setup below. The provider checks are the **OAuth path**; they create/reconcile `custom:newapi` and must not run as prerequisites for Supabase-native proxy mode. For proxy mode, verify the existing Supabase login, backend secret storage, trusted user mapping, and actual key/checkout capabilities instead.

### 0.1 One command with optional auto-install

`./scripts/check-env.ps1 -Install` (Windows) / `./scripts/check-env.sh --install` (macOS/Linux). Without the flag they only verify and print the exact fix; with it they install missing CLIs through the platform package manager or npm (including `supabase` and `vercel`) and re-run the checks. `newapi-cli` is installed through the gateway backend and is only verified by these bootstrap checks.

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

The layout below is the existing OAuth reference. In proxy mode, reuse native Supabase login and its callbacks; omit gateway OAuth start/callback and provider-refresh code. Its existing `user-sync` function is OIDC-specific, not a ready-made proxy user mapping.

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
│     ├─ supabase/client.ts            # browser client (anon key)
│     ├─ supabase/server.ts            # server client (cookies)
│     ├─ gateway-session.ts            # gateway refresh + 401 retry
│     └─ gateway.ts                    # AI relay calls
│  └─ proxy.ts                         # Supabase session refresh (middleware.ts pre-16)
├─ supabase/{config.toml, migrations/<timestamp>_*.sql, functions/user-sync/index.ts}
└─ public/ · .env.example · next.config.ts · package.json · tsconfig.json
```

`src/lib/supabase/client.ts` uses only the anon/publishable key and is safe in the browser; `server.ts` reads cookies and still uses the anon key unless a route explicitly needs elevated privileges. The `service_role` / secret key is imported only by trusted server code or an Edge Function, never by a Client Component and never with a `NEXT_PUBLIC_` prefix. Business data access goes through Supabase + RLS; if you choose a BFF instead, business tables can live in your own database, but the identity and billing chapters still apply. The runnable reference app follows this layout, with the consumer routes under `(consumer)` route groups and an `admin/` tree for the Ant Design console; see `references/web/outfit-demo/README.md`.

## Step 2: Identity (Proxy First; OAuth Optional)

Start with the proxy-first branch in `references/standards/identity.md`: retain Supabase login, verify the session on the backend, resolve the trusted user/App mapping, and keep per-user keys and the management PAT server-side. Check availability before promising App-bound keys or specified-user checkout.

The remaining login instructions and local-acceptance commands in this step apply **only to OAuth mode**. In that mode new-api is the OIDC provider and Authorization Code + PKCE is the supported authorization flow; the app does not collect the user's new-api password.

- **Public or confidential, decide once**: a server that can keep a secret uses a confidential client (BFF, `client_secret` server-only); an SPA or mini-program uses a public client (`public_client = true`, no secret, PKCE mandatory). Never enable public mode for a server-side app.
- Generate `state`, `nonce`, and the PKCE `code_verifier` on the server, keep them in HttpOnly / `Secure` / `SameSite=Lax` cookies, and validate `state` and the verifier on the callback.
- Supabase direct mode: the provider identifier must be exactly `custom:newapi`; the gateway must already be publicly reachable over HTTPS with an issuer equal to its public base URL **before** the provider is created; the app's redirect URI is `https://<project>.supabase.co/auth/v1/callback`.
- Supabase does not refresh a custom provider's token: refresh through `POST <gateway>/oauth/token` before the 1 hour expiry, store the rotated refresh token, sign out on `invalid_grant`, and revoke the refresh token on logout.

### Local acceptance

For OAuth with hosted Supabase, tunnel the **gateway**. Native proxy acceptance also needs a public HTTPS App origin (`APP_ORIGIN`) and, for a local Supabase stack, an exact public Auth issuer; full flow, symptoms, and Supabase URL configuration: `references/standards/local-acceptance.md`. Re-run the append below whenever the tunnel hostname changes.

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

All AI traffic goes to `/v1/*`. Proxy mode attaches the authenticated user's backend-held API key; OAuth mode uses the OAuth access token. A dashboard PAT is not a relay credential. Proxy rules, capability limits, and the OAuth example: `references/standards/relay.md`.

- Proxy mode: resolve the key from the trusted session mapping and fixed App configuration, then return `upstream.body` without buffering. OAuth BFFs attach their OAuth token; OAuth public clients may call the gateway directly.
- Never log the `Authorization` header, token, request body, or upstream body; log method, path, status, latency, and a request id only.
- Never forward cookies, `client_secret`, or dashboard PATs upstream; preserve the upstream status and content type.
- OAuth mode may retry a `401` once after token refresh. API keys have no OAuth refresh flow; surface invalid/revoked-key failures and use the backend's explicit key lifecycle management.
- Relay billing for an OAuth access token is **App-scoped**: pre-consume, settlement, and refund all use the quota pool of the App that owns the token (`app_quota`), never the user's platform wallet.
- Async task endpoints (video, music, and other task submissions, including Midjourney) accept App access tokens. Pre-consume, completion-delta settlement, and refunds all use the App quota pool, the App wallet `used_quota` is maintained alongside platform statistics, and tasks are isolated by `app_id`: an App token can only read, remix, or cancel tasks its own App created, and Midjourney fetch/action endpoints resolve tasks within that same App.

## Step 4: Users, Quota, and Payments

Proxy mode uses verified specified-user management and payment contracts with a backend-only admin PAT. Existing self-payment and `/api/token/*` routes act on the PAT owner; adding `user_id` does not make them delegated APIs. App-bound keys, specified-user checkout and returned credit fields are implemented on this branch; verify deployed support. Full current contract and limits: `references/standards/billing.md`.

Proxy mode is balance-only even on dual-mode Apps. Its subscription capability is false; hide subscription UI and reject subscription reads, purchases, admin grants and subscription funding on the server. A balance shortfall must fail, never consume a residual App/platform subscription. Keep proxy and OAuth user IDs, orders and assets independent.

The following `/oauth/*` table and token-derived App scoping describe **OAuth mode**. OAuth tokens cannot reach dashboard `/api/*`; do not send the proxy-mode PAT to `/oauth/*`.

Every call is scoped by the access token, not by request data: the token's `client_id` selects the App, and a request body `app_id` is only rejected when it does not match. Plans and products of other Apps, and platform plans (`app_id = 0`), are never returned or purchasable through it.

| Endpoint | Scope | Contract |
|----------|-------|----------|
| `GET /oauth/userinfo` | `openid`; `api:read` for quota | `sub`, plus `username` / `name` (`profile`) and `email` (`email`). With `api:read`: `quota`, `used_quota`, `request_count`, `group`, and `app_quota` / `app_used_quota` for the token's App. |
| `GET /oauth/subscription/plans` | `api:read` | `{"plans":[{"plan":{...}}]}` — enabled plans owned by this App only. |
| `POST /oauth/subscription/checkout` | `api:write` | `{"plan_id":1,"payment_method":"balance"}`; balance returns `{"status":"success","payment_method":"balance"}`, online methods return a `pending` order with a checkout link. |
| `GET /oauth/products` | `api:read` | `{"products":[{id,name,description,price_amount,currency,quota_amount,max_purchase_per_user}]}` — enabled products of this App only. |
| `POST /oauth/products/checkout` | `api:write` | `{"product_id":1,"payment_method":"balance"}`; balance returns `{"status":"success","order_id":"...","quota_amount":...}`, `waffo_pancake` returns `checkout_url`. |
| `POST /oauth/topup/checkout` | `api:write` | `{"amount":10,"payment_method":"balance"}`; balance returns `{"status":"success","quota":...}`, `waffo_pancake` returns `checkout_url` / `order_id` / `expires_at`. |
| `GET /oauth/logs` | `api:read` | Redacted request logs of the token owner. |

- **Two balances, one billing rule**: `quota` stays the platform wallet and `app_quota` is the App pool. Relay pre-consume, settlement, and refund for an OAuth token only touch that App pool (plus App-scoped subscription quota); the platform wallet is not charged.
- **An App pool starts empty**: the wallet row is created lazily with `0` on first credit, and `app_quota` reads as `0` before that. Fund it by `POST /oauth/topup/checkout` or by buying an App product; both credit `app_quota`.
- **Balance top-up is a transfer, not a grant**: `payment_method: "balance"` debits the user's platform wallet by the quota converted from `amount` and credits that same quota to the App pool. Insufficient platform balance or a violated minimum/maximum returns HTTP `400` `checkout_failed`; nothing is minted. `waffo_pancake` (the default when `payment_method` is omitted) credits the pool only after the provider confirms payment.
- **Payment matrix** — plans: `balance`, `epay`, `stripe`, `creem`, `waffo_pancake`; products: `balance`, `waffo_pancake`; App top-up: `balance`, `waffo_pancake`. Any other value returns `400` `unsupported_payment_method`. `balance` on a plan or product debits the App pool; `balance` on top-up moves platform balance into the App pool.
- **Cross-App purchase is rejected**: a plan or product owned by another App, a disabled plan/product, or a reached per-user purchase limit returns `403 access_denied` or `400 checkout_failed`; a missing `plan_id` / `product_id` returns `400 invalid_request`, as does an omitted `payment_method` on plan or product checkout (top-up defaults to `waffo_pancake`).
- **Subscription state and order confirmation differ**: `/oauth/userinfo` includes the active App subscription. A balance change cannot prove a particular order succeeded; verify the target server's order-query capability and confirm the order itself, never a browser return URL or `pending` response. Proxy mode does not expose subscriptions.
- Link accounts on `sub` from `GET /oauth/userinfo`; `username` and `email` are mutable and email may be empty. OAuth login never creates a new-api user.
- 外部 app 用 OAuth access token 调 `/oauth/userinfo` 拿身份与额度，站内充值走 `/oauth/topup/checkout`（modal），套餐/商品走 `/oauth/subscription/*` 和 `/oauth/products*`，日志走 `/oauth/logs`；不要跳 newapi 仪表板。
- 每个 App 在后台独立配置自己的套餐与商品（按 `app_id` 归属），外部 app 只看到自己 App 的那一份；后台按 App 筛选用户与订单见 `references/standards/api-contract.md`。
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

- **RLS + trusted App ownership is the enforcement point**: policies enforce both the configured App and `auth.uid()`. Proxy mode needs a protected user/App mapping compatible with native Supabase identities; the existing OIDC helper is not a proxy implementation. In OAuth mode, every business table carries `client_id text not null`; policies enforce `current_app_client_id()` and `auth.uid()`, bound to exactly one verified `auth.identities` row with `provider = 'custom:newapi'` and its provider-owned `identity_data.sub` / audience. Use the fail-closed helper in `references/standards/supabase.md` §3 for that OAuth path; missing, malformed, mismatched, or ambiguous identities must deny access.
  - Never trust `user_metadata`, `raw_user_meta_data`, or request fields for tenant, gateway identity, or admin grants — users can edit that metadata, including `aud` and `sub`. Supabase's automatically populated `app_metadata.provider` / `providers` is not an App binding; any server-written mapping must first verify the provider identity and configured issuer/client. Empty RLS results are not a reason to fall back to editable claims or anon Storage access.
- Edge Functions verify the caller with `supabase.auth.getUser()`, resolve gateway user/App ownership through the selected mode's protected mapping, and use the service-role key only inside the function. Work beyond the function limits returns a job id (see **Long-Running Work and Supabase Limits** in `references/standards/supabase.md`).
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

The OAuth issuer/client variables and provider-origin rules below apply only to OAuth mode. Proxy mode keeps its App ID, management PAT and per-user credential store server-only; it does not need a gateway OAuth client or provider issuer. Keep native Supabase Site URL and callback allow lists aligned with the deployed app.

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

Optional surface with a different stack: Next.js App Router + Ant Design 5 + `@refinedev/core` + Supabase. Conventions: `references/standards/admin-dashboard.md`. Proxy mode identifies app admins through verified Supabase identities and the app's trusted role store; the OAuth-subject bootstrap below applies only to OAuth mode.

- What OAuth gives the console: `sub` always, `username` / `name` with `profile`, `email` with `email`; the `id_token` carries no name, email, or role. Key the admin account on `sub`.
- **The app's admin role is not the new-api role** (`1` / `10` / `100`): store it in `auth.users.app_metadata.admin_role` (service-role writes only) or in an `app_admins` table; never read a role from `user_metadata`, a query string, or a request body.
- Every admin Route Handler and the admin layout call the same `requireAdmin()`; client checks and `proxy.ts` are not authorization boundaries.
- Ant Design (admin) and Tailwind + DaisyUI (consumer) never share a layout or route tree; `accessControlProvider` mirrors the server guard, never replaces it.
- **Grant the first owner out of band**: proxy mode uses an explicitly selected, verified native Supabase UUID and fixed App; it requires no OAuth login. Only the OAuth branch resolves the app developer's gateway `sub` through the protected provider identity. See the mode-specific procedure in `references/standards/admin-dashboard.md`; block self-promotion and removing the last owner.

## Hard-Won Constraints (from production bugs)

Each entry is a rule distilled from a real fix; the short hash links it to the commit for traceability. Violating one of these reproduces the original bug.

- **After sign-in, return to the gateway authorize page, not the dashboard** (`4ff7a8a6c`). When an unauthenticated `/oauth/authorize` redirects the user to sign-in, the post-login navigation MUST take the browser back to `/oauth/authorize` with a full page load (`window.location.assign`), never SPA client routing. Client navigation matches `/oauth/authorize` to the frontend `/oauth/$provider` route and shows a "missing code" error.
- **Honor `X-Forwarded-Proto` in same-origin checks behind a TLS proxy** (`10934556f`). When the gateway runs behind a TLS-terminating reverse proxy, origin validation MUST derive the scheme from `X-Forwarded-Proto` (fall back to `r.TLS`, then URL scheme) instead of assuming plaintext when `r.TLS == nil`; otherwise legitimate HTTPS requests are rejected as "request origin is not allowed". Keep session-cookie origin guards distrusting the client-supplied header.
- **Consent endpoints: no redundant `Origin`/`Sec-Fetch-Site` gate, and a fresh CSRF token on every load** (`2fa17c384`, `9d8ea00f8`). The consent `POST /oauth/authorize` relies on the session-bound CSRF token only — do NOT stack an extra `Origin`/`Sec-Fetch-Site` check (proxies/browsers strip it and legitimate submissions 403). Issue the CSRF token freshly on consent page load instead of carrying one in the redirect URL that may expire before submit.
- **Quote MySQL reserved-word columns such as `key` / `group`** (`fbc535356`). Never write raw SQL like `WHERE key = ?` against a reserved-word column — use the ORM struct form (`Where(&model.Option{Key: ...})`) or `commonKeyCol` / `commonGroupCol`, or MySQL raises error 1064.
- **The CLI must reject a plaintext non-loopback server before printing the authorize URL** (`0618ec20a`). Validate the server (scheme + reachability) and reject any non-loopback `http://` endpoint BEFORE emitting `/cli/authorize`, so the authorization code and token never travel in plaintext to a remote host.
- **Make long-running serverless work resumable** (`7dc68bd0c`). On Vercel/Edge, a long generation/try-on pipeline MUST be broken into idempotent, resumable steps (stepwise advance) with state persisted between steps, never one monolithic request — otherwise a platform timeout loses all progress.
  - **Resumable advance calling protocol** (`7dc68bd0c`): the create endpoint returns the task id immediately (never blocks on generation), then the browser polls `POST /api/generate/[id]/advance` one step at a time. Each `/advance` call reads the gateway access token from the request cookie, so it cannot run as a server-side background job — the browser must keep polling with its session. Steps are idempotent: stage outputs are persisted per step, so a closed tab pauses (not fails) the task, and re-entering with the same inputs re-runs `advance` from the persisted state (the create endpoint dedupes back to the original id).
- **Bill by the configured per-model price and surface real relay errors** (`26103a132`). The pre-generation quote MUST use the actually configured per-model price, never a hardcoded/frozen fee. Never swallow upstream relay errors: preserve the relay error code and HTTP status and pass through the real reason instead of a generic "request failed".
- **Render the generated result before history, with thumbnails** (`4c1ab5023`). After generation, show the result inline first and do not auto-redirect to history; render result thumbnails for succeeded rows so ordering and content stay correct under streaming/concurrent completion.
- **Persist the inviter when creating an OAuth user** (`48a6c87f9`). When `findOrCreateOAuthUser` creates a new user from OAuth login, persist the resolved `inviter_id` (from the affiliate code) onto the new user in the same atomic create, so the inviter relation is not silently dropped.

## Security Checklist

- [ ] Proxy mode validates the Supabase session, resolves user/App ownership server-side, and keeps each user's API key and scoped admin PAT outside browser code, profile metadata, and logs.
- [ ] Proxy mode verifies the actual key funding pool and specified-user checkout contract; ordinary platform keys are not assumed to spend App quota.
- [ ] Proxy/OAuth users remain independent, including on dual-mode Apps; proxy subscription reads, checkout, admin grants and funding are rejected, and quota-rule limits are not treated as subscriptions.
- [ ] OAuth-only callback, PKCE, state/nonce, and provider-refresh checks below apply when OAuth is selected; they are not prerequisites for native Supabase login.
- [ ] Confidential clients keep `client_secret` in backend env vars only; public clients have no secret and rely on PKCE.
- [ ] `redirect_uri` exactly matches a registered URI, and the PKCE verifier from that request is validated on callback.
- [ ] `state` is validated against a server-held value; `nonce` is per request and validated when an `id_token` is consumed.
- [ ] State, verifier, and session cookies are `HttpOnly`, `Secure` in production, and `SameSite=Lax`.
- [ ] Gateway tokens stay in memory and are never logged or written to `user_metadata`; a rotated refresh token replaces the old one.
- [ ] OAuth tokens are used for `/v1/*` and `/oauth/*` (`userinfo`, `subscription/*`, `products*`, `topup/checkout`, `logs`) only; every `/api/*` dashboard endpoint rejects them.
- [ ] Async tasks and Midjourney calls made with an App access token settle against the App quota pool and can only access tasks created by the same App (`app_id` isolation); no `app_task_unsupported` capability limit applies.
- [ ] A browser return URL or `pending` order is never proof of payment; admin permissions come from your own store and the server guard.
- [ ] `SUPABASE_SECRET_KEY` is imported only by `server-only` code and never exposed through `NEXT_PUBLIC_*`.
- [ ] All media lives in Supabase Storage private buckets; only `bucket` + `path` is in Postgres.
- [ ] Balance, credits, orders, and usage come from new-api; there is no local ledger.

Full list: `references/standards/security.md`.

## Token Lifecycle

Proxy mode manages its backend-held API keys and PATs through their actual expiry, revocation, and rotation controls; it does not mint or refresh OAuth provider tokens. The TTLs and refresh procedure below describe **OAuth mode**.

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
| `api:read` | Read-only AI endpoints (`GET` / `HEAD` / `OPTIONS`, e.g. `/v1/models`), quota in `/oauth/userinfo`, App plans/products, and `/oauth/logs` |
| `api:write` | Non-read AI endpoints (chat, images, embeddings, ...) and plan/product/top-up checkout. Request both if the app also reads. |

These scopes belong to OAuth access tokens only. Dashboard personal access tokens use a separate catalog (`profile:read`, `profile:write`, `account_security:write`, `wallet:read`, `wallet:write`, `billing:read`, `billing:write`, ...); list it with `GET /api/user/access_tokens/scopes`.

## Model Whitelist per App

Each OAuth App in new-api can restrict which AI models are available:

```sql
-- In new-api admin, set on the OAuth App:
ai_model_whitelist = '["gpt-4o-mini","gpt-4o"]'
```

For OAuth calls, the relay enforces this through `service.CheckOAuthAppModelWhitelist()`. App-bound proxy keys also enforce App status, key mode and model whitelist. Ordinary platform keys keep their existing platform behavior. Verify the target server supports App-bound keys.
