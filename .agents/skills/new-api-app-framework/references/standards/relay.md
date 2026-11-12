# Relay Standard (AI Calls through `/v1/*`)

All AI traffic goes to `/v1/*` on the gateway with an OAuth access token. This
file is the full contract behind **Step 3** of `../../SKILL.md`. An OAuth access
token is **not** a dashboard credential: do not send it to `/api/*`.

## 1. Proxy through a Next.js Route Handler

A BFF proxy keeps the token on the server and centralizes retry, logging, and
rate limiting:

```typescript
// src/app/api/relay/[...path]/route.ts
import { type NextRequest } from 'next/server';
import { getGatewayAccessToken } from '@/lib/gateway-session';

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

## 2. Direct call from a public client

In Supabase direct mode the browser calls the gateway itself:

```typescript
// Direct AI call (no BFF):
const res = await fetch('https://your-gateway.com/v1/chat/completions', {
  method: 'POST',
  headers: { Authorization: `Bearer ${await getGatewayAccessToken()}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [...] }),
});
```

`getGatewayAccessToken()` refreshes before expiry, keeps the rotated refresh token, and clears local state on `invalid_grant`; in Next.js it lives in `src/lib/gateway-session.ts` and is imported only by Client Components.

Model access per OAuth app is bounded by the app's whitelist; see **Model
Whitelist per App** in `../../SKILL.md`.
