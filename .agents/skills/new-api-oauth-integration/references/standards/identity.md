# Identity Standard (new-api OAuth 2.0 / OIDC)

new-api is the OIDC identity provider; your app never stores new-api passwords.
This file is the full contract behind **Step 2** of `../../SKILL.md`: creating
the OAuth app, choosing an integration mode, the confidential and public client
code, the Supabase custom provider, the starter app, and logout. Local
verification and the Cloudflare Tunnel acceptance flow live in
`local-acceptance.md`; admin roles seeded from these identities live in
`admin-dashboard.md`.

## 1. Create an OAuth application

In the new-api admin dashboard, go to **OAuth Apps** and create an application:

- **Name**: Your application's display name
- **Redirect URIs**: your callback. Use `https://<project>.supabase.co/auth/v1/callback`
  for Supabase direct mode, or `https://app.example.com/auth/callback` for a BFF
- **Post-Logout Redirect URIs**: `https://your-app.com/logout-done`
- **Public client**: enable for SPAs and mini-programs (PKCE only, no secret)
- **Scopes**: `openid` (OIDC identity), `profile` (username, display name),
  `email` (email address), `api:read` (read-only AI access), `api:write` (chat,
  images, and other non-read AI endpoints). See **Scopes Reference** in `../../SKILL.md`.

A confidential client returns `client_id` and `client_secret`; the secret is
shown **only once** — store it in backend environment variables. A **public
client** returns only `client_id`: it never has a secret, authentication relies
on PKCE, and `code_verifier` stays mandatory. Do not enable public mode for a
server-side app that can keep a secret.

`newapi-cli` is the supported way to create / probe OAuth apps.
**注意：当前 `newapi-cli` 版本没有 `oauth-apps create` 子命令**，
用通用 `api` 命令代替：

```bash
# 列出所有 OAuth apps，挑选已有或决定是否新建
newapi-cli api GET /api/oauth-apps

# 创建 public client（Supabase public client 模式）
newapi-cli api POST /api/oauth-apps --data '{
  "name": "My App",
  "redirect_uris": ["https://<project>.supabase.co/auth/v1/callback"],
  "post_logout_redirect_uris": ["https://<app>/logout-done"],
  "scopes": ["openid","profile","email","api:read","api:write"],
  "public_client": true,
  "status": 1
}'

# 查询单个 app（拿到当前 redirect_uris 再追加，不要直接覆盖）
newapi-cli api GET /api/oauth-apps/<id>

# 更新已有的（例如追加 redirect_uri）
newapi-cli api PUT /api/oauth-apps/<id> --data '{
  "redirect_uris": ["https://<project>.supabase.co/auth/v1/callback","https://<app>/auth/callback"]
}'
```

对于 `status=1` 且带有效 20 字符 `supabase_project_ref` 的 public client，可以**复用**，但仍须校验当前项目、issuer、client_id、PKCE 和 scopes。bootstrap 的 override 必须唯一匹配当前 App 列表；所有 Supabase keys/db 命令显式使用 `--remote=<ref>`。已有 `.env.local` 或进程环境的项目/App/网关绑定必须一致；已有加密 key 必须保持原值且为 canonical base64 编码的 32 字节，缺失才使用 CSPRNG 生成，空值、重复或格式错误都中止。迁移目录属于旧项目时使用独立工作副本，不要复用或重新绑定旧项目迁移。

## 2. Choose an integration mode

BFF mode keeps `client_secret` and tokens server-side and proxies AI calls;
public PKCE mode has no secret, stores tokens in Supabase Auth + app memory,
refreshes via `/oauth/token`, calls the gateway directly, and isolates data with
RLS + `client_id`. Use BFF when you have a server.

## 3. Confidential client in a Next.js Route Handler

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

## 4. Public client with PKCE (Supabase direct mode)

When your app uses Supabase as the backend (Auth, PostgREST, Storage, Edge
Functions), you can skip the BFF entirely. Supabase Auth acts as the OIDC
client, and the browser talks directly to new-api for AI calls.

```text
Browser / Mini-Program
  ├─ Supabase Auth ──► new-api OIDC Provider (login, JWT)
  ├─ Supabase PostgREST ─► PostgreSQL + RLS (business data)
  └─ new-api /v1/* ─► AI relay (direct, with OAuth token)
```

### Supabase custom provider setup

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

# 列出所有已配置 custom providers（端点是 custom-providers 复数，不是 providers），先过滤再决定创建还是跳过
curl -s "https://<project>.supabase.co/auth/v1/admin/custom-providers" \
  -H "apikey: $SUPABASE_SECRET_KEY" -H "Authorization: Bearer $SUPABASE_SECRET_KEY"

# 幂等安全的检查：如果 GET 返回的 providers 数组里 identifier="custom:newapi"
# 且 issuer/client_id/scopes 匹配、pkce_enabled=true、email_optional=true，就跳过 POST。
# 否则 DELETE 后重建：
curl -X DELETE "https://<project>.supabase.co/auth/v1/admin/custom-providers/custom:newapi" \
  -H "apikey: $SUPABASE_SECRET_KEY" -H "Authorization: Bearer $SUPABASE_SECRET_KEY"
```

- **Gateway 必须先可达再创建**：Auth service 在 provider 创建时立即拉 issuer 的 `.well-known/openid-configuration`，gateway HTTPS 不可达会导致 provider 创建失败（或 discovery_document 为空，后续登录 500）。
- `supabase db push --remote=<ref> --yes` 之前先成功执行 `supabase link --project-ref <ref>`；link 失败必须中止。用 `supabase projects api-keys --remote=<ref> --reveal` 拿 anon + service_role，捕获输出不打印密钥；保留已有 `.env.local` 的加密 key 和部署绑定。
- **Supabase site_url / additional_redirect_urls** 的配置方式见 `local-acceptance.md`（`http://localhost:*` 默认允许；非 localhost 域名必须显式加入）。

- The Auth service fetches the issuer's discovery document when the provider is created, so the gateway must already be publicly reachable over HTTPS and `ServerAddress` must equal the issuer. `discovery` must list `none` in `token_endpoint_auth_methods_supported` (public client).
- `supabase config`/`config push` cannot declare custom OIDC providers, and the Management API has no provider CRUD. Do not hand-insert into `auth.custom_oauth_providers` with a bare identifier — Auth looks up `custom:<identifier>` and keeps returning `custom provider ... not found`.
- `email_optional: true` is required when the gateway user has no email. The new-api OAuth app registers `https://<project>.supabase.co/auth/v1/callback`.
- To rotate credentials, delete the old provider first (`DELETE /auth/v1/admin/custom-providers/custom:newapi`, identifier with the `custom:` prefix) and create it again; editing the new-api OAuth app from confidential to public does not update Supabase on its own.

**Map claims**: ensure `app_metadata` includes `gateway_user_id` and
`client_id`; the `user-sync` Edge Function handles this on first login.

**RLS**: every business table must have `client_id text not null` and use
`current_app_client_id()` (security definer function) to isolate rows by
OAuth app. See `../web/outfit-demo/supabase/schema.sql`.

### PKCE flow from a Client Component

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
id), never in `user_metadata`. `../web/outfit-demo/lib/gatewayAuth.ts`
is the reference implementation and:

| Behavior | Detail |
|----------|--------|
| Expiry check | Parses `exp` from the access token JWT; refreshes when less than 60 s remain |
| Concurrency | Deduplicates concurrent refreshes with one in-flight promise |
| Relay `401` | Retries exactly once, then surfaces the error |
| `invalid_grant` | Clears local state and signs the user out |

### `sub` vs `auth.users.id` 映射

`newapi-cli api GET /api/user/self` 的正整数 `id` = OAuth `sub`；可信来源是
Auth 管理的 `auth.identities.identity_data`，不是用户可编辑的
`raw_user_meta_data`。`auth.users.id`（UUID）才是 `app_admins.user_id`。
`INITIAL_ADMIN_SUB` 保留网关数字 subject，不要改成 UUID。
bootstrap.ps1 / bootstrap.sh 校验当前 App、项目与 provider 配置后自动映射。
手动解析时，先校验 `<newapi_user_id>` 为正整数字符串，并使用受信任配置中的
`<client_id>`；查询必须恰好返回一行（不能用 `LIMIT 1` 隐藏歧义）：

```sql
SELECT u.id::text
FROM auth.identities i JOIN auth.users u ON u.id = i.user_id
WHERE i.provider = 'custom:newapi'
  AND i.identity_data->>'sub' = '<newapi_user_id>'
  AND i.identity_data->>'sub' ~ '^[1-9][0-9]*$'
  AND pg_catalog.jsonb_typeof(i.identity_data->'aud') = 'array'
  AND pg_catalog.jsonb_typeof(i.identity_data->'aud'->0) = 'string'
  AND i.identity_data->'aud'->>0 = '<client_id>'
  AND (SELECT count(*) FROM auth.identities linked
       WHERE linked.user_id = u.id AND linked.provider = 'custom:newapi') = 1;
```

用 `supabase db query --remote=<20-char-project-ref> --read-only "<sql>"`。
授权插入时必须再次验证唯一身份，使用 `app_admins(client_id,user_id)` 复合唯一键；
完整事务示例见 `admin-dashboard.md` §3.1。

The first-owner grant itself is described in `admin-dashboard.md` (section 3.1).

## 5. Starter template and reference app

`../web/outfit-demo/` is a runnable Next.js App Router app built on the
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
renders 8 official outfits after login. Local verification symptoms and fixes are
tabulated in `local-acceptance.md`.

## 6. Handle logout

When the user logs out, revoke the refresh token and clear your session. In BFF
mode, call `oauthClient.revoke(session.refreshToken)` in a `POST` Route Handler
and redirect to `APP_ORIGIN`. In public PKCE mode, revoke
`provider_refresh_token` through `oauthClient` or a direct `POST /oauth/revoke`,
then call `supabase.auth.signOut()`.

### OAuth access token 能调的外部端点（除了 /v1/models）

new-api 暴露以下 `/oauth/...` 端点供 OAuth access token 直接调用，避免外部 app 去碰 dashboard PAT 或走 `/api/user/self`。套餐、商品、充值按 token 的 `client_id` 限定到单个 App；`/oauth/logs` 当前是 token 用户的全量脱敏日志，不按 App 过滤。

| Endpoint | Scope | Request | Response |
|----------|-------|---------|----------|
| `GET /oauth/userinfo` | `openid`(必须) + `api:read`(可选) | Authorization: Bearer <token> | `sub`；授予 `profile` 时附 `username, name`，授予 `email` 时附 `email`；带 `api:read` 时再附加 `quota, used_quota, request_count, group, app_quota, app_used_quota` |
| `GET /oauth/subscription/plans` | `api:read` | — | `{ "plans": [ { "plan": { ... } } ] }`，仅本 App 的已启用套餐 |
| `POST /oauth/subscription/checkout` | `api:write` | `{ "plan_id": 1, "payment_method": "balance" }` | 余额支付 `{ "status": "success", "payment_method": "balance" }`；在线支付 `{ "status": "pending", ...跳转字段 }` |
| `GET /oauth/products` | `api:read` | — | `{ "products": [ { id, name, description, price_amount, currency, quota_amount, max_purchase_per_user } ] }` |
| `POST /oauth/products/checkout` | `api:write` | `{ "product_id": 1, "payment_method": "balance" }` | 余额支付 `{ "status": "success", "order_id": "...", "quota_amount": ... }`；waffo_pancake 返回 `checkout_url` |
| `POST /oauth/topup/checkout` | `api:write` | `{ "amount": 10, "payment_method": "balance" }` | 余额支付 `{ "status": "success", "payment_method": "balance", "quota": ... }`；在线支付 `{ "status": "pending", "checkout_url": "...", "order_id": "...", "expires_at": "..." }` |
| `GET /oauth/logs` | `api:read` | query `page=1&page_size=20`；默认 1/20，page 最大 10000、page_size 上限 100；无效/非正值使用默认值；`order` 不支持 | `{ total, page, page_size, items: [ { id, created_at, username, token_name, model_name, quota, prompt_tokens, completion_tokens, use_time, channel_name, group, request_id } ] }`；token 用户的**全量**日志，非 App 隔离；不含 `content`、`other`、`ip`、`user_id`、`token_id`、`channel_id`、`type`、`is_stream`、`upstream_request_id` |

- `POST /oauth/topup/checkout` 的 `balance` 是**从用户平台余额划转到该 App 额度池**，不是免费发放；余额不足返回 `400 checkout_failed`。在线充值当前支持 `waffo_pancake`（也是省略 `payment_method` 时的默认值），套餐还支持 `epay` / `stripe` / `creem`。
- `GET /oauth/userinfo` 的 `quota` 是平台钱包，`app_quota` 是 token 所属 App 的额度池；OAuth relay 的预扣/结算/退款只作用于 App 额度池。平台 `quota` 语义不变。
- App access token 可直接调用异步任务与 Midjourney 接口；预扣、完成后的差额结算与失败退款都走该 App 的额度池，App 钱包 `used_quota` 与平台统计同步累加。任务按 `app_id` 隔离：App token 只能查询、remix、取消本 App 创建的任务，Midjourney 的 fetch/action 接口同样只命中本 App 的任务。
- 所有端点只认 OAuth access token；不接受 PAT。
- 后端不创建数据库迁移；TopUp 记录沿用 dashboard 侧同一 schema（App 充值记录带 `app_id`）。
- bootstrap.sh/.ps1 只复用当前已启用的 public OAuth app，不创建或修改 OAuth app；缺少 `openid profile email api:read api:write` 时中止。Supabase provider 同步这些 scopes。外部 app 登录授权后即可拿到额度、购买 App 套餐/商品、发起充值、查看用户级脱敏日志。
