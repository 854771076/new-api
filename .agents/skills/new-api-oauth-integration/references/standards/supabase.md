# Supabase Standard (CLI 3.x, Auth, RLS, Edge Functions, SSR)

Applies to: **Supabase CLI 3.x**, **`@supabase/ssr` 0.5+**,
**`@supabase/supabase-js` 2.x**, Supabase Postgres 15+, Next.js 15+ App Router.

Official docs: [CLI](https://supabase.com/docs/guides/cli) ·
[Migrations](https://supabase.com/docs/guides/local-development) ·
[Custom OIDC](https://supabase.com/docs/guides/auth/social-login/auth-custom-provider) ·
[RLS](https://supabase.com/docs/guides/database/postgres/row-level-security) ·
[Edge Functions](https://supabase.com/docs/guides/functions) ·
[Next.js SSR auth](https://supabase.com/docs/guides/auth/server-side/nextjs)

Repo background: `../../SKILL.md` (Supabase direct mode),
`../web/outfit-demo/supabase/schema.sql`.

## 1. CLI Workflow and Migrations

```bash
supabase login
supabase link --project-ref <project-ref>
supabase migration new add_business_tables
supabase db push
supabase db query "select count(*) from public.user_profiles"
supabase functions deploy user-sync
supabase secrets set GATEWAY_BASE_URL=https://ai.example.com
```

- **CLI 3.x flag syntax**: ref-taking options use the equals form,
  `--remote=<project-ref>`; the space-separated form is rejected.
- `supabase link` stores the ref under `supabase/.temp/`; verify it with
  `supabase projects list` before any destructive command.
- Schema changes are migrations: `migration new` → commit → `db push`. Use
  `db query` for inspection instead of ad-hoc `psql`.
- Never run `supabase db reset` against a linked remote project; it is local
  only. `supabase status` / `stop` also manage the local stack only.
- `config.toml` cannot declare a custom OIDC provider, and the Management API
  has no provider CRUD; use the Auth admin API (section 2).

Migration order matters because `LANGUAGE sql` bodies resolve table references at
`CREATE` time: tables (every business table carries `client_id text not null`) →
indexes including `(client_id, ...)` → helper functions such as
`current_app_client_id()` → `enable row level security` → policies → grants.
Seeds belong in a `security definer` function executable only by `service_role`.

## 2. Auth: new-api as a Custom OIDC Provider

- Identifier **must** start with `custom:` — use exactly `custom:newapi`.
- `issuer` **must** equal the gateway's public base URL byte for byte, without a
  trailing slash, matching `<gateway>/.well-known/openid-configuration`.
- The gateway OAuth app must be a **public client** (`public_client = true`), so
  PKCE is enforced and no secret reaches the browser.
- `pkce_enabled: true` and `email_optional: true` are required; a gateway user
  may have no email.
- The admin API requires a non-empty `client_secret` even for a public client;
  send a placeholder such as `ignored-by-public-client`.
- The issuer's discovery document must list `none` in
  `token_endpoint_auth_methods_supported`, or creation and token exchange fail.
- Register the gateway OAuth app redirect URI as
  `https://<project-ref>.supabase.co/auth/v1/callback`.
- The gateway must be publicly reachable when the provider is created: Supabase
  fetches discovery then. A cloud project cannot use `http://localhost`.

```bash
curl -X POST "https://<project-ref>.supabase.co/auth/v1/admin/custom-providers" \
  -H "apikey: $SUPABASE_SECRET_KEY" -H "Authorization: Bearer $SUPABASE_SECRET_KEY" \
  -H "Content-Type: application/json" -d '{
    "provider_type": "oidc", "identifier": "custom:newapi", "issuer": "https://ai.example.com",
    "client_id": "nac_xxxxxxxx", "client_secret": "ignored-by-public-client",
    "scopes": ["openid","profile","email","api:read","api:write"],
    "pkce_enabled": true, "email_optional": true }'
```

To rotate, delete (`DELETE .../custom-providers/custom:newapi`, prefix included)
and recreate. Flipping the gateway app to public does not update Supabase.

The client passes the full identifier:
`supabase.auth.signInWithOAuth({ provider: 'custom:newapi', options: { redirectTo: `${APP_URL}/auth/callback` } })`.
Use `session.provider_token` / `provider_refresh_token` for gateway calls.
Supabase does not refresh a custom provider's token; refresh it through
`<gateway>/oauth/token` before expiry (see `../../SKILL.md`).

## 3. Row Level Security

Every table exposed through PostgREST is readable with the anon key until RLS is
enabled. RLS is mandatory.

```sql
create table public.user_outfits (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references auth.users(id) on delete cascade,
  client_id text not null,
  name text not null
);
create index idx_user_outfits_client on public.user_outfits(client_id);

create or replace function public.current_app_client_id()
returns text language sql stable
security definer set search_path = public, pg_temp
as $$ select client_id from public.app_config where id = 1; $$;
revoke execute on function public.current_app_client_id() from public, anon;
grant execute on function public.current_app_client_id() to authenticated, service_role;

alter table public.user_outfits enable row level security;
create policy "user_outfits own rows" on public.user_outfits for all
  using (user_id = auth.uid() and client_id = public.current_app_client_id())
  with check (user_id = auth.uid() and client_id = public.current_app_client_id());
```

- `enable row level security` on **every** table in `public`, including config
  and lookup tables.
- Policies compare `client_id` to `current_app_client_id()` so several apps can
  share one project without rewriting policies.
- The helper is `security definer` with a fixed `search_path`; execute is revoked
  from `public`/`anon`.
- Never derive `client_id` from request input; take it from server state.
- Identity mapping columns (`gateway_user_id`) are written only by
  `service_role`. Expose such tables read-only:
  `revoke all on public.user_profiles from anon, authenticated; grant select ... to authenticated;`
- `service_role` bypasses RLS; never drive a service-role query from unverified
  browser input.

## 4. Edge Functions

```ts
// supabase/functions/user-sync/index.ts
const jwt = (req.headers.get('Authorization') ?? '').replace(/^Bearer /, '').trim()
if (!jwt) return new Response('unauthorized', { status: 401 })

const admin = createClient(
  Deno.env.get('SUPABASE_URL')!,
  Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!,
  { auth: { persistSession: false, autoRefreshToken: false } },
)
const { data, error } = await admin.auth.getUser(jwt)   // identity from the JWT
if (error || !data.user) return new Response('unauthorized', { status: 401 })

await admin.from('user_profiles').upsert({
  id: data.user.id,                                  // never body.user_id
  client_id: Deno.env.get('GATEWAY_CLIENT_ID')!,
})
```

- `SUPABASE_SERVICE_ROLE_KEY` lives only in the function runtime; never in
  `NEXT_PUBLIC_*`, client code, or logs.
- Derive the caller from the verified JWT; never trust `body.user_id`,
  `body.email`, or a role flag from the browser.
- When forwarding a browser token, verify its issuer and `sub` first.
- Secrets go through `supabase secrets set` and are read with `Deno.env.get`.
- Restrict CORS on mutating endpoints to the app origin, not `*`.

## 5. Storage (mandatory for all media)

Every binary asset the app produces or accepts — avatars, product images,
generated images, video, audio, PDFs, attachments, exports — MUST live in
**Supabase Storage**. There is no alternative media store in this standard.

```sql
insert into storage.buckets (id, name, public)
values ('app-media', 'app-media', false)
on conflict (id) do nothing;

create policy "app_media_read" on storage.objects
for select to authenticated
using (
  bucket_id = 'app-media'
  and (storage.foldername(name))[1] = public.current_app_client_id()
);

create policy "app_media_write" on storage.objects
for insert to authenticated
with check (
  bucket_id = 'app-media'
  and (storage.foldername(name))[1] = public.current_app_client_id()
  and (storage.foldername(name))[2] = auth.uid()::text
);
```

Rules:

- Path convention: `{client_id}/{user_id}/{uuid}.{ext}`. Store `bucket` and
  `path` in Postgres; never store signed URLs (they expire) or the bytes.
- Keep buckets private unless the asset is genuinely public. Read private
  objects through short-lived `createSignedUrl` links generated on the server.
- RLS on `storage.objects` is the authorization point; a private bucket alone is
  not access control.
- Validate MIME type and size **server-side** before upload (an accepted file
  extension is not a guarantee), and strip or re-encode untrusted images.
- Uploads from a Server Action, Route Handler, or Edge Function use the user's
  session when possible and the privileged client only when the operation
  genuinely needs it.
- Delete or replace the object when its database row is deleted; do not leave
  orphans.
- Supabase Storage limits and egress still apply; upload directly with
  `supabase.storage.from(...).upload()` or a signed upload URL instead of
  proxying large files through your own server.

Never:

- base64 or binary blobs in Postgres (`bytea`, large `text`);
- writes to the Vercel/Next.js filesystem (`public/`, `/tmp`, `fs.writeFile` in a
  serverless function) for anything that must survive the request;
- third-party image hosts, object storage, or CDNs for user content;
- permanent public URLs for private assets.

## 6. `@supabase/ssr` with Next.js

```ts
// middleware.ts — refreshes cookies for Server Components
const supabase = createServerClient(URL, ANON_KEY, {
  cookies: {
    getAll: () => request.cookies.getAll(),
    setAll: (items) => {
      items.forEach(({ name, value }) => request.cookies.set(name, value))
      items.forEach(({ name, value, options }) => response.cookies.set(name, value, options))
    },
  },
})
const { data } = await supabase.auth.getUser()   // verifies with the Auth server
if (!data.user && request.nextUrl.pathname.startsWith('/dashboard')) {
  return NextResponse.redirect(new URL('/login', request.url))
}
export const config = {
  matcher: ['/((?!_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|css|js)$).*)'],
}
```

`createServerSupabase()` in `lib/supabase/server.ts` mirrors this with
`cookies()` from `next/headers`, wrapping `set` in `try/catch` because Server
Component renders cannot set cookies (middleware does the refresh).

- Browser uses `createBrowserClient`; server uses `createServerClient`; never parse cookies by hand.
- Implement `getAll`/`setAll`; the singular `get`/`set`/`remove` API is legacy.
- Authorize with `auth.getUser()`, which validates the JWT with the Auth server.
  `getSession()` only reads a cookie and is not an authorization check.
- Never wrap `service_role` in `createServerClient`; use a separate admin client
  in trusted server code and Edge Functions.

## 7. Anti-Patterns and Checklist

| Anti-pattern | Consequence | Do instead |
|--------------|-------------|------------|
| RLS off on one table | Anon key reads/writes everything | Enable RLS in the migration |
| `client_id` from the request | Cross-app data leak | Derive via the helper |
| Trusting `body.user_id` | Account takeover | Verify the JWT, use `user.id` |
| Issuer with slash or `localhost` | Creation / exchange fails | Exact public HTTPS origin |
| Service-role key in browser env | Full database compromise | Server / Edge Function only |
| Uploads as base64 / on local disk | Bloat, lost files, no access control | Supabase Storage private bucket |

- [ ] Schema changes are committed migrations applied with `db push`.
- [ ] Provider uses `custom:newapi`, an exact public HTTPS issuer, PKCE, and
      `email_optional: true`.
- [ ] Every table has RLS, `client_id`, and indexed policy columns.
- [ ] Edge Functions verify the JWT and ignore identity fields in the body.
- [ ] Service-role key never reaches a client bundle or log.
- [ ] Middleware refreshes sessions; protected routes call `getUser()`.
- [ ] Every media asset is in a Supabase Storage bucket; the database stores
      only `bucket` + `path`.
- [ ] Private buckets are read through short-lived signed URLs, and
      `storage.objects` policies enforce `client_id` and owner.
- [ ] Upload MIME type and size are validated server-side.
