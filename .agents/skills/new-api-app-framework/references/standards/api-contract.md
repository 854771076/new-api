# API Contract (Pagination + Response Codes)

This file holds the cross-cutting conventions shared by every Next.js Route
Handler and every list-returning endpoint in this standard. It covers offset
pagination and the `{"success", "message", "data"}` response envelope that
matches new-api's `common/gin.go`.

## 1. Pagination — offset-based only

所有列表接口统一用 **offset-based 分页**（参数 `page` + `page_size`），
而不是 cursor-based（`cursor` + `limit`）——原因：Supabase PostgREST 和 GORM 都天然支持
`count` + `range`，cursor-based 需要额外游标字段设计。

### 请求参数

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `page` | int | 1 | 页码从 1 开始 |
| `page_size` | int | 20 | 每页条数；上限 100，超出按 100 处理 |
| `sort` | string | `created_at` | 排序字段（数据库列名） |
| `order` | `asc` \| `desc` | `desc` | 排序方向 |
| `<filter>` | varies | — | 业务筛选参数，如 `?category=tops&is_active=true` |

### 响应结构

```json
{
  "success": true,
  "message": "",
  "data": {
    "items": [ { ... }, { ... } ],
    "total": 1407,
    "page": 1,
    "page_size": 20,
    "total_pages": 71
  }
}
```

注意：`items` 是数组（不是对象的某个字段），`total_pages` 由服务端算好
`ceil(total / page_size)`，前端直接用。

### Next.js Route Handler 分页模板

```typescript
// app/api/outfits/route.ts
import { NextResponse } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';

export async function GET(request: Request) {
  const url = new URL(request.url);
  const pageInput = Number(url.searchParams.get('page') ?? '1');
  const sizeInput = Number(url.searchParams.get('page_size') ?? '20');
  const page = Number.isSafeInteger(pageInput) && pageInput > 0 ? pageInput : 1;
  const pageSize = Number.isSafeInteger(sizeInput) && sizeInput > 0 ? Math.min(100, sizeInput) : 20;
  if (!Number.isSafeInteger(page * pageSize)) {
    return NextResponse.json({ success: false, message: 'Bad request' }, { status: 400 });
  }
  const requestedSort = url.searchParams.get('sort') ?? 'created_at';
  const sort = ['created_at', 'name_zh'].includes(requestedSort) ? requestedSort : 'created_at';
  const order = url.searchParams.get('order') === 'asc' ? 'asc' : 'desc';
  const category = url.searchParams.get('category');

  // Cookie session + anon/publishable key: all reads still enforce RLS.
  const supabase = await createServerSupabase();
  const { data: auth, error: authError } = await supabase.auth.getUser();
  if (authError || !auth.user) {
    return NextResponse.json({ success: false, message: 'Unauthorized' }, { status: 401 });
  }
  // Protected provider identity + server config; see supabase.md §3.
  const { data: clientId, error: scopeError } = await supabase.rpc('current_app_client_id');
  if (scopeError || typeof clientId !== 'string' || !clientId) {
    return NextResponse.json({ success: false, message: 'Forbidden' }, { status: 403 });
  }
  let q = supabase.from('outfits').select('id, name_zh, category, created_at', { count: 'exact' })
    .eq('client_id', clientId)
    .eq('is_active', true)
    .order(sort, { ascending: order === 'asc' })
    .range((page - 1) * pageSize, page * pageSize - 1);
  if (category) q = q.eq('category', category);

  const { data, count, error } = await q;
  if (error) return NextResponse.json({ success: false, message: 'Internal error' }, { status: 500 });

  return NextResponse.json({
    success: true,
    message: '',
    data: {
      items: data ?? [],
      total: count ?? 0,
      page,
      page_size: pageSize,
      total_pages: Math.max(1, Math.ceil((count ?? 0) / pageSize)),
    },
  });
}
```

This is an authenticated App catalog, not an anonymous service-role endpoint.
RLS must enforce the same App binding; user-private tables additionally require
`user_id = auth.uid()` (and an explicit owner filter when appropriate).

### 前端分页控件（DaisyUI）

```tsx
// page.tsx Server Component 渲染到 Client Component
<div className="join flex justify-center">
  {Array.from({ length: Math.min(7, totalPages) }, (_, i) => i + 1).map((p) => (
    <Link
      key={p}
      href={`/wardrobe?page=${p}`}
      className={`btn btn-sm join-item ${p === page ? 'btn-primary' : 'btn-outline'}`}
    >{p}</Link>
  ))}
  <span className="btn btn-sm btn-ghost join-item">共 {total} 条</span>
</div>
```

如果 `totalPages > 7`，中间加省略号（显示 1 ... 4 5 6 ... 7 或 1 2 ... 5 ... 8 9）。

## 2. Response envelope — `{"success": bool, "message": "...", "data": ...}`

new-api 后端已确立 `{"success": bool, "message": "...", "data": ...}` 的格式
（见 `common/gin.go` 的 `ApiSuccess` / `ApiError` / `ApiErrorStatus`）。外部 Next.js
Route Handler 必须跟这个格式对齐，保持前后端一致。

### new-api 后端既有格式（参考）

```go
// common/gin.go:ApiSuccess → HTTP 200
gin.H{"success": true, "message": "", "data": data}

// common/gin.go:ApiError → 默认 HTTP 200，可用 ApiErrorStatus(c, 400, err) 改
gin.H{"success": false, "message": err.Error(),
      "message_key": key, "message_params": params,  // web console i18n
      "error_code": "..."}                            // 可选业务码
```

### Next.js Route Handler 响应模板

| 场景 | HTTP 状态 | JSON shape | 模板 |
|------|-----------|------------|------|
| **成功** | 200 | `{ success: true, message: "", data: T }` | `NextResponse.json({ success: true, data })` |
| **创建成功** | 201 | `{ success: true, message: "Created", data: T }` | `NextResponse.json({ success: true, message: "Created", data }, 201)` |
| **部分成功** | 200 | 同上 | 批量 upsert 有行跳过但不阻断整体 |
| **参数校验失败** | 400 | `{ success: false, message: "<i18n key>", error_code: "BAD_REQUEST", errors: [...] }` | Zod / manual validation fail |
| **未认证** | 401 | `{ success: false, message: "Unauthorized" }` | session 不存在 / token 过期；让前端 redirect `/login` |
| **无权限** | 403 | `{ success: false, message: "Forbidden" }` | 不是 admin / quota 不足 |
| **资源不存在** | 404 | `{ success: false, message: "Not found", error_code: "NOT_FOUND" }` | `id` 查不到 |
| **冲突（幂等创建）** | 409 | `{ success: false, message: "Already exists" }` | 唯一键冲突 |
| **限流** | 429 | `{ success: false, message: "Too many requests", retry_after: 60 }` | 保留 `retry_after` 秒数 |
| **服务端异常** | 500 | `{ success: false, message: "Internal error" }` | **不要**把 panic / stack trace 暴露到 response body |

模板代码：

```typescript
// lib/api-response.ts — 可选，统一封装
import { NextResponse } from 'next/server';

export const ok = <T>(data: T, message = '', status = 200) =>
  NextResponse.json({ success: true, message, data }, { status });

export const created = <T>(data: T, message = 'Created') =>
  NextResponse.json({ success: true, message, data }, { status: 201 });

export const fail = (
  message: string,
  status: number,
  errorCode?: string,
  extra?: Record<string, unknown>,
) =>
  NextResponse.json(
    { success: false, message, ...(errorCode ? { error_code: errorCode } : {}), ...extra },
    { status },
  );
```

### 前端错误处理约定

- **401**：清除 session + redirect `/login`（不是弹窗）
- **403**：toast "额度不足，请先充值" + 打开 App 充值（`POST /oauth/topup/checkout`）；只有平台额度不足才跳 newapi `/topup`。App 级接口的 403 还可能是 `access_denied`，见 §3，不要一律当成额度不足
- **429**：toast + 按钮重试（带倒计时）
- **500**：toast "服务暂时不可用，请稍后重试" + 日志上报
- 所有非 2xx 都要有用户可见反馈（toast / alert banner），不能静默吞掉

### 错误码命名

`error_code` 用大写蛇形 `UPPER_SNAKE_CASE`，固定前缀：

| 前缀 | 含义 | 示例 |
|------|------|------|
| `BAD_REQUEST_*` | 参数/格式错误 | `BAD_REQUEST_MISSING_REQUIRED`, `BAD_REQUEST_INVALID_ENUM` |
| `AUTH_*` | 认证/授权 | `AUTH_SESSION_EXPIRED`, `AUTH_QUOTA_EXCEEDED` |
| `NOT_FOUND_*` | 资源不存在 | `NOT_FOUND_OUTFIT`, `NOT_FOUND_POSE` |
| `CONFLICT_*` | 幂等/唯一键冲突 | `CONFLICT_DUPLICATE_PROVIDER` |
| `RATE_*` | 限流 | `RATE_LIMITED_GATEWAY` |
| `UPSTREAM_*` | 上游（new-api）错误透传 | `UPSTREAM_GATEWAY_TIMEOUT`, `UPSTREAM_MODEL_UNAVAILABLE` |

### i18n 消息 key

`message` 字段用英语原文 + `message_key` / `message_params` 字段（跟 new-api 后端对齐），
前端从 `message_key` 查 i18n 字典渲染：

```go
// new-api 后端（参考）
common.ApiErrorT(c, "Insufficient quota: need {{need}}, have {{have}}",
  map[string]any{"need": need, "have": have})
// → {"success": false, "message": "Insufficient quota...",
//    "message_key": "Insufficient quota: need {{need}}, have {{have}}",
//    "message_params": {"need": 100, "have": 20}}
```

Next.js 侧直接返回英语 message，前端翻译层同时查 `message_key` 和兜底 `message`。

## 3. new-api App 级接口（套餐 / 商品 / 额度池）

OAuth App 有自己的套餐、商品和额度池，全部按 `app_id` 归属。这些接口不走上面的
Next.js 信封约定，直接对接时按本节处理。

### 3.1 OAuth App 级接口（`/oauth/*`）

- 鉴权：`Authorization: Bearer <OAuth access token>`。`GET` 需要 `api:read`，写操作需要
  `api:write`。
- 作用域来自 token 的 `client_id`；body 里的 `app_id` 只用于拒绝不匹配的请求，不能用来
  选择另一个 App。其他 App 的套餐/商品不可见、不可购买。
- 响应不是 `{"success", "message", "data"}`，而是 OAuth 错误格式：HTTP 状态 +
  `{"error": "...", "error_description": "..."}`。

| HTTP | `error` | 场景 |
|------|---------|------|
| 401 | `invalid_token` | 缺少、过期或无效的 access token |
| 403 | `insufficient_scope` | token 缺少 `api:read` / `api:write` |
| 403 | `invalid_client` | OAuth App 已停用或不可用 |
| 400 | `invalid_request` | JSON 非法；缺 `plan_id` / `product_id`，或套餐/商品 checkout 缺 `payment_method`（充值省略时默认 `waffo_pancake`，缺 `amount` 才报错） |
| 400 | `unsupported_payment_method` | 该对象不支持此支付方式 |
| 400 | `checkout_failed` | 业务失败：套餐/商品停用、达到购买上限、余额不足、金额越界 |
| 403 | `access_denied` | 套餐/商品属于其他 App，或 `app_id` 与 token 不匹配 |
| 404 | `not_found` | 套餐/商品不存在 |
| 500 | `server_error` | 读取目录或创建支付单时服务端失败 |
| 502 | `checkout_failed` | App 充值的支付网关或上游依赖暂时不可用 |

| 接口 | Scope | 说明 |
|------|-------|------|
| `GET /oauth/userinfo` | `openid`；`api:read` 才有额度 | 新增 `app_quota` / `app_used_quota`（token 所属 App 的额度池），平台 `quota` / `used_quota` 语义不变 |
| `GET /oauth/subscription/plans` | `api:read` | `{"plans":[{"plan":{...}}]}`，仅本 App 的已启用套餐 |
| `POST /oauth/subscription/checkout` | `api:write` | `{"plan_id":1,"payment_method":"balance"}`；余额支付同步返回 `success`，在线支付返回 `pending` + 跳转字段 |
| `GET /oauth/products` | `api:read` | `{"products":[{id,name,description,price_amount,currency,quota_amount,max_purchase_per_user}]}` |
| `POST /oauth/products/checkout` | `api:write` | `{"product_id":1,"payment_method":"balance"}`；余额支付返回 `order_id` / `quota_amount` |
| `POST /oauth/topup/checkout` | `api:write` | `{"amount":10,"payment_method":"balance"}`；`balance` 是**从平台余额划转到 App 额度池**，不是免费发放，余额不足返回 `400 checkout_failed` |

支付方式（OAuth）：套餐 `balance` / 已配置的易支付类型（如 `alipay` / `wxpay`）/ `stripe` / `creem` / `waffo_pancake`（兼容 `epay`，使用第一个已配置的易支付类型）；商品 `balance` / `epay` / `stripe` / `waffo_pancake`；App 充值 `balance` / `epay` / `stripe` / `creem` / `waffo_pancake`。具体方法仍需实例配置且目录返回可用。省略 `payment_method` 时默认 `waffo_pancake`。代理 checkout/preview 仅支持 `waffo_pancake`，不支持订阅。`amount` 是配置的展示单位整数，不是 quota。

App 额度语义：App token 的 relay 预扣、结算、退款只作用于 token 所属 App 的额度池
（以及该 App 的订阅额度），不扣平台 `quota`。`/oauth/userinfo` 的活跃订阅摘要与
`app_quota` 都不能证明某笔订单已支付。后端保存订单标识及可信用户/App 归属，通过目标
实例已支持且有权限的订单查询确认该订单成功后，再刷新余额或订阅；本地订单仅作缓存。
缺少对应查询能力时保持“待确认”，不以余额变化、本地记录或浏览器回跳替代订单终态，
旧部署必须先核对下述 OAuth 订单查询接口能力；本分支已实现，旧部署缺失时不得伪造确认。

OAuth 订单查询合同（`GET /oauth/orders` 和 `/oauth/orders/:order_id`）：OAuth api:read 查询 token 本人、token 固定 App 的充值/商品/订阅订单列表、详情与状态，不要求 dashboard PAT/会话，拒绝跨用户或跨 App 的参数与订单标识，并同步 OAuth 参考 BFF。关闭 OAuth 后该入口拒绝，历史走控制台独立只读入口；均返回 success 信封；列表使用 p/page_size。

checkout 关联合同：订单查询覆盖的每个现有下单分支统一返回稳定 order_id，保留旧字段及同步 success/在线 pending，包括余额充值、余额订阅及 Stripe/Epay；余额订单、归属和资金/权益变更同事务。副作用前持久化按可信操作者/来源/用户/App/类型绑定的幂等键及规范化输入；同键同参数恢复原 ID/快照，改参数冲突，在途/外部结果不明按键恢复或对账，不重复扣款/授予/建单。过期保留原 ID/状态，明确新购才用新键。新 BFF 必须传键，兼容旧客户端缺键请求但不保证可恢复重试，结果不明不能自动重下单。同步参考 BFF、gateway-contract.ts、schema 与可信订单关联存储，调用前保存请求键，丢响应/本地写失败可恢复；同套餐不同键各查各单，禁止按最新订单、金额、URL 或余额猜 ID。上述为本分支扩展，部署时核对服务器能力；旧实例缺 ID 保持待确认，校验先判断合同能力。

### 3.2 管理端接口（admin session 或 admin PAT）

分页参数是 `p` + `page_size`（默认 10，上限 100），不是 §1 的 `page`；`data` 为 new-api
`PageInfo`：`{page, page_size, total, items}`，**没有 `total_pages`**，需要前端自行
`ceil(total / page_size)`。

| 能力 | 接口 |
|------|------|
| 套餐列表（按 App 筛选） | `GET /api/subscription/admin/plans?app_id=<id>`；省略 = 全部套餐，`0` = 仅平台套餐 |
| 创建/更新套餐 | `POST /api/subscription/admin/plans`、`PUT` / `PATCH /api/subscription/admin/plans/:id`，body 内含 `app_id`（`0` = 平台）；正数必须是已存在的 OAuth App。更新是整体替换，漏传 `app_id` 会把套餐移到平台目录 |
| 商品列表 | `GET /api/app-products?app_id=<id>&p=1&page_size=20`；省略或 `0` = 所有 App |
| 商品创建/更新/启停/删除 | `POST /api/app-products`、`PUT /api/app-products/:id`、`PATCH /api/app-products/:id` `{"enabled":bool}`、`DELETE /api/app-products/:id`；创建必须有正数 `app_id`，已产生订单的商品只能停用不能删除 |
| 商品订单 | `GET /api/app-products/orders?app_id=&user_id=&p=&page_size=`、`POST /api/app-products/orders/:trade_no/complete`（确认线下收款后补单，给 App 额度池入账） |
| 用户列表（App 视角） | `GET /api/user/?app_id=<id>`、`GET /api/user/search?...&app_id=<id>`；原生用户保留资金视角；代理用户只含不可变归属该 App 的账户（无钱包为 0），支持 identity_source=proxy/platform |
| 调整额度 | `POST /api/user/manage` `{"id":<user>,"action":"add_quota","mode":"add" / "subtract" / "override","value":<quota>,"app_id":<id>}`；`app_id` 省略或 `0` 调整平台 `quota`，正数只调整该 App 额度池 |

App 用户查询增加代理账户归属过滤：选 A 时只含归属 A 的代理行，包括未建钱包用户，不能混入 B；来源筛选有无都生效，列表/搜索/计数/分页/排序一致。保留旧 OAuth/平台账户资金视角及全局管理员目录，不把钱包记录存在性当账户归属。

管理端仍使用 §2 的 `{"success", "message", "data"}` 信封和 `ApiError` 错误结构；`app_id`
只影响数据范围，不改变鉴权要求。


### Development-branch App settings and marketplace

The current development branch extends the existing admin `/api/oauth-apps/` create/read/update API with `api_key_enabled`, `oauth_enabled`, nullable `quota_per_credit`, `marketplace_visible`, `marketplace_description`, `marketplace_url` and `marketplace_sort`. New calls without OAuth configuration default to proxy-only; legacy callers supplying redirects/scopes without either mode flag keep OAuth-only behavior. Existing rows retain OAuth through the nullable migration default. The management DTO adds `client_secret_configured` (boolean), never the stored secret hash, so toggling OAuth preserves an existing confidential-client credential. Explicit secret rotation keeps its existing endpoint.

`GET /api/app-marketplace` is public and returns the standard success envelope with an array of exactly `id`, `name`, `logo_url`, `description`, `url`. Only enabled, opted-in Apps appear, ordered by display priority then ID. Visit URLs require HTTPS without embedded credentials. Visibility does not grant wallet or authentication permissions. These branch changes are not evidence that proxy provisioning, App-bound keys, delegated checkout or normalized wallet/quote endpoints are available on a target deployment.


### Proxy-first branch API

All `/api/app-proxy/*` routes require admin role plus the PAT scope below. A PAT remains administrative authority, not an App-restricted AI credential. The BFF must fix App/user from verified mappings; never expose this PAT to a browser.

| Method and path | PAT scope | Contract |
|---|---|---|
| POST `/api/app-proxy/users` | `user:write` | `{app_id,issuer,subject}`; atomic recoverable external identity → stable user_id, app_id, identity_source, status |
| GET `/api/app-proxy/users/:user_id/wallet` | `user:read` | `?app_id=`; success envelope, remaining quota, nullable exact credits, subscription_supported=false |
| GET `/api/app-proxy/users/:user_id/keys` | `api_key:read` | `?app_id=&p=&page_size=&status=`; masked keys only |
| POST `/api/app-proxy/users/:user_id/keys` | `api_key:write` | `?app_id=`, Idempotency-Key; issue secret once per recoverable operation; old_token_id stages rotation |
| PUT `/api/app-proxy/users/:user_id/keys/:token_id` | `api_key:write` | status=1/2, optional name/remain_quota; immutable ownership |
| POST `/api/app-proxy/users/:user_id/keys/:token_id/commit-rotation` | `api_key:write` | replacement_key possession; revoke old only after saving/verifying new. Limited rotation returns409 |
| GET `/api/app-proxy/users/:user_id/catalog` | `billing:read` | App products/payment_methods; empty plans, subscription_supported=false; raw object |
| GET `/api/app-proxy/users/:user_id/pricing` | `user:read` | App whitelist and quota_per_credit; raw pricing contract |
| GET `/api/app-proxy/users/:user_id/logs` | `log:read` | Fixed user/App redacted logs; legacy log-list shape |
| POST `/api/app-proxy/users/:user_id/topup-preview` | `billing:read` | `{app_id,amount,payment_method:"waffo_pancake"}`; success envelope with money/currency/quota/credits |
| POST `/api/app-proxy/users/:user_id/checkout` | `billing:write` | Same explicit app_id, purchase_type=topup/product, amount/product_id; Idempotency-Key required; stable order_id and frozen snapshot |
| GET `/api/app-proxy/users/:user_id/orders` | `billing:read` | `?app_id=&p=&page_size=&keyword=`; success envelope with items/total |
| GET `/api/app-proxy/users/:user_id/orders/:order_id` | `billing:read` | Exact authorized order; success envelope; pending/processing/reconciliation_required never prove payment |

OAuth equivalents: `GET /oauth/wallet`, `/oauth/orders`, `/oauth/orders/:order_id` need api:read and fix both principals from the token. `POST /oauth/topup/preview` needs api:read and supports Pancake preview. Console equivalents are under `/api/user/self/` (`wallet`, `app-catalog`, `app-checkout`, `app-topup-preview`, `orders`). Legacy userinfo meanings remain unchanged.

The reference `/api/generate/quote` adds estimated_quota, estimated_credits (string|null), quota_per_credit and a server-bound confirmation digest. Legacy credits remains raw quota. Estimates support the documented fixed/linear expression subset only; unsupported expressions fail explicitly. Settlement fields remain null until actual correlated gateway charges/refunds are known.

Payment callbacks verify signature, paid status, frozen amount/currency and expiry before credit. Amount/currency mismatches or late unpaid orders enter reconciliation_required with the durable order ID. Valid duplicate paid notifications remain idempotent after expiry. Operators investigate provider records and the frozen intent before any existing authorized manual correction; never blindly replay provider creation or treat a return URL as success. Epay scoped products/subscriptions require CNY before checkout; Creem scoped requests use plan currency. Stripe/Pancake product currencies must match configured plan/product currency.

Scoped Stripe top-up validates the configured active USD one-time per-unit Price and whole purchased quantities before creating a provider session; its frozen money is price × quantity, with promotions disabled. Scoped product Stripe checkouts also disable promotions. Legacy orders without snapshots never acquire today's credit ratio; unknown original currencies remain empty and the UI displays No Data rather than inventing USD or labeling unit counts as paid money.

## Specified-user quota validity and rotation cancellation

All paths below start with `/api/app-proxy/users/:user_id`, require admin role, existing Casbin authorization and `?app_id=<fixed App>`. Read scope is `api_key:read`; mutations require `api_key:write`. The owner is the target proxy user, not the PAT actor. Reject cross-App/cross-user rules and keys. Unknown input fields/ownership metadata are rejected. These parameters do not make a globally authorized admin PAT App-restricted.

| Method/path | Contract |
| --- | --- |
| GET `/quota-rules` | `p`, `page_size`; success envelope `data.items,total,server_time` |
| POST `/quota-rules` | Rule input below; absent dates create unbounded endpoints |
| PUT `/quota-rules/:id` | Save full rule; omitted dates preserve, explicit null clears |
| DELETE `/quota-rules/:id` | Requires no bound keys; original usage rows survive |
| PUT `/keys/:token_id/quota-rule` | Exactly `{rule_id}`; 0 unbinds; pending source rotations block binding |
| POST `/keys/:token_id/cancel-rotation` | Token is replacement; idempotent cancelled status; committed returns409 `operation_conflict` |

Rule input: `name`, optional `description`, `enabled`, `quota_limit_enabled`, `quota_period` (daily/weekly/monthly), `quota_limit`, `time_window_enabled`, `time_window_timezone` (UTC/Asia/Shanghai), `allowed_weekdays` (JSON string of ISO weekdays), optional `valid_from`/`valid_until` (null or integer 1..253402300799, start < end). Enabled rules enforce `[valid_from,valid_until)` independently of the quota/weekday switches, including unlimited keys, trusted/free requests and tasks. `validity_status` is disabled/scheduled/effective/expired and describes absolute time only, not spendability. Rejected new relay calls return403 `rule_not_started`/`rule_expired` without retry; already admitted work keeps its original budget snapshot and usage row.

POST `/keys` additionally accepts `quota_rule_id` for atomic creation and binding. The operation fingerprint includes a nonzero rule ID. Changing the binding uses the dedicated route. Keys list includes rule/usage summaries and optional `rotation_status` pending/committed/cancelled. Rule-bound rotation is rejected even when the rule is disabled. Cancelled replacement commit, enable and idempotent creation replay all fail409 `rotation_cancelled`, with no secret returned. Cleanup reads/unbind/revoke/cancel remain authorized after disabling the App; creation/update/binding do not.

Pre-submit task private data persists `snapshot_version=1`, `quota_limit_enabled`, `quota_limit` and original RuleID/UsageID/Amount. Supplemental reservation uses the frozen limit against aggregate original usage, even after edits/deletion; actual settlement is never clipped. Legacy snapshots fail positive supplemental reservation with `reservation_snapshot_unavailable` while retaining settlement/refund compatibility. A crash with uncertain upstream outcome leaves `SUBMITTING` for reconciliation, never automatic resubmission/refund.

Reference BFF admin allowlist: `/api/admin/gateway-proxy/:native_user_uuid/quota-rules[/:id]`, `keys/:id/quota-rule`, `keys/:id/cancel-rotation`. Every route checks the existing server admin guard, mutation origin, protected proxy mapping and configured App. Consumer management allowlists deliberately do not expose membership writes. Keep payment verification, entitlement orders, refund/replay ordering and wallet funding in the App's verified business process; these rules do not implement new-api subscriptions.
