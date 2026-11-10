# Admin Dashboard Standard (Next.js + Ant Design + Refine + Supabase)

Applies to: **internal admin / back-office consoles** for apps built on this
standard — Next.js 15+ (App Router), React 19, Ant Design 5, `@refinedev/core`
5.x, `@supabase/ssr` 0.5+.

Official docs: [Ant Design](https://ant.design/components/overview) ·
[Ant Design + Next.js](https://ant.design/docs/react/use-with-next) ·
[Refine core](https://refine.dev/docs/core/) ·
[Refine Supabase](https://refine.dev/docs/data/packages/supabase/) ·
[Supabase SSR](https://supabase.com/docs/guides/auth/server-side/nextjs)

## 1. Application Shape

An admin console is a **separate surface** from the consumer app, even when it
ships in the same repository.

| Decision | Recommendation |
|----------|----------------|
| Separate deployment | Preferred for anything with real customer data |
| Same deployment | Use a route group `app/(admin)/` with its **own root layout** |
| Styling | Admin uses Ant Design; consumer app uses Tailwind + DaisyUI. Do not render both in one route tree: Tailwind preflight and antd reset fight over base elements, buttons, and inputs. |
| Access | The admin origin must not be linked from the public app; protect it with network policy, SSO, or both. |

Directory shape:

```text
app/
├── (site)/                 # consumer app (Tailwind + DaisyUI layout)
│   ├── layout.tsx
│   └── ...
└── (admin)/                # admin console (Ant Design layout)
    ├── layout.tsx          # server guard + antd Sider/Header
    ├── dashboard/page.tsx
    ├── users/page.tsx
    └── audit/page.tsx
app/api/admin/**/route.ts   # the only data boundary for the admin UI
lib/supabase/{client,server,admin}.ts
```

## 2. Install

```bash
bun add antd @ant-design/icons @ant-design/nextjs-registry @refinedev/core zod
```

`@refinedev/core` is headless: you keep Ant Design as the only UI dependency and
write the thin glue yourself. Add `@refinedev/antd` only if you accept its
pinned antd version and opinionated layout; do not mix both approaches in one
console.

## 3. Identity: What You Can and Cannot Get

After OAuth authorization the console has these identity sources:

| Source | Fields | Notes |
|--------|--------|-------|
| `GET /oauth/userinfo` (access token) | `sub` always; `username`, `name` with `profile`; `email` with `email` | Verified live: returns `{"sub":"1","username":"...","name":"...","email":""}`. `email` may be empty. |
| `id_token` | `sub`, `auth_time`, `nonce`, `iss`, `aud`, `exp`, `nbf`, `iat`, `jti` | **No** `name`, `email`, or role claims. Do not expect profile data here. |
| Supabase session (`identity_data`) | provider `sub` (string) mapped into `user.identities[]` | `name`/`email` are not guaranteed to be populated; sync profile data explicitly instead of reading it from the session |
| `/api/user/self` with dashboard session or PAT | `id`, `username`, `display_name`, `role`, `status`, `email`, `group`, `quota`, `used_quota`, `permissions` | new-api role: `1` user, `10` admin, `100` root. |

Rules:

- Key the admin account on `sub` (numeric string), never on `username` or
  `email`.
- The **app's** admin role is not the new-api role. Store it in
  `auth.users.app_metadata.admin_role` (service_role only) or in an
  `app_admins` table; never trust `user_metadata`, which the user can edit.
- If you need to reflect the new-api role, read it server-side with an admin
  session or admin PAT and store the derived permission, not the raw token.
- Never pass a role from the browser (query string, form field, request body) to
  a privileged query.

### 3.1 Bootstrapping the first administrator

Nobody has `admin_role` yet, and the console cannot grant it to itself. The first
administrator is therefore created **once, out of band, by an operator** — never
by a public endpoint, and never by "the first user to sign up becomes admin".

**Default owner: the developer who created the OAuth app.** new-api stores that
person's user id in `oauth_apps.created_by` (see `controller/oauth_app.go`), and
that id is the `sub` they receive after login. Capture it once at provisioning:

```bash
# Dashboard admin session (PATs are not accepted on this route):
GET /api/oauth-apps/<app-id>        # response contains created_by
# or query the new-api database:
#   select created_by from oauth_apps where client_id = 'nac_...';
# or simply read the developer's `sub` from GET /oauth/userinfo after login.
```

Set it as `INITIAL_ADMIN_SUB` in the deployment environment and use one of:

- seed `app_admins` with that `sub` in the first migration (preferred), or
- let the server perform an **idempotent first-run grant** when `app_admins` has
  no `owner` **and** the verified `sub` from `getUser()` equals
  `INITIAL_ADMIN_SUB`.

The second option is safe because the `sub` is fixed by the developer, not by
whoever arrives first. If `created_by` is `0` (legacy app), grant the first owner
manually with the SQL below.

Prerequisite: that person completes one OAuth login so a row exists in
`auth.users` and `auth.identities` with the provider `sub`.

Preferred: store administrators in your own table.

```sql
-- app_admins: user_id primary key, gateway_sub text unique, admin_role text,
-- granted_by text, created_at timestamptz default now()
insert into public.app_admins (user_id, gateway_sub, admin_role, granted_by)
select i.user_id, i.identity_data->>'sub', 'owner', 'bootstrap'
from auth.identities i
where i.provider = 'custom:newapi'
  and i.identity_data->>'sub' = '<gateway-sub>'
on conflict (user_id) do update set admin_role = 'owner';
```

Alternative: write `app_metadata` (only `service_role` can do this).

```sql
update auth.users u
set raw_app_meta_data =
      coalesce(u.raw_app_meta_data, '{}'::jsonb)
      || jsonb_build_object('admin_role', 'owner')
where u.id = (
  select i.user_id
  from auth.identities i
  where i.provider = 'custom:newapi'
    and i.identity_data->>'sub' = '<gateway-sub>'
  limit 1
);
```

Run it with `supabase db query "<sql>"` against the linked project (or commit it
as a one-off migration). Direct SQL is acceptable for the one-time bootstrap;
for grants made at runtime, call the Auth Admin API
(`auth.admin.updateUserById(id, { app_metadata })`) instead. Then:

1. The bootstrapped user signs out and in again (or calls
   `supabase.auth.refreshSession()`) so the new claim is in the session.
2. Server code reads the role with `supabase.auth.getUser()` — it hits the Auth
   server, so it sees the change immediately; `getSession()` does not.
3. Verify `GET /api/admin/me` returns the identity for that user and `403` for a
   normal user.
4. Record who granted it, when, and why in the audit log or the change record.

Rules for every later grant:

- Only an `owner` may promote or demote through the admin UI; the Route Handler
  writes `app_metadata`/`app_admins` with the privileged client after
  `requireAdmin()`.
- No self-promotion: an admin cannot raise their own role, and the last `owner`
  cannot be demoted or deleted.
- If you use an env allowlist for automation, key it on `sub` (not email, which
  may be empty) and remove it once the first owner exists.
- Never derive admin status from `username`, `email`, `user_metadata`, or a
  request field.

## 4. Supabase Server and Client Split

| Client | File | Key | Used by |
|--------|------|-----|---------|
| Browser | `lib/supabase/client.ts` (`createBrowserClient`) | anon/publishable | Client Components: forms, tables, realtime |
| Server (user) | `lib/supabase/server.ts` (`createServerClient` + cookies) | anon/publishable | Server Components, Route Handlers acting as the user |
| Server (privileged) | `lib/supabase/admin.ts` (`createClient` with `SUPABASE_SECRET_KEY`) | secret / service_role | Admin API calls, provisioning, Edge Function invocation |

Requirements:

- `lib/supabase/admin.ts` starts with `import 'server-only';`. It must never be
  imported by a Client Component or a shared module.
- Privileged reads happen in Route Handlers (`app/api/admin/**`) with an
  explicit guard; the browser never receives `SUPABASE_SECRET_KEY`.
- RLS stays enabled on every table even if the console only uses the privileged
  client; the privileged client bypasses RLS, so every query must filter by the
  intended tenant/app (`client_id`) explicitly.
- Admin RLS helper (if the browser client also reads admin data):

```sql
-- security definer so the policy can read auth.users.app_metadata
create or replace function public.is_app_admin()
returns boolean
language sql
stable
security definer
set search_path = public
as $$
  select coalesce(
    (select (u.app_metadata->>'admin_role') in ('admin', 'owner')
       from auth.users u where u.id = auth.uid()),
    false
  );
$$;

revoke execute on function public.is_app_admin() from public, anon;
grant execute on function public.is_app_admin() to authenticated;
```

## 5. Server-Side Guard

Every admin Route Handler and the admin layout repeat the same cheap check:

```ts
// app/api/admin/guard.ts
import 'server-only';
import { createServerClient } from '@supabase/ssr';
import { cookies } from 'next/headers';

export async function requireAdmin() {
  const supabase = createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
    {
      cookies: {
        getAll: async () => (await cookies()).getAll(),
        setAll: () => {},
      },
    },
  );

  const { data } = await supabase.auth.getUser();
  const role = data.user?.app_metadata?.admin_role;
  if (!data.user || (role !== 'admin' && role !== 'owner')) {
    throw new Response('forbidden', { status: 403 });
  }
  return data.user;
}
```

The admin `layout.tsx` calls the same logic and redirects to `/login` instead of
throwing. Client Components may read the role to hide controls, but that is UX
only — the Route Handler guard is the enforcement point.

## 6. Refine Integration (`@refinedev/core`)

Refine supplies data, auth, access control, notification, and table/form state.
Keep the network boundary in Next.js Route Handlers.

```ts
// components/refine/data-provider.ts
import type { DataProvider } from '@refinedev/core';

async function request(path: string, init?: RequestInit) {
  const res = await fetch(`/api/admin${path}`, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
    credentials: 'same-origin',
  });
  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).message ?? res.statusText);
  return res.json();
}

export const dataProvider: DataProvider = {
  getList: async ({ resource, pagination, sorters, filters }) => {
    const params = new URLSearchParams();
    const { current = 1, pageSize = 20 } = pagination ?? {};
    params.set('page', String(current));
    params.set('page_size', String(pageSize));
    for (const sorter of sorters ?? []) params.set('sort', `${sorter.field}:${sorter.order}`);
    for (const filter of filters ?? []) params.append('filter', `${filter.field}:${filter.value}`);
    const json = await request(`/${resource}?${params}`);
    return { data: json.data, total: json.total };
  },
  getOne: async ({ resource, id }) => ({ data: (await request(`/${resource}/${id}`)).data }),
  create: async ({ resource, variables }) => ({
    data: (await request(`/${resource}`, { method: 'POST', body: JSON.stringify(variables) })).data,
  }),
  update: async ({ resource, id, variables }) => ({
    data: (await request(`/${resource}/${id}`, { method: 'PATCH', body: JSON.stringify(variables) })).data,
  }),
  deleteOne: async ({ resource, id }) => ({
    data: (await request(`/${resource}/${id}`, { method: 'DELETE' })).data,
  }),
  custom: async ({ url, method, payload, query, headers }) => {
    const queryString = query ? `?${new URLSearchParams(query as Record<string, string>)}` : '';
    return {
      data: await request(`${url ?? ''}${queryString}`, {
        method: method.toUpperCase(),
        body: payload ? JSON.stringify(payload) : undefined,
        headers,
      }),
    };
  },
  getApiUrl: () => '/api/admin',
};
```

Wiring rules:

- `authProvider.getIdentity` returns the server-verified identity from
  `/api/admin/me`; `login`/`logout` delegate to Supabase Auth.
- `accessControlProvider.can` maps `admin_role` to resource/action pairs
  (`users:list`, `users:delete`, `audit:list`). It mirrors the server guard; it
  does not replace it.
- `notificationProvider` maps Refine notifications to antd `message` /
  `notification`.
- Use the Next.js router provider (or a small custom one) so Refine URLs map to
  `app/(admin)/**`.
- Map `useTable` to antd `Table` with `manualPagination`, `manualSorting`,
  `manualFiltering`; never paginate in the browser over a full table.

## 7. Ant Design in the App Router

```tsx
// app/(admin)/layout.tsx
import { AntdRegistry } from '@ant-design/nextjs-registry';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return (
    <AntdRegistry>
      <ConfigProvider locale={zhCN} theme={{ token: { borderRadius: 8 } }}>
        {children}
      </ConfigProvider>
    </AntdRegistry>
  );
}
```

- `AntdRegistry` prevents style flash on first paint; add it once per admin root
  layout, not per page.
- Theme through `ConfigProvider` tokens, not global CSS overrides.
- Tables: stable `rowKey`, server-side pagination (`page`, `page_size`),
  whitelisted sort/filter fields, explicit `columns` (no `SELECT *` passthrough).
- Forms: antd `Form` for layout, shared Zod schema for validation on the client,
  and the same schema again in the Route Handler. Client validation is UX only.
- Dates: antd v5 uses dayjs; set `dayjs/locale/zh-cn` in the admin root.
- Large datasets: virtual table (`virtual` + `scroll`) or server-side export;
  never ship more than a page or two of rows to the browser.

## 8. Data, Audit, and Safety

- Every admin Route Handler: guard → validate input → query with an explicit
  `client_id`/tenant filter → write audit row → respond.
- Audit table columns: `actor_id`, `actor_role`, `action`, `resource`,
  `resource_id`, `ip`, `user_agent`, `created_at`; never store tokens or
  passwords.
- Destructive actions require a confirmation dialog that repeats the target
  name; never rely on the button label alone.
- Exports (CSV/Excel) are generated server-side after the same guard and are
  rate limited.
- Errors returned to the UI are stable messages; stack traces and SQL errors
  stay in server logs.
- PII: return display name and email only where the screen needs them; mask
  secrets (`sk-...`, `nap_...`, `nac_...secret`) before rendering.

### 8.1 All media assets go to Supabase Storage

This is mandatory, not a default: **every** binary asset — avatars, product
images, generated images, video, audio, PDFs, attachments, and exports — is
stored in **Supabase Storage**. Nothing else is an acceptable media store.

- Buckets are **private** unless the asset is genuinely public (brand logo,
  marketing image). Private objects are served with short-lived signed URLs
  (`createSignedUrl`), never a permanent public link.
- Object path: `{client_id}/{user_id}/{uuid}.{ext}`. The database stores
  `bucket` and `path` only, never a signed URL (they expire) and never the bytes.
- RLS on `storage.objects` keys on the first path segment (`client_id`) and the
  second (`auth.uid()`). A private bucket is not authorization by itself.
- Admin uploads go through `POST /api/admin/uploads` behind `requireAdmin()`:
  validate MIME type and size server-side, upload with the privileged client,
  then return the path. The browser never receives `SUPABASE_SECRET_KEY`.
- Delete the object when its row is deleted, and reuse/replace instead of
  accumulating orphans.

Forbidden, with no exception:

- Base64 or binary blobs in Postgres (`bytea`/`text` columns).
- Writing uploads to the Vercel/Next.js filesystem — it is ephemeral and
  read-only in production.
- Third-party image hosts, object storage, or CDNs for user content.
- Serving user content directly from `public/` or a permanent public bucket URL.

## 9. Checklist

- [ ] Admin UI and consumer UI live in separate route trees (or separate apps) with only one styling system each.
- [ ] `lib/supabase/admin.ts` has `import 'server-only'` and is never referenced by client code.
- [ ] Role comes from `app_metadata`/`app_admins`, never `user_metadata` or request input.
- [ ] The first `owner` is the OAuth app developer (`oauth_apps.created_by` → `INITIAL_ADMIN_SUB`), granted out of band after one OAuth login, and the grant is recorded.
- [ ] Only an `owner` can change admin roles; self-promotion and removing the last owner are blocked.
- [ ] Every admin Route Handler calls the guard before touching data.
- [ ] Refine `dataProvider` targets `/api/admin/**`; the browser never queries privileged tables directly.
- [ ] `accessControlProvider` mirrors, but does not replace, the server guard.
- [ ] antd `Table` uses server-side pagination/sorting/filtering and a stable `rowKey`.
- [ ] Audit row written for create/update/delete and privileged reads.
- [ ] Exports are server-generated after the guard and rate limited.
- [ ] `AntdRegistry` and `ConfigProvider` are mounted once in the admin root layout.
- [ ] All media assets are in Supabase Storage private buckets; the database stores only `bucket` + `path`.
- [ ] Media reads use short-lived signed URLs, and `storage.objects` policies enforce `client_id` and owner.

## 10. Anti-Patterns

- Rendering antd and DaisyUI components in the same page and patching conflicts with `!important`.
- Doing admin authorization in `proxy.ts`/middleware only; middleware is not an
  authorization boundary for data access.
- Calling Supabase with the service_role key from a Client Component, or
  exposing it through `NEXT_PUBLIC_*`.
- Using `user_metadata.admin_role` because it is convenient.
- Granting the first admin from an API route, a seed on first request, or "the
  first user to sign up".
- Storing uploads as base64 in Postgres, on the Vercel filesystem, or on a
  third-party host instead of Supabase Storage.
- Loading the whole table and paginating with `useMemo`.
- Letting Refine call Supabase directly from the browser for admin resources
  while RLS has no admin policy, then "fixing" it by disabling RLS.
