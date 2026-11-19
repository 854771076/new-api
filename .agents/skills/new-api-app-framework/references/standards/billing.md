# Billing and User System Standard (Quota, PAT, Payments)

## Proxy-first integration and current limits

Prefer the user's existing Supabase login plus a trusted backend holding each user's AI key. Use a scoped admin PAT for supported management calls; create a specified-user checkout only after confirming that the target server actually implements that contract. Existing self-payment and `/api/token/*` handlers use the PAT owner's identity, even when the PAT owner is an administrator. Passing a target `user_id` to them does not delegate the operation.

App credit configuration is implemented on this development branch in admin `/api/oauth-apps/` (`quota_per_credit: integer | null`, 1..9007199254740991; omitted update keeps the value, null clears it). It changes display units only. App-bound key billing, specified-user PAT checkout and normalized wallet/quote credit fields are implemented on this branch. An ordinary platform `sk-...` key charges the platform wallet; an App-bound proxy key charges its immutable App wallet only; an OAuth App token charges its App pool. Verify matching funding scopes before connecting checkout to AI use. Do not substitute the administrator's self checkout, manually grant quota as payment confirmation, or create a parallel credits ledger.

Display their returned credits and conversion ratio rather than duplicating conversion in each client. `quota` remains the remaining ledger balance; it must not have `used_quota` subtracted again. A quote is an estimate, while actual consumption comes from settlement. Verify deployed support before relying on these fields.

The normalized wallet is a separate contract from legacy `/oauth/userinfo`. Its `quota`/`used_quota` are only for its response `app_id`, and credits use that App balance and current ratio; platform `app_id=0` keeps currency display with null ratio/credits. Legacy OAuth `quota`/`used_quota` remain platform fields and `app_quota`/`app_used_quota` remain token-App fields; leave that endpoint unchanged. The new DTO is accessed via the native console session, an authorized specified-user admin PAT, or the OAuth `api:read` surface fixed to the token App; never send a dashboard PAT to OAuth or let OAuth choose another App. Proxy reads only its bound App. Routes: `/api/user/self/wallet`, `/api/app-proxy/users/:user_id/wallet`, `/oauth/wallet`. All return the success envelope. Regression: platform 9000, App 1000, App used 200, ratio 100 returns App credits "10" and used credits "2", never 90 or 8; legacy OAuth still returns platform 9000 and App 1000.

Credit conversion preserves signed wallet debt. Truncate toward zero to at most six decimal places: quota -100 / ratio 100 returns credits "-1", and -1 / 3 returns "-0.333333". Subprecision negative amounts return "0", but the UI must use the raw negative quota to show debt below 0.000001 credits, never a debt-free zero. Top-ups first repay debt (-100 + 60 = -40). Preserve actual settlement and reject insufficient-balance new requests; display rules do not change ledger accounting.

OAuth order reads (`GET /oauth/orders` and `/oauth/orders/:order_id`): api:read, user and App fixed to the validated token, covering own App top-up, product and subscription orders. Add the reference OAuth BFF consumer there; the proxy reference remains independent of OAuth order reads and has no subscriptions. No dashboard PAT/session is needed for this target OAuth read contract. Verify deployed route support; keep awaiting-confirmation behavior on older instances. When OAuth is disabled, use the separate native-console historical read path.

Checkout/read correlation: every checkout branch covered by order reads must return a stable order_id while retaining existing fields and synchronous success versus online pending, including balance top-up/subscription and Stripe/Epay. Persist a queryable balance order and its ownership atomically with the transfer/purchase. Before any charge, grant or provider creation, persist an idempotency request scoped to the trusted actor, source, user, App and purchase type with canonical inputs: same key/inputs recovers the same order ID/snapshot; changed inputs conflict. Recover in-flight or unknown provider results by that key/reconciliation without repeating side effects. Expired orders keep their ID/status; a deliberate new purchase uses a new key. Update the reference BFF, gateway-contract.ts, response schemas and trusted order mapping together; save the key before calling so a lost first response or mapping-write failure is recoverable. The new BFF must send the key; retain legacy keyless requests compatibly, but never promise recoverable retries or automatically resubmit an unknown result without a key. Two same-plan purchases with distinct keys must remain distinguishable. Never infer an ID from the newest order, plan/amount, URL or balance. These are branch extensions to existing checkout cores; capability-gate required order_id validation and keep old instances without IDs awaiting confirmation.

Target proxy contracts are balance/top-up/one-time quota products only: **no subscriptions**, including reads, checkout, admin grants and relay funding. Dual-mode users are independent and must not share gateway IDs/assets. Enforce capability from trusted user origin, not merely `oauth_enabled`. OAuth-only plans and subscription examples below remain available to OAuth users.

Checkout snapshots fix source, user, App, target, currency, amount, credited quota and `quota_per_credit` atomically (explicit null when unconfigured). Retries return the same snapshot; historical orders without it keep their original currency/quota display. Disablement blocks new checkout, not authorized historical order queries or settlement of existing valid orders. Late real payments after expiry require audited exception handling, not silent loss or fabrication of a successful order.

The existing reference quote's top-level `credits: number` is raw quota. Preserve that legacy meaning while deprecating it; return `estimated_quota` and `estimated_credits: string | null`, and migrate clients/types explicitly. Bind accepted quotes to a server-generated version/digest, user, source, App and inputs before rechecking price/ratio. Do not silently change the old field's unit.

This file is the full contract behind **Step 4** of `../../SKILL.md`. Two
surfaces exist:

- `/api/*` — the dashboard user system (platform wallet, platform plans, PATs).
  It rejects OAuth access tokens; use a dashboard session or a PAT.
- `/oauth/*` — identity, App quota, plans, products, and top-up accept the OAuth
  access token. Catalog/payment calls are App-scoped. `/oauth/logs` returns the
  token owner's redacted logs, filtered to the fixed App.

An OAuth App has its own catalog (`app_id`) and its own quota pool
(`app_quota`). Platform `quota` keeps its original meaning and is not charged
by relay calls made with an App token.

## 1. Identity payload

`GET /oauth/userinfo` with `Authorization: Bearer <OAuth access token>`:

| Field | Present when scope | Meaning |
|-------|--------------------|---------|
| `sub` | always | new-api user ID as a string. Stable identity key. |
| `username` | `profile` | Login name. |
| `name` | `profile` | Display name. |
| `email` | `email` | Email address; may be empty. |
| `quota` | `api:read` | Platform wallet balance, in quota units. |
| `used_quota` | `api:read` | Cumulative platform consumption, in quota units. |
| `request_count` | `api:read` | Relay request count. |
| `group` | `api:read` | Billing group. |
| `app_quota` | `api:read` | Balance of the quota pool of the App that owns the token. Created lazily and `0` until first credit. |
| `app_used_quota` | `api:read` | Consumption charged to that App pool. |

Linking rules:

- Store `sub` as your user's `newapi_user_id`. Never key accounts on `username`
  or `email`: both are mutable and email may be absent.
- OAuth login does not create new-api users. The user must already exist in
  new-api, either self-registered or provisioned by an administrator.
- If your system owns the account lifecycle, create the new-api user first,
  then let the user sign in with OAuth.

This payload is the entire identity an admin console can read from OAuth. The
`id_token` adds only `sub`, `auth_time`, and `nonce`; it carries no name, email,
or role. Admin permissions therefore come from your own store
(`app_metadata.admin_role` / `app_admins`), not from new-api — see **Step 8** in
`../../SKILL.md`.

Provision an ordinary user for the existing native/OAuth path (admin session, or admin PAT with the matching `user` scope). This example does not establish proxy origin/App ownership, a unique external identity or recoverable stable-ID return; it is insufficient for proxy provisioning. That controlled contract is defined in `identity.md`.

```http
POST /api/user/
Content-Type: application/json

{
  "username": "alice",
  "password": "initial-password",
  "display_name": "Alice",
  "role": 1
}
```

`role` values: `1` common user, `10` administrator, `100` root. An administrator
can only create roles lower than its own; creating an administrator requires an
additional security proof.

## 2. Reading quota and usage

`GET /oauth/userinfo` (scope `api:read`) returns the platform wallet as `quota`
/ `used_quota` and the token's App pool as `app_quota` / `app_used_quota`. The
dashboard credential path still exists when you need the full DTO:

- User PAT with `profile:read` — `GET /api/user/self`
- Admin session or admin PAT — `GET /api/user/:id` for any user,
  `GET /api/user/?p=1&page_size=20&app_id=<id>` for the paged list, and
  `GET /api/user/search?keyword=&group=&role=&status=&p=&page_size=&app_id=<id>`
  for filtered search

`GET /api/user/self` returns the dashboard user DTO. Fields useful to an
external system:

| Field | Meaning |
|-------|---------|
| `id` | User ID (same value as `sub`) |
| `username`, `display_name` | Names |
| `role`, `status` | Role (`1` / `10` / `100`) and account status |
| `email` | Email address |
| `group` | Billing group |
| `quota` | Remaining platform wallet balance, in quota units |
| `used_quota` | Cumulative platform consumption, in quota units |
| `request_count` | Relay request count |
| `aff_code`, `aff_count`, `aff_quota`, `aff_history_quota` | Invitation data |
| `permissions` | Dashboard capability flags; do not use as your own authorization source |

Admin list/search accept `app_id` to switch to the App view: each user then
carries `app_quota` and `app_used_quota` for that App (`0` when the user has no
wallet there). This is a LEFT JOIN over all users, not an App-account ownership filter. Omitting `app_id`, or passing `0`, keeps the platform view.

The App user-list contract adds the protected proxy-account ownership condition: in App A, proxy rows must belong to A, even without an explicit source filter. Include A accounts without a wallet as zero balance; exclude B proxy accounts. Apply identical conditions to list/search/count/pagination/sort. Preserve existing OAuth/platform account funding-view behavior and the global admin directory; showing a proxy account globally does not authorize crediting another App or its platform wallet.

## 3. Quota units

`quota`, `used_quota`, `app_quota`, and `app_used_quota` are integer quota
units, not money.

`QuotaPerUnit` defaults to `500000` (1 USD = 500,000 quota units), and displayed
money is `quota / QuotaPerUnit`, converted with the configured exchange rate
when the display currency is CNY. Payment `amount` fields are in the configured
display type (money amount or token count), **not** quota units — never send a
`quota` value as a payment `amount`. `quota` is the platform wallet balance
only; relay calls made with an App token are charged to that App's pool
(`app_quota`) and, when the App has an active subscription, to the App-scoped
subscription first. Platform subscriptions (`GET /api/subscription/self`) and
App subscriptions are separate scopes. `/oauth/wallet` exposes normalized balances and capabilities; the native console scoped catalog includes own App subscriptions. Proxy users have no subscription state.

## 4. Personal access tokens (PAT)

Users create PATs in the dashboard (Security → Access Tokens). With a browser
session the same call is:

```http
POST /api/user/access_tokens
Content-Type: application/json

{
  "name": "external-crm",
  "scopes": ["profile:read", "wallet:read", "wallet:write"],
  "expires_at": 0
}
```

| Rule | Detail |
|------|--------|
| Format | The raw token starts with `nap_`, is shown once, and is sent as `Authorization: Bearer nap_...`. |
| Expiry | `expires_at` is a Unix timestamp; `0` means no expiry, and a non-zero value must be at least one hour in the future. |
| Creation | Requires a browser session and a security proof; a PAT cannot create another PAT. |
| Scopes | Each dashboard route declares its required scope: `profile:read`, `profile:write`, `account_security:write`, `wallet:read`, `wallet:write`, `billing:read`, `billing:write`, `usage:read`, and the admin scope `user:write`. `GET /api/user/access_tokens/scopes` lists the full catalog and also requires a browser session. |
| Limits | A PAT never exceeds the user's role, so admin routes still require an admin or root account. PAT requests have no browser `SessionID`, so they cannot list or revoke dashboard sessions. |

## 5. Session management

When your own frontend embeds the new-api dashboard, these endpoints manage
browser sessions:

- `GET /api/user/sessions` — list active sessions
- `DELETE /api/user/sessions/:sid` — revoke one session
- `POST /api/user/sessions/revoke-others` — revoke every other session

They reject PATs, so they require an actual browser session.

## 6. Payment system

Platform payment endpoints live under `/api/user/*` and `/api/subscription/*`,
so they require a dashboard session or a PAT. A practical PAT scope set is
`wallet:read` + `wallet:write`. App-scoped plans, products, and App top-up use
the `/oauth/*` endpoints in **App-scoped billing endpoints** below and accept the
OAuth access token itself.

Payment endpoints answer with `{"message": "success" | "error", "data": ...}`
over HTTP 200 rather than the usual `{"success": ...}` envelope. Redemption
(`POST /api/user/topup`) is the exception and uses the standard envelope.
`/oauth/*` endpoints instead use OAuth-style errors: HTTP status plus
`{"error":"...","error_description":"..."}`.

### Compliance gate

Top-up, redemption, subscription purchase, and invitation rewards stay disabled
until an administrator confirms the payment compliance terms. Check first:

```http
GET /api/user/topup/info
Authorization: Bearer nap_...
```

Use `payment_compliance_confirmed` and `payment_compliance_terms_version` from
the response. While it is `false`, payment calls return errors and
`GET /api/subscription/plans` returns an empty list.

### Available channels

`GET /api/user/topup/info` (scope `wallet:read`) also reports what is enabled:

| Field | Meaning |
|-------|---------|
| `enable_online_topup` | epay gateway |
| `enable_stripe_topup` | Stripe |
| `enable_creem_topup` | Creem |
| `enable_waffo_topup` | Waffo |
| `enable_waffo_pancake_topup` | Waffo Pancake |
| `enable_redemption` | Redemption codes |
| `pay_methods` | Methods as `{name, type, color, min_topup}` |
| `min_topup`, `stripe_min_topup`, `waffo_min_topup`, `waffo_pancake_min_topup` | Minimum amount per channel |
| `amount_options`, `discount` | Preset amounts and their discount ratios |
| `waffo_pay_methods`, `creem_products` | Channel-specific methods / products |
| `topup_link` | Optional external top-up URL |

`amount` is an **integer** in the configured display unit. These endpoints do
not accept fractional currency amounts.

### Top-up order APIs

All require scope `wallet:write`. Every one is rate limited, so do not retry
aggressively.

| Channel | Request | Response |
|---------|---------|----------|
| epay gateway | `POST /api/user/pay` `{"amount":10,"payment_method":"alipay"}` | `{"message":"success","url":"https://...","data":{...}}` |
| Stripe | `POST /api/user/stripe/pay` `{"amount":10,"payment_method":"stripe","success_url":"...","cancel_url":"..."}` | `data.pay_link` |
| Creem | `POST /api/user/creem/pay` `{"product_id":"...","payment_method":"creem"}` | `data.checkout_url`, `data.order_id` |
| Waffo | `POST /api/user/waffo/pay` `{"amount":10,"pay_method_index":0}` (`pay_method_index` omitted = auto) | `data.payment_url`, `data.order_id` |
| Waffo Pancake | `POST /api/user/waffo-pancake/pay` `{"amount":10}` | `data.checkout_url`, `data.session_id`, `data.expires_at`, `data.order_id`, `data.token`, `data.token_expires_at` |
| Redemption code | `POST /api/user/topup` `{"key":"REDEEM-CODE"}` | `{"success":true,"data":<credited quota>}` |

For epay, send the user to `url` with the returned `data` parameters. For Stripe,
Creem, and Waffo Pancake, redirect to the returned checkout link.

Amount preview (scope `wallet:read`): `POST /api/user/amount`,
`POST /api/user/stripe/amount`, `POST /api/user/waffo/amount`, and
`POST /api/user/waffo-pancake/amount` with `{"amount":10}` return the payable
money as a string.

### Order status and crediting

| Rule | Detail |
|------|--------|
| Crediting | Quota is credited only after the provider confirms payment (epay notify, or Stripe / Creem / Waffo webhooks); a browser return URL is not proof. |
| Polling | `GET /api/user/topup/self` (scope `wallet:read`) returns `trade_no`, `amount`, `money`, `payment_method`, `payment_provider`, `status`, `create_time`, `complete_time`. |
| Status | `pending`, `success`, `failed`, `expired`; only `success` means quota was credited. |
| Idempotency | Repeated provider notifications do not credit twice. |
| Missing payment | An administrator can run `POST /api/user/topup/complete` `{"trade_no":"..."}` (admin session, or admin PAT with `billing:write`). |

### App-scoped billing endpoints (OAuth access token)

Every endpoint below is selected by the token's `client_id`; it never returns or
charges another App. Payment errors use `400 checkout_failed` for business
failures (including insufficient balance and purchase limits), `403
access_denied` for a plan/product owned by another App or a mismatching
`app_id`, `404 not_found` for a missing plan/product, and `400
unsupported_payment_method` for an unsupported method. Auth errors are `401
invalid_token`, `403 insufficient_scope`, and `403 invalid_client` (the OAuth App
is disabled). Unexpected failures are `500 server_error`; an App top-up gateway
failure is `502 checkout_failed`.

| Endpoint | Scope | Request | Response |
|----------|-------|---------|----------|
| `GET /oauth/subscription/plans` | `api:read` | — | `{"plans":[{"plan":{...}}]}`, enabled plans of this App only |
| `POST /oauth/subscription/checkout` | `api:write` | `{"plan_id":1,"payment_method":"balance"}` | balance: `{"status":"success","payment_method":"balance"}`; online: `{"status":"pending",...}` with `url` + `params` (epay), `pay_link` (stripe), `checkout_url` + `order_id` (creem), or `checkout_url` + `session_id` + `expires_at` + `order_id` (waffo_pancake) |
| `GET /oauth/products` | `api:read` | — | `{"products":[{id,name,description,price_amount,currency,quota_amount,max_purchase_per_user}]}` |
| `POST /oauth/products/checkout` | `api:write` | `{"product_id":1,"payment_method":"balance"}` | balance: `{"status":"success","order_id":"...","quota_amount":...}`; online: `{"status":"pending","checkout_url":"...","order_id":"...","session_id":"...","expires_at":"..."}` |
| `POST /oauth/topup/checkout` | `api:write` | `{"amount":10,"payment_method":"balance"}` | balance: `{"status":"success","payment_method":"balance","quota":<app quota>}`; online: `{"status":"pending","checkout_url":"...","order_id":"...","expires_at":"..."}` |

App plan list returns the same `plan` object as the platform list, including
`app_id`; plan and product bodies carry `price_amount` in the configured
display unit, while `quota_amount` and `total_amount` are quota units.

Payment matrix:

| Purchase | Supported `payment_method` |
|----------|----------------------------|
| App subscription plan | `balance`, configured Epay types (e.g. `alipay`, `wxpay`), `stripe`, `creem`, `waffo_pancake`; legacy `epay` selects the first configured type |
| App product | `balance`, `epay`, `stripe`, `creem`, `waffo_pancake`, subject to configured product IDs/currency |
| App top-up | `balance`, configured Epay method, `stripe`, `creem`, `waffo_pancake` (default when omitted); proxy supports Pancake only |

Rules specific to the App pool:

- `POST /oauth/topup/checkout` with `payment_method: "balance"` is a **transfer**
  from the user's platform wallet into the App pool: the platform wallet is
  debited by the quota converted from `amount`, and the App pool is credited that
  same quota. It is not a free grant; an insufficient platform balance returns
  `400 checkout_failed` with an "Insufficient balance" description, and the
  normal minimum/maximum top-up bounds apply.
- Online top-up and online product payment credit `app_quota`, and online plan
  payment activates the App subscription, only after the provider notification;
  the browser return URL is never proof of payment.
- `balance` plan and product purchases debit `app_quota` itself (platform plans
  debit the platform wallet). An empty App pool fails with `400
  checkout_failed`; fund it with App top-up first.
- App top-up, product and subscription orders are App records. Use `/oauth/orders` and `/oauth/orders/:order_id` (`api:read`) fixed to the token user/App. Record `order_id` (`trade_no`) and its verified user/App ownership.
  Confirm success only through a supported, authorized order query for that
  exact order, then refresh `app_quota`. Without that query capability, show
  awaiting confirmation; balance changes cannot establish an order's status.
- `waffo_pancake` requires the gateway's Waffo Pancake configuration; a product
  paid this way must be priced in USD with at least `0.01`. Online plan payment
  also requires the channel configuration and, for `stripe`, the plan's
  `stripe_price_id`; send the selected configured Epay type in `payment_method`
  for subscription checkout. Legacy `epay` uses the first configured Epay method. Missing
  configuration returns `400 checkout_failed`.
- An App access token can submit async tasks and Midjourney calls. Pre-consume,
  completion-delta settlement, and refunds all use `app_quota`, and the App
  wallet `used_quota` is maintained alongside the platform statistics. Tasks are
  isolated by `app_id`: the token can only read, remix, or cancel tasks its App
  created, and Midjourney fetch/action endpoints resolve tasks within that same
  App.

### Subscriptions

`GET /api/subscription/plans` (scope `wallet:read`) returns
`[{ "plan": { ... } }]`. Relevant plan fields: `title`, `price_amount`,
`currency`, `duration_unit`, `duration_value`, `total_amount` (quota units,
`0` = unlimited), `allow_balance_pay`, `allow_wallet_overflow`,
`max_purchase_per_user` (`0` = unlimited), `upgrade_group`,
`quota_reset_period`.

Purchase (scope `wallet:write`):

| Method | Request |
|--------|---------|
| Wallet balance | `POST /api/subscription/balance/pay` `{"plan_id":1}` — deducted and activated synchronously |
| epay | `POST /api/subscription/epay/pay` `{"plan_id":1,"payment_method":"alipay"}` |
| Stripe | `POST /api/subscription/stripe/pay` `{"plan_id":1}` |
| Creem | `POST /api/subscription/creem/pay` `{"plan_id":1}` |
| Waffo Pancake | `POST /api/subscription/waffo-pancake/pay` `{"plan_id":1}` |

External channels return the same `url` / `pay_link` / `checkout_url` shapes as
top-ups; balance payment returns `success: true` immediately.

Read subscription state with `GET /api/subscription/self` (scope `wallet:read`):

```json
{
  "billing_preference": "subscription_first",
  "subscriptions": [ ... active ... ],
  "all_subscriptions": [ ... including expired ... ]
}
```

Subscription fields: `plan_id`, `amount_total` and `amount_used` (quota units),
`start_time`, `end_time`, `status` (`active` / `expired` / `cancelled`),
`next_reset_time`, `allow_wallet_overflow`, `upgrade_group`.

`PUT /api/subscription/self/preference` (scope `wallet:write`) accepts one of
`subscription_first`, `wallet_first`, `subscription_only`, `wallet_only`.
Use `wallet_only` to keep users off subscription quota, or `subscription_only`
to prevent falling back to wallet balance.

### Administration

Admin session, or admin PAT with `billing:read` / `billing:write`:

- `GET /api/user/` and `GET /api/user/topup` — user list and all top-up orders
- Plan CRUD: `GET|POST /api/subscription/admin/plans`,
  `PUT|PATCH /api/subscription/admin/plans/:id`
- Grant: `POST /api/subscription/admin/bind` `{"user_id":1,"plan_id":1}` — a
  plan without payment
- User subscriptions: `GET|POST /api/subscription/admin/users/:id/subscriptions`,
  `POST /api/subscription/admin/users/:id/subscriptions/reset`,
  `POST /api/subscription/admin/user_subscriptions/:id/invalidate`, and
  `DELETE /api/subscription/admin/user_subscriptions/:id`

App ownership on the admin surface:

- `GET /api/subscription/admin/plans` accepts `app_id`: omitted returns every
  plan, `0` returns platform plans only, and a positive value returns only that
  App's plans. `POST` and `PUT|PATCH` take the whole plan body including `app_id`
  (`0` = platform). A positive `app_id` must be an existing OAuth App, otherwise
  the request is rejected. The update replaces the record, so omitting `app_id`
  moves the plan to the platform catalog and omitting other fields resets them.
- Quota adjustment: `POST /api/user/manage`
  `{"id":<user>,"action":"add_quota","mode":"add|subtract|override","value":<quota>,"app_id":<id>}`
  with an admin PAT (`user:write`). `app_id` omitted or `0` adjusts platform
  `quota`; a positive value adjusts only that user's App pool. Subtracting more
  than the App pool holds fails rather than going negative.
- Products: `GET|POST /api/app-products`, `PUT /api/app-products/:id`,
  `PATCH /api/app-products/:id` `{"enabled":true|false}`, and
  `DELETE /api/app-products/:id`. `GET` accepts `app_id` (omitted or `0` = all
  Apps). Creation requires an existing positive `app_id`; deletion is refused
  while orders reference the product.
- Plan create/update is blocked until an administrator confirms the payment
  compliance terms, the same gate as platform payments.
- Product orders: `GET /api/app-products/orders` accepts `app_id`, `user_id`,
  `p`, and `page_size`;
  `POST /api/app-products/orders/:trade_no/complete` marks a pending order paid
  and credits the App pool. Use it only after offline payment is confirmed.

## 7. Credits, top-up, and orders are new-api data

Do not build a parallel wallet. In this standard, the app's "credits" are new-api
`quota` (platform wallet), the App pool `app_quota`, and subscription quota;
top-up orders and consumption records are new-api records. The UI may convert
quota units to credits for display, but there is exactly one balance per pool.

| App object | Source of truth | API |
|------------|-----------------|-----|
| Platform balance / credits | `quota`, `used_quota` | `GET /api/user/self` (scope `profile:read`) or `/oauth/userinfo` (`api:read`) |
| App quota pool | `app_quota`, `app_used_quota` | `GET /oauth/userinfo` (`api:read`) |
| Subscription quota | active subscriptions | `GET /api/subscription/self` (platform, scope `wallet:read`); console `/api/user/self/app-catalog?app_id=<id>` includes own App subscriptions |
| App plans and products | App catalog | `GET /oauth/subscription/plans`, `GET /oauth/products` (`api:read`) |
| App orders | immutable App purchase records | `GET /oauth/orders` (`api:read`), console `/api/user/self/orders?app_id=<id>`, or scoped proxy admin route (`billing:read`) |
| Consumption / usage | request logs | `/oauth/logs` (`api:read`), or `GET /api/log/self`, `/self/stat`, `/self/search` (scope `usage:read`) |
| Channels and compliance | payment config | `GET /api/user/topup/info` |

Choose the access mode that matches the identity flow:

| Mode | Quota / orders readable | How |
|------|-------------------------|-----|
| Dashboard session (embedded dashboard or BFF login) | Yes | Browser session cookie on the gateway origin, or a same-site BFF |
| PAT (`profile:read`, `wallet:read`, `billing:read`, `usage:read`) | Yes, for that user | User creates it in the new-api dashboard |
| OAuth access token only (Supabase direct mode) | App pool/catalog, platform wallet totals and exact App order history: yes | `/oauth/userinfo` includes platform `quota` and App `app_quota`; `/oauth/*` handles App purchases and App-scoped redacted logs; `/api/*` rejects OAuth tokens |

Rules:

- OAuth-only apps top up and buy through `/oauth/topup/checkout`,
  `/oauth/subscription/checkout`, and `/oauth/products/checkout`. Do not send the
  user to `<GATEWAY_PUBLIC_URL>/app.html#/topup` for App quota, and do not
  reimplement payment. Platform wallet top-up and platform order history still
  require the dashboard (or a PAT), and an OAuth token can never mint a PAT.
- Every `/self` endpoint returns the PAT owner's data. A scoped administrator PAT can read a specified user through `/api/app-proxy/users/:user_id/wallet?app_id=<id>` (`user:read`). Other cross-user reads require admin endpoints (`GET /api/user/` with `user:read`, `GET /api/user/topup` with
  `billing:read`) and belong in the admin console, never in the consumer app to
  fabricate per-user balances.
- No second ledger: no local `credits`, `wallet`, or `orders` table as the
  authoritative balance. A local mirror keyed by `trade_no` is cache only,
  reconciled from an authorized order query matching its `trade_no`, user and
  App. `/api/user/topup/self` applies only to the credential owner's supported
  orders. Neither `app_quota` nor a return URL marks an order paid; without a
  supported query, keep its payment status awaiting confirmation.
- Relay calls are billed by new-api automatically. Do not deduct credits again in
  application code after a `/v1/*` call.
- App-level paid actions (unlock a feature, buy a template) are either priced as
  relay usage, or performed by your backend as an audited quota adjustment:
  `POST /api/user/manage` `{"id":<sub>,"action":"add_quota","mode":"subtract","value":<quota>}`
  with an admin PAT (`user:write`); add `"app_id":<id>` to adjust the App pool
  instead of the platform wallet. Keep an idempotency key and a business record,
  never expose that token or endpoint to the browser, and refuse to go negative.
- Define the quota-to-credit conversion once, in one module, and format it with
  the project's number/currency helpers.

### 外部 app 站内 topup 推荐模式（不跳 newapi 仪表板）

**不要**让用户跳 `{gateway}/app.html#/topup` 充 App 额度——站内体验断了。推荐模式：

1. OAuth 模式的 Server Action / Route Handler 从私有加密会话库读取 OAuth access token（仅在已验证的 PKCE 回调初始化，禁止每次请求从 provider_token 恢复），调 new-api `POST /oauth/topup/checkout`，body 为 `{"amount":10,"payment_method":"waffo_pancake"}`（省略 `payment_method` 时默认 `waffo_pancake`）。返回 `checkout_url` 与 `order_id`；后端保存订单与已验证用户/App 的归属，Route Handler 对前端仅返回收银台链接及订单标识；**不向前端透传 access token**。
   - 也可用 `"payment_method":"balance"`：从用户**平台余额**扣款并转入该 App 额度池，余额不足返回 `400 checkout_failed`（不是免费发放）。
   - `amount` 是配置的展示单位整数，不是 quota。
2. 前端点击"充值"按钮 → fetch Route Handler → 得到 checkout_url → 打开 DaisyUI `<dialog>` / `<dialog className="modal">` + iframe `src={checkoutUrl}`。Modal 右上角关闭按钮。
3. 降级路径：如果 Waffo 策略阻止 iframe，catch 后 `window.open(checkoutUrl, '_blank', 'width=720,height=640')`。
4. 用户关闭 Modal 后，后端通过已支持且有权限的订单查询核对该 `order_id` 的用户/App 归属与成功状态，再刷新 `/oauth/userinfo` 的 `app_quota`。若目标实例没有对应查询能力，界面保持“待确认”；不能以余额变化、关闭弹窗或浏览器回跳确认该订单支付成功，本分支使用 `/oauth/orders/:order_id`，旧部署需要先确认能力。

平台钱包充值、平台订单历史仍需 newapi 仪表板或 PAT；App 额度与 App 套餐/商品不需要。

**不要**让前端直接持有 OAuth access token（安全；access token 是用户级敏感凭证，只能在 Route Handler / Server Component 内用）。

### 外部 app 消费日志推荐模式

- 用 OAuth access token 调 `/oauth/logs`（scope `api:read`），走 Route Handler 代理（前端不拿 token）。
- 返回已脱敏的白名单字段；不需要也不应该在外部 app 显示 prompt 原文或上游 API key。
- Profile 页面用 DaisyUI `<table>` 渲染，服务端分页（`page`/`page_size`）+ 空状态。
- `/oauth/logs` 按已验证 token 的用户和 App 在服务端筛选。代理使用 `/api/app-proxy/users/:user_id/logs?app_id=<id>`（管理员 `log:read`），BFF 固定可信目标。成功响应是裸
  `{total,page,page_size,items}`；item 白名单仅为 `id,created_at,username,token_name,
  model_name,quota,prompt_tokens,completion_tokens,use_time,channel_name,group,request_id`。
  不返回 `type,is_stream,channel_id,upstream_request_id,content,other,ip,user_id,token_id`。
  默认 page=1/page_size=20，page 最大 10000，page_size 上限 100；不支持 `order`。
  租户隔离由服务端查询实现，不能依赖客户端筛选。

Scoped payment limits: proxy checkout and top-up preview currently use Waffo Pancake only. OAuth retains supported payment cores. Epay scoped purchases require CNY; Stripe scoped top-up requires an active USD one-time per-unit Price with an integer minor-unit price and whole quantities. Its payable snapshot uses the provider Price, not the purchased unit count. Promotion codes are disabled for scoped Stripe checkout so the paid amount matches the frozen snapshot. A provider price/currency mismatch or late payment remains reconciliation_required; sandbox merchant acceptance is still required. Unsupported periodic key-rule fields are rejected explicitly; this branch does not add a separate rules engine.

## Timed proxy membership rules

Use the specified-user quota/cancel contract in `api-contract.md`. Rule dates enforce an enabled key's new-request admission and do not fund wallets or enable proxy subscriptions. Disabling/unbinding removes restrictions. Each key has independent calendar usage; rule-bound rotation remains unsupported. Cancel pending replacements before binding, and never revive a cancelled operation. Admission snapshots preserve the original budget through rule edits, expiry, deletion and task recovery; legacy snapshots cannot supplement reservations. A task stranded before a confirmed upstream result remains `SUBMITTING` for explicit reconciliation, without automatic repeat work or refunds. The reference admin BFF pins native mapping and App; ordinary consumers cannot write paid entitlement gates.
