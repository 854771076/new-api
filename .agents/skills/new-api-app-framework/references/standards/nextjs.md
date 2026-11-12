# Next.js App Router Standard

Applies to: **Next.js 15+ (App Router), React 19, TypeScript 5.x with
`strict: true`**, deployed on Vercel with Tailwind CSS v4.

Official docs: [App Router](https://nextjs.org/docs/app) ·
[Routing](https://nextjs.org/docs/app/building-your-application/routing) ·
[Server Components](https://nextjs.org/docs/app/building-your-application/rendering/server-components) ·
[Data fetching](https://nextjs.org/docs/app/building-your-application/data-fetching) ·
[Route Handlers](https://nextjs.org/docs/app/building-your-application/routing/route-handlers) ·
[Env vars](https://nextjs.org/docs/app/building-your-application/configuring/environment-variables)

Vendored background: `../vendor/nextjs-react-typescript/SKILL.md`,
`../vendor/vercel-react-best-practices/`.

## 1. Directory Layout

```
app/
  layout.tsx                 # root: <html>, <body>, fonts, data-theme
  page.tsx                   # "/"
  globals.css                # Tailwind + DaisyUI entry
  (marketing)/pricing/page.tsx   # route group: shared layout, no URL segment
  (app)/layout.tsx           # authenticated shell
  (app)/dashboard/page.tsx   # "/dashboard"
  auth/callback/route.ts     # OAuth callback
  api/gateway/[...path]/route.ts
  _components/               # private folder: not routable
  _lib/                      # private folder: server helpers
```

- One `page.tsx` per URL; a folder without it is not a route.
- Route groups `(name)` add layouts, not URL segments. The same path in two
  groups fails the build.
- Special files only: `layout`, `page`, `loading`, `error`, `not-found`,
  `route`, `template`, `default`. Anything else is a normal module.
- Prefix `_` for folders that must stay out of routing.
- Keep `app/` thin: route files compose features; server logic lives in `lib/`.
- `route.ts` and `page.tsx` cannot share one segment path.

## 2. Server vs Client Components

Server Components are the default; add `'use client'` only for state, effects,
event handlers, or browser APIs, and only at the leaf that needs it.

```tsx
// app/(app)/dashboard/page.tsx — server component
export default async function DashboardPage() {
  const rows = await listGenerations()
  return (
    <section className="card bg-base-100">
      <div className="card-body">
        <h1 className="card-title">History</h1>
        <ul>{rows.map((row) => <li key={row.id}>{row.model}</li>)}</ul>
        <GenerateButton />   {/* 'use client' leaf with the click handler */}
      </div>
    </section>
  )
}
```

- `'use client'` covers the whole import subtree. Never mark a `layout.tsx` or a
  data-loading `page.tsx` client-side without need.
- Server → client props must be serializable: no functions, class instances,
  driver objects, or Supabase clients.
- Add `import 'server-only'` to modules holding secrets or the service-role
  client, so the bundler fails loudly on a client import.
- Client components still receive server HTML; do not re-fetch initial data.

## 3. Data Fetching and Caching

```ts
import { cache } from 'react'
export const getProfile = cache(async (userId: string) => {
  const supabase = await createServerSupabase()
  const { data, error } = await supabase
    .from('user_profiles').select('display_name').eq('id', userId).single()
  if (error) throw error
  return data
})
```

- Read in `async` Server Components. Parallelize independent reads with
  `Promise.all`; chained `await` is a waterfall.
- Deduplicate per request with `React.cache()`. Never add module-level mutable
  request state; it leaks between users.
- Be explicit about caching. User-scoped data needs `cache: 'no-store'` (or
  `cookies()` / `headers()`), or a static render can be reused for another user.
- Mutations go through Server Actions or Route Handlers, then call
  `revalidatePath` / `revalidateTag`.
- Stream slow sections with `Suspense` and a skeleton.
- Do not fetch the same resource on the server and again in a client
  `useEffect` on mount; use a subscription or explicit refresh for live data.

**Anti-pattern**: `useEffect(() => { fetch('/api/...') }, [])` for first paint —
an HTML → JS → request waterfall with no SSR content.

## 4. Environment Variables

- `NEXT_PUBLIC_*` is inlined into the browser bundle at build time; treat it as
  permanently public. Changing it requires a rebuild.
- Server-only: gateway `client_secret`, `SUPABASE_SERVICE_ROLE_KEY`,
  `SESSION_SECRET`, webhook secrets.
- Validate once at startup and import only from server code:

```ts
// lib/env.ts
import 'server-only'
for (const key of ['NEXT_PUBLIC_APP_URL', 'NEXT_PUBLIC_SUPABASE_URL']) {
  if (!process.env[key]) throw new Error(`Missing environment variable: ${key}`)
}
export const env = {
  appUrl: process.env.NEXT_PUBLIC_APP_URL!,
  gatewayBaseUrl: process.env.GATEWAY_BASE_URL!,
}
```

- Only the Supabase anon/publishable key may be `NEXT_PUBLIC_*`.
- `.env.local` is git-ignored; commit `.env.example` with placeholders.

## 5. Route-Level UI Files

| File | Runs on | Purpose |
|------|---------|---------|
| `loading.tsx` | server | Suspense fallback for the segment |
| `error.tsx` | **client** | Error boundary; receives `{ error, reset }` |
| `not-found.tsx` | server | Rendered by `notFound()` or an unmatched route |
| `template.tsx` | server | Like layout but remounts on navigation |

- Give every data-backed segment a `loading.tsx` skeleton matching the final
  layout.
- `error.tsx` must be a Client Component with a recovery action (`reset()` or a
  link). Log the digest server-side; never show raw errors or stacks.
- Call `notFound()` for a missing row instead of rendering an empty page.

## 6. TypeScript

- `strict: true`, no `any`; use `unknown` plus narrowing or a schema parser.
- Parse HTTP responses, JWT claims, and form input with Zod at the boundary,
  then use `z.infer` inside the app.
- Shared domain types in `lib/types.ts`; component props beside the component.
- Prefer `interface` for object shapes and `satisfies` for config literals.
- Do not export server DTO types into a client bundle unless values are needed.

## 7. Route Handler as a Gateway BFF

Proxy through a Route Handler when the browser must not hold the credential,
the gateway does not allow the origin (CORS), or the call needs server policy.

```ts
// app/api/gateway/[...path]/route.ts
const GATEWAY = process.env.GATEWAY_BASE_URL!

async function proxy(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  await requireSession()                       // server-side authorization
  const { path } = await ctx.params
  const target = new URL(path.join('/'), `${GATEWAY}/`)
  target.search = req.nextUrl.search
  const upstream = await fetch(target, {
    method: req.method,
    headers: {
      'content-type': req.headers.get('content-type') ?? 'application/json',
      authorization: `Bearer ${await getGatewayToken()}`,   // secret stays here
    },
    body: req.method === 'GET' ? undefined : req.body,
    duplex: 'half',
  })
  return new Response(upstream.body, {
    status: upstream.status,
    headers: { 'content-type': upstream.headers.get('content-type') ?? 'application/json' },
  })
}
export { proxy as GET, proxy as POST }
```

- Authenticate and authorize inside the handler; a proxy without a session check
  is an open relay for your gateway quota.
- Allow-list proxied paths; never forward arbitrary URLs.
- Never pass client-supplied credentials upstream; inject the server token.
- Preserve upstream status and body shape so the UI can react to `401` and rate
  limits.
- If the browser can safely hold a short-lived token and the gateway allows the
  origin, direct calls are fine (see `../../SKILL.md`).

## 8. Anti-Patterns

| Anti-pattern | Consequence | Do instead |
|--------------|-------------|------------|
| `'use client'` on a page | Ships data logic to the browser | Keep page server |
| Static caching of user data | Cross-user data leak | `no-store` / dynamic APIs |
| `process.env.SECRET` in a client file | Secret inlined into JS | Server module or Route Handler |

## 9. Checklist

- [ ] `app/` holds only route files plus `_`-prefixed colocated folders.
- [ ] Every client component is a leaf and justifies `'use client'`.
- [ ] Initial data is server-fetched and deduplicated with `React.cache`.
- [ ] Only `NEXT_PUBLIC_*` values appear in client code.
- [ ] Data-backed segments have `loading.tsx`; risky ones have `error.tsx`.
- [ ] Secret-bearing gateway calls go through a Route Handler.
