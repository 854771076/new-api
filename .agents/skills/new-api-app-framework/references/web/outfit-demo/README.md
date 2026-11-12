# AI Outfit — AI Try-On Reference Demo

A full-stack reference application for the **new-api-app-framework** skill. It demonstrates the complete chain the skill standardizes: new-api as the OIDC identity provider and AI gateway, Supabase for auth/data/Storage, and Vercel for deployment.

Users sign in with their new-api account, upload a character photo and garments, and generate AI try-on images through the gateway. The app ships with a consumer surface (wardrobe, characters, poses, history, profile) and an optional Ant Design admin console for catalog and plan management. Billing is App-scoped: plans, products, and top-up are read and purchased through the gateway's `/oauth/*` endpoints.

## Tech Stack

| Layer | Choice |
|-------|--------|
| Framework | Next.js (App Router) + TypeScript strict |
| UI runtime | React 19 |
| Consumer styling | Tailwind CSS v4 + DaisyUI 5 |
| Admin console | Ant Design 5 + `@refinedev/core` |
| Backend | Supabase (Auth, Postgres + RLS, Storage) |
| Identity / AI gateway | new-api OAuth 2.0 / OIDC (public client + PKCE) |
| Deployment | Vercel |
| Package manager | bun |

## Requirements

- A new-api instance reachable over HTTPS.
- Supabase CLI (`supabase login`) and a Supabase project.
- `bun`.
- Optional: `newapi-cli` (bundled with the `newapi-cli` skill) for the one-shot bootstrap.

## Environment Variables

Copy `.env.example` to `.env.local` and fill in the values. `.env*` is gitignored; never commit real secrets.

| Variable | Exposure | Purpose |
|----------|----------|---------|
| `NEXT_PUBLIC_SUPABASE_URL` | Browser | Supabase project URL, e.g. `https://<project-ref>.supabase.co` |
| `NEXT_PUBLIC_SUPABASE_ANON_KEY` | Browser | Supabase anon/publishable key (protected by RLS) |
| `NEXT_PUBLIC_GATEWAY_BASE_URL` | Browser | new-api gateway origin used by the browser relay, e.g. `https://your-gateway.example.com` |
| `NEXT_PUBLIC_GATEWAY_CLIENT_ID` | Browser | new-api OAuth public client id (`nac_...`); safe to expose |
| `GATEWAY_PUBLIC_URL` | Server | Public issuer; must equal the Supabase `custom:newapi` provider issuer |
| `APP_ORIGIN` | Server | Stable production origin used for redirects, e.g. `https://your-app.example.com` |
| `INITIAL_ADMIN_SUB` | Server | new-api user `sub` (the OAuth app creator) for the one-time first-owner grant |
| `SUPABASE_SECRET_KEY` | Server only | Supabase `service_role` key; used only by server-side/admin code, never `NEXT_PUBLIC_` |
| `GATEWAY_TOKEN_ENCRYPTION_KEY` | Server only | 32 random bytes, standard Base64; encrypts gateway session tokens at rest |

## Local Run

```bash
cp .env.example .env.local   # then fill in the values
bun install
bun run dev                  # http://localhost:3000
```

The root page redirects to `/login` (not signed in) or `/studio` (signed in).

Other scripts: `bun run build`, `bun run start`, `bun run lint`, `bun test`.

## Supabase Setup

The schema, RLS policies, and Storage buckets live in `supabase/migrations/`.

```bash
supabase login
supabase link --project-ref <project-ref>
supabase db push            # applies supabase/migrations/*.sql
```

Auth uses Supabase with a custom OIDC provider whose identifier is exactly `custom:newapi`:

- `issuer` = `GATEWAY_PUBLIC_URL` (the gateway's public HTTPS origin).
- `client_id` = `NEXT_PUBLIC_GATEWAY_CLIENT_ID` (a **public** new-api OAuth client, PKCE mandatory, no `client_secret`).
- `scopes` = `openid profile email api:read api:write`.
- Redirect URI = `https://<project-ref>.supabase.co/auth/v1/callback`.

`scripts/bootstrap.ps1` performs this whole wiring automatically (probe login → reuse/select the OAuth app → `supabase link` → `db push` → create/verify `custom:newapi` → write `.env.local` → grant the first `app_admins` owner). Run it from the project root:

```powershell
./scripts/bootstrap.ps1 -ProjectName "AI Outfit"
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
