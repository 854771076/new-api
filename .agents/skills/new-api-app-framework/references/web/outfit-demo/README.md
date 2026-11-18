# AI Outfit — AI Try-On Reference Demo

A full-stack reference application for the **new-api-app-framework** skill. It demonstrates the complete chain the skill standardizes: Supabase native identity and new-api as the AI gateway, with optional OIDC, Supabase for auth/data/Storage, and Vercel for deployment.

Users sign in with their existing Supabase account by default, upload a character photo and garments, and generate AI try-on images through the gateway. The app ships with a consumer surface (wardrobe, characters, poses, history, profile) and an optional Ant Design admin console for catalog and plan management. Billing is App-scoped. Native proxy users use their own backend-held App AI key and admin-PAT management calls; they support top-ups and one-time products only. OAuth users retain separate gateway identities and subscriptions.

## Tech Stack

| Layer | Choice |
|-------|--------|
| Framework | Next.js (App Router) + TypeScript strict |
| UI runtime | React 19 |
| Consumer styling | Tailwind CSS v4 + DaisyUI 5 |
| Admin console | Ant Design 5 + `@refinedev/core` |
| Backend | Supabase (Auth, Postgres + RLS, Storage) |
| Identity / AI gateway | Supabase native + hosted personal App key; optional new-api OAuth 2.0 / OIDC |
| Deployment | Vercel |
| Package manager | bun |

## Requirements

- A new-api instance reachable over HTTPS.
- Supabase CLI (`supabase login`) and a Supabase project.
- `bun`.
- Optional: `newapi-cli` (bundled with the `newapi-cli` skill) for the one-shot bootstrap.

## Recommended native proxy setup

1. Create an App in new-api with API key mode enabled, OAuth disabled, and proxy_issuer exactly `https://<supabase-host>/auth/v1`. Set its positive App ID below. Set quota_per_credit when the App should display its own credits.
2. Use native Supabase email/password login. Phone identities are accepted by the guard; the login form implements email/password. Native and custom:newapi identities must remain independent; mixed linked identities are rejected.
3. Configure `.env.local` and export the same required variables in the shell before running `scripts/bootstrap.ps1` or the framework `scripts/bootstrap.sh`. Bootstrap validates the exact issuer/App and applies migrations; it does not load arbitrary dotenv shell text or create an OAuth provider. Run it from this reference's copied project, with the intended Supabase project already linked.
4. Keep GATEWAY_ADMIN_ACCESS_TOKEN, SUPABASE_SECRET_KEY and the encryption key server-only. Minimum PAT scopes: user:write, user:read, api_key:write, api_key:read, billing:read, billing:write, log:read, plus admin role (bootstrap additionally needs option:read). Never use an administrator AI key for relay.
5. For the optional console, set INITIAL_ADMIN_USER_ID to the exact verified native Supabase UUID. The service-only bootstrap grants only that identity the first owner membership. Remove the bootstrap variable after confirming access. OAuth owner initialization uses INITIAL_ADMIN_SUB separately.

| Variable | Exposure | Native proxy value |
|---|---|---|
| NEXT_PUBLIC_GATEWAY_INTEGRATION_MODE | Browser | proxy (default), oauth or dual; do not switch an existing identity mapping between modes |
| NEXT_PUBLIC_GATEWAY_APP_ID | Browser | Positive immutable new-api App ID |
| GATEWAY_ADMIN_ACCESS_TOKEN | Server only | Admin nap_ PAT with the required scopes |
| INITIAL_ADMIN_USER_ID | Server only | Optional exact native Supabase UUID for first owner |

On native login the BFF validates the signed token with Auth, checks issuer and the live session, provisions a stable `(issuer,subject,App)` gateway identity, and encrypts its personal AI key with AES-GCM in private tables. Browser input never selects a gateway user or App. Mapping/issuance retries recover the same identity/key. RLS binds content to the exact App session. Credits, charges and payment status always come from new-api; generation quote values are estimates, and unavailable actual charges stay null.

For OAuth use explicit `-Mode oauth` / `--mode oauth` during bootstrap and configure the OIDC section below. Dual mode offers both login buttons but never links users or wallets by email. A linked native/OAuth Supabase identity is rejected; isolate identity domains if the Auth provider would auto-link the same email.

The quote parser supports fixed prices or linear token expressions only. Other valid gateway expressions fail with PRICING_UNSUPPORTED until the reference estimator supports them. Token estimates are not spending caps.

To exercise real native Auth/RLS on an isolated migrated project, export SUPABASE_URL, SUPABASE_ANON_KEY and SUPABASE_SERVICE_ROLE_KEY and run `python scripts/verify-native-rls.py`. It creates/deletes disposable test users; use a dedicated test project. Use `bun run test` (serial module mocks), `bunx tsc --noEmit`, `bun run lint` and `bun run build` for local checks. Payment sandbox and public redirect acceptance must be run with configured provider credentials; do not substitute unit tests for that evidence.

## Environment Variables

Create `.env.local` using the variable table below. `.env*` is gitignored; never commit real secrets.

| Variable | Exposure | Purpose |
|----------|----------|---------|
| `NEXT_PUBLIC_SUPABASE_URL` | Browser | Supabase project URL, e.g. `https://<project-ref>.supabase.co` |
| `NEXT_PUBLIC_SUPABASE_ANON_KEY` | Browser | Supabase anon/publishable key (protected by RLS) |
| `NEXT_PUBLIC_GATEWAY_BASE_URL` | Browser | new-api gateway origin used by the server BFF (public configuration, never an AI key), e.g. `https://your-gateway.example.com` |
| `NEXT_PUBLIC_GATEWAY_CLIENT_ID` | Browser | OAuth public client id when using OAuth; proxy defaults to proxy:<AppID> |
| `GATEWAY_PUBLIC_URL` | Server | Public issuer; must equal the Supabase `custom:newapi` provider issuer |
| `APP_ORIGIN` | Server | Stable production origin used for redirects, e.g. `https://your-app.example.com` |
| `INITIAL_ADMIN_SUB` | Server | new-api user `sub` (the OAuth app creator) for the one-time first-owner grant |
| `SUPABASE_SECRET_KEY` | Server only | Supabase `service_role` key; used only by server-side/admin code, never `NEXT_PUBLIC_` |
| `GATEWAY_TOKEN_ENCRYPTION_KEY` | Server only | 32 random bytes, standard Base64; encrypts personal API keys and OAuth tokens at rest |

## Local Run

```bash
# Create .env.local from the table above first
bun install
bun run dev                  # http://localhost:3000
```

The root page redirects to `/login` (not signed in) or `/studio` (signed in).

Other scripts: `bun run build`, `bun run start`, `bun run lint`, `bun run test` (serial module mocks).

## Supabase Setup

The schema, RLS policies, and Storage buckets live in `supabase/migrations/`.

```bash
supabase login
supabase link --project-ref <project-ref>
supabase db push            # applies supabase/migrations/*.sql
```

**OAuth mode only:** auth uses Supabase with a custom OIDC provider whose identifier is exactly `custom:newapi`:

- `issuer` = `GATEWAY_PUBLIC_URL` (the gateway's public HTTPS origin).
- `client_id` = `NEXT_PUBLIC_GATEWAY_CLIENT_ID` (a **public** new-api OAuth client, PKCE mandatory, no `client_secret`).
- `scopes` = `openid profile email api:read api:write`.
- Redirect URI = `https://<project-ref>.supabase.co/auth/v1/callback`.

`scripts/bootstrap.ps1` performs this whole wiring automatically (probe login → reuse/select the OAuth app → `supabase link` → `db push` → create/verify `custom:newapi` → write `.env.local` → grant the first `app_admins` owner). Run it from the project root:

```powershell
./scripts/bootstrap.ps1 -Mode oauth -ProjectName "AI Outfit"
```

Without an existing app to reuse, pass `-ClientIdOverride <client_id>` to select one. To seed the outfit/pose asset library afterwards:

```bash
bun run scripts/seed-assets.ts --pack-root <asset-pack-dir> --client-id <client_id>
```

## new-api Gateway Preparation

1. In the new-api admin, create an **OAuth application**: public client, PKCE enabled, scopes `openid profile email api:read api:write`. Note its `client_id` (`nac_...`) and bind its `supabase_project_ref`.
2. Register the redirect URI `https://<project-ref>.supabase.co/auth/v1/callback` (and the app's `/auth/callback` origin for BFF mode if you switch).
3. Create the **App-scoped subscription plans and products** this app will sell (owned by this OAuth App's `app_id`). Balance top-up needs no product.
4. Configure the **payment provider** (e.g. epay / stripe / creem / waffo_pancake) with its notify/callback URL pointing back at the new-api gateway; the app only follows the checkout response returned by `/oauth/subscription/checkout`, `/oauth/products/checkout`, and `/oauth/topup/checkout`.

The app reads identity/quota via `GET /oauth/userinfo` and lists plans/products via `/oauth/subscription/plans` and `/oauth/products` — all scoped by the access token's `client_id`.

## Directory Structure

```text
outfit-demo/
├─ src/
│  ├─ app/
│  │  ├─ layout.tsx · globals.css · page.tsx   # root: redirect /studio or /login
│  │  ├─ login/page.tsx                         # /login (Supabase custom:newapi)
│  │  ├─ auth/callback/route.ts                 # OAuth code exchange (single-use)
│  │  ├─ (consumer)/
│  │  │  ├─ studio/page.tsx                     # /studio — AI try-on studio
│  │  │  ├─ wardrobe/page.tsx                   # /wardrobe
│  │  │  ├─ characters/page.tsx                 # /characters
│  │  │  ├─ poses/page.tsx                      # /poses
│  │  │  ├─ history/page.tsx                    # /history
│  │  │  └─ profile/page.tsx                    # /profile — catalog, payments, credits
│  │  ├─ admin/**                               # Ant Design + Refine console
│  │  └─ api/**                                 # Route Handlers (relay, checkout, media, admin)
│  ├─ components/                               # DaisyUI consumer components
│  ├─ lib/
│  │  ├─ supabase/{client,server,admin}.ts      # browser / server / service-role clients
│  │  ├─ gateway*.ts                            # gateway contract, session, BFF proxy, errors
│  │  ├─ tryon/                                 # try-on pipeline (quote, image, wear, rollback, advance)
│  │  └─ config.ts · constants.ts · types.ts
│  └─ proxy.ts                                  # Next.js 16 session-refresh middleware
├─ supabase/
│  ├─ config.toml
│  ├─ migrations/*.sql                          # schema + RLS + Storage
│  └─ fix_client_id_fn.sql
├─ scripts/
│  ├─ bootstrap.ps1                             # one-shot new-api + Supabase wiring
│  └─ seed-assets.ts                            # upload asset packs (outfits/poses)
├─ public/
└─ .env.example · .gitignore · .vercelignore · package.json · tsconfig.json · next.config.ts · bun.lock
```

## Async Try-On Generation

Generation is asynchronous and resumable. `POST /api/generate` validates, freezes the quote, persists a `pending`/`quoted` record, and returns the task id immediately — it never blocks on the image. The browser then polls `POST /api/generate/[id]/advance` every ~1.5s; each call advances the six-stage pipeline one step and persists that step's output. Because `/advance` reads the gateway access token from the request cookie, it cannot run as a server background job: the tab must stay open to poll, and closing it pauses (not fails) the task. Re-entering the studio with the same selections re-runs `advance` from the persisted state, and the create endpoint dedupes back to the original id. See `src/lib/tryon/advance.ts`.

## Deploy on Vercel

```bash
vercel login
vercel link
vercel env add   # set the env vars above (server-only ones included)
vercel --prod
```

Or connect the repo and set the environment variables in the Vercel dashboard. Update the three origins together in one change window: the production domain (`APP_ORIGIN`), the new-api OAuth app redirect URIs, and Supabase Auth (Site URL + redirect allow list). Preview deployments get a new origin each time — do not register a wildcard redirect URI.

## Security Notes

- The gateway `client_secret` is not used: this is a public client with PKCE.
- `SUPABASE_SECRET_KEY` and `GATEWAY_TOKEN_ENCRYPTION_KEY` are server-only; never prefix them with `NEXT_PUBLIC_` and never pass them to a Client Component.
- Tokens and request bodies are never logged; gateway tokens are encrypted with `GATEWAY_TOKEN_ENCRYPTION_KEY`.
- Media lives in private Supabase Storage buckets; the database stores only `bucket` + `path`.
- Balance, credits, orders, and usage come from new-api; there is no local ledger.

For the full contract (identity, relay, billing, security checklist), read the skill's `SKILL.md` and `references/standards/*.md`.
