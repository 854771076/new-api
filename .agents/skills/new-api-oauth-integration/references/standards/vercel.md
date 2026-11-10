# Vercel Deployment Standard

Applies to: **Vercel CLI 40+ / current**, a Next.js 15+ App Router project, and
Supabase as the backend. All commands run from the app root unless stated.

Official documentation:

- Vercel CLI: https://vercel.com/docs/cli
- `vercel env`: https://vercel.com/docs/cli/env
- `vercel deploy`: https://vercel.com/docs/cli/deploy
- `vercel promote` / `rollback`: https://vercel.com/docs/cli/promote
- Environment variables: https://vercel.com/docs/environment-variables
- Preview deployments: https://vercel.com/docs/deployments/preview-deployments

## 1. Core CLI Commands

| Task | Command |
|------|---------|
| Authenticate | `vercel login` |
| Bind the folder to a project | `vercel link` |
| Local dev with project env | `vercel dev` |
| Preview deploy | `vercel` |
| Production deploy | `vercel --prod` |
| List deployments | `vercel ls` |
| Inspect a deployment | `vercel inspect <deployment-url>` |
| Runtime/build logs | `vercel logs <deployment-url>` (add `--follow`) |
| Pull cloud env to a file | `vercel env pull .env.local` |
| Add / list / remove env | `vercel env add <KEY>` / `vercel env ls` / `vercel env rm <KEY>` |
| Promote a preview to production | `vercel promote <deployment-url>` |
| Roll back production | `vercel rollback <deployment-url>` |
| Open the project | `vercel open` |

Rules:

- `vercel link` writes `.vercel/project.json`. Commit it if the team shares one
  project; never commit `.vercel/` if it contains personal tokens. Prefer adding
  `.vercel` to `.gitignore`.
- `vercel` (no flag) produces a **preview** URL. Treat previews as disposable;
  the hostname is unique per deployment.
- Use `vercel promote` only after the preview passed the smoke test. Production
  should be a known-good preview, not a fresh unverified build.
- `vercel logs` shows runtime logs, not build logs. Build failures are surfaced
  by `vercel inspect` or the dashboard build output.
- Do not run `vercel --prod` on a dirty working tree; the CLI uploads local
  files, not a git ref, when the project is not git-linked.

## 2. Environment Variables: Preview vs Production

Vercel has three scopes: **Production**, **Preview**, **Development**
(`.env.local` pulled by the CLI).

```bash
vercel env add NEXT_PUBLIC_APP_URL production
vercel env add NEXT_PUBLIC_APP_URL preview
vercel env pull .env.local                          # Development scope
vercel env pull .env.preview --environment=preview  # Preview scope
```

Rules:

- Values differ per scope. A preview URL is not the production URL; if the app
  embeds its own origin in a redirect, each scope needs its own value.
- `NEXT_PUBLIC_*` values are inlined at **build** time. After changing one, the
  project must be rebuilt and redeployed; restarting does not help.
- Server-only values (`GATEWAY_CLIENT_SECRET`, `SUPABASE_SERVICE_ROLE_KEY`,
  `SESSION_SECRET`) must not be exposed to the client. Never prefix them with
  `NEXT_PUBLIC_`.
- `vercel env pull` overwrites the target file. Do not keep manual edits in
  `.env.local` that you cannot regenerate.
- Vercel's "Sensitive" variables cannot be read back after creation. Keep the
  source of truth in a password manager.
- Preview deployments may be protected by Vercel Authentication; an external
  OAuth provider cannot always reach them. Test the payment/OAuth callback on a
  production or unprotected preview URL.

## 3. The Three Addresses That Must Agree

After every new deployment URL, three independent systems hold a copy of your
origins. Miss one and login or callback silently fails.

| # | Location | What to configure |
|---|----------|-------------------|
| 1 | new-api dashboard → OAuth Apps → your app → Redirect URIs | Exact callback URL. Supabase direct mode: `https://<project-ref>.supabase.co/auth/v1/callback`. BFF mode: `https://<your-app>/auth/callback`. Also set Post-Logout Redirect URIs. |
| 2 | Supabase → Auth → URL Configuration → Redirect URLs | Every app origin allowed to receive a session: production URL, stable preview URL, `http://localhost:3000`. |
| 3 | Vercel env (all three scopes) | `NEXT_PUBLIC_APP_URL`, `NEXT_PUBLIC_SUPABASE_URL`, `NEXT_PUBLIC_SUPABASE_ANON_KEY`, and server-only `GATEWAY_BASE_URL`, `GATEWAY_CLIENT_ID`, `GATEWAY_CLIENT_SECRET` (confidential mode) |

Rules:

- URLs must match exactly: scheme, host, port, path, and no trailing slash
  unless registered with one. `http` and `https` are different URIs.
- Prefer a stable production domain (custom domain) over the generated
  `<project>.vercel.app` URL, and register only that plus localhost.
- Do not add `*` wildcards to Supabase redirect URLs to "make previews work".
  Use a fixed staging domain or accept manual login testing on production.
- Changing the gateway's public server address invalidates the Supabase custom
  provider issuer. Update the provider (delete + recreate) in the same change.
- Keep a single source of truth: record the three values in the app README or
  `.env.example` comments so the next developer updates all three.

## 4. Order of Operations When Supabase Is Involved

Deployment, Supabase, and the gateway are mutually dependent. Use this order:

1. `vercel link` and set all environment variables for Production and Preview.
2. `supabase link --project-ref <ref>` and `supabase db push` (schema + RLS).
3. `supabase functions deploy <fn>` and `supabase secrets set ...` for each
   function (`GATEWAY_BASE_URL`, `GATEWAY_CLIENT_ID`, `APP_ORIGIN`).
4. Set the Supabase Auth **Site URL** and **Redirect URLs** to the deployed
   origin(s).
5. Create the new-api OAuth app with the Supabase callback
   `https://<project-ref>.supabase.co/auth/v1/callback`, public client, scopes.
6. Create or recreate the Supabase `custom:newapi` provider with
   `issuer = <gateway public URL>`.
7. Add the final production origin to the app env and redeploy if any
   `NEXT_PUBLIC_*` value changed.
8. Smoke test: login → callback → RLS read → gateway `/v1/models` → logout.

Why this order: Supabase fetches the gateway discovery document when the provider
is created, and the gateway validates the redirect URI when the user authorizes.
A missing step at 4–6 produces a redirect that never comes back.

## 5. Troubleshooting

### Blank 404 from Vercel

- **Wrong Root Directory**: the project root must be the folder containing
  `package.json` and `app/`. Set it in Project Settings → General, or run
  `vercel link` from that folder.
- **Framework preset mismatch**: must be Next.js, not "Other". With "Other",
  Vercel serves files without the Next.js runtime and every route 404s.
- **Missing build output**: check `vercel inspect <url>` for the build log. A
  failed build usually still shows the previous deployment, not a 404.
- **Route does not exist**: confirm the file is `app/<segment>/page.tsx` and not
  `app/<segment>/index.tsx` (Pages Router convention).
- **Middleware redirect loop**: a matcher that also matches `/login` or static
  assets makes the app appear as 404/redirect. Exclude `_next` and assets.

### `redirect_uri_mismatch` / `invalid redirect_uri`

- The URI sent in `redirectTo` / `redirect_uri` is not byte-equal to a
  registered one. Check scheme, trailing slash, `www`, and the custom domain.
- Supabase direct mode: Supabase sends its own callback
  (`https://<project-ref>.supabase.co/auth/v1/callback`) to the gateway, not your
  app URL. Register that URI on the gateway and your app URL in Supabase.
- BFF mode: the gateway redirect URI is your app callback, and that same URI must
  be sent in the token exchange.
- After changing a redirect URI, retry from a fresh tab: the browser may have
  cached the old authorize redirect.

### Login returns to `/login` immediately

- Supabase **Site URL** still points at `http://localhost:3000`.
- The redirect URL is not in the allow-list, so Supabase falls back to the Site
  URL.
- Cookies were set on a different host than the one serving the page (custom
  domain vs `vercel.app`). Keep one origin.
- The gateway `SESSION_SECRET` changed between requests, invalidating the
  authorize cookie.

### `provider is not enabled` / `custom provider not found`

- Provider identifier must be `custom:newapi` on both the Auth admin API call and
  the client `signInWithOAuth({ provider })`.
- The provider was created against a different issuer than the gateway's current
  public address. Delete and recreate it.
- Provider creation silently failed because discovery was unreachable.

### `401` from the gateway after about an hour

- The gateway access token expired. Supabase does not refresh a custom
  provider's token; the app must refresh it with the stored
  `provider_refresh_token` and replace the rotated value.

## 6. Checklist

- [ ] `vercel link` points at the intended project and root directory.
- [ ] Production, Preview, and Development env scopes are all populated.
- [ ] Changing a `NEXT_PUBLIC_*` value was followed by a redeploy.
- [ ] OAuth app redirect URI, Supabase redirect allow-list, and app env all use
      the same production origin.
- [ ] Functions are deployed and their secrets set before the provider is
      created.
- [ ] Preview deploy passed login, RLS read, and one gateway call before
      `vercel promote`.
- [ ] No secret is prefixed `NEXT_PUBLIC_`, and `.env.local` is git-ignored.
