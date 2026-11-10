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
import { createAdminSupabase } from '@/lib/supabase/admin';

export async function GET(request: Request) {
  const url = new URL(request.url);
  const page = Math.max(1, parseInt(url.searchParams.get('page') ?? '1', 10));
  const pageSize = Math.min(100, Math.max(1, parseInt(url.searchParams.get('page_size') ?? '20', 10)));
  const sort = url.searchParams.get('sort') ?? 'created_at';
  const order = url.searchParams.get('order') === 'asc' ? 'asc' : 'desc';
  const category = url.searchParams.get('category');

  const supabase = createAdminSupabase();
  let q = supabase.from('outfits').select('*', { count: 'exact' })
    .eq('is_active', true)
    .order(sort, { ascending: order === 'asc' })
    .range((page - 1) * pageSize, page * pageSize - 1);
  if (category) q = q.eq('category', category);

  const { data, count, error } = await q;
  if (error) return NextResponse.json({ success: false, message: error.message }, { status: 500 });

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
- **403**：toast "额度不足，请先充值" + 打开 App 充值（`POST /oauth/topup/checkout`）；只有平台额度不足才跳 newapi `/topup`。App 级接口的 403 还可能是 `access_denied` 或 `app_task_unsupported`，见 §3，不要一律当成额度不足
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
| 403 | `app_task_unsupported` | 用 App token 调异步任务接口（体为 `{"code","message","data"}`，不是 OAuth 错误格式） |

| 接口 | Scope | 说明 |
|------|-------|------|
| `GET /oauth/userinfo` | `openid`；`api:read` 才有额度 | 新增 `app_quota` / `app_used_quota`（token 所属 App 的额度池），平台 `quota` / `used_quota` 语义不变 |
| `GET /oauth/subscription/plans` | `api:read` | `{"plans":[{"plan":{...}}]}`，仅本 App 的已启用套餐 |
| `POST /oauth/subscription/checkout` | `api:write` | `{"plan_id":1,"payment_method":"balance"}`；余额支付同步返回 `success`，在线支付返回 `pending` + 跳转字段 |
| `GET /oauth/products` | `api:read` | `{"products":[{id,name,description,price_amount,currency,quota_amount,max_purchase_per_user}]}` |
| `POST /oauth/products/checkout` | `api:write` | `{"product_id":1,"payment_method":"balance"}`；余额支付返回 `order_id` / `quota_amount` |
| `POST /oauth/topup/checkout` | `api:write` | `{"amount":10,"payment_method":"balance"}`；`balance` 是**从平台余额划转到 App 额度池**，不是免费发放，余额不足返回 `400 checkout_failed` |

支付方式：套餐 `balance` / `epay` / `stripe` / `creem` / `waffo_pancake`；商品 `balance` /
`waffo_pancake`；App 充值 `balance` / `waffo_pancake`（省略 `payment_method` 时默认
`waffo_pancake`）。`amount` 是配置的展示单位整数，不是 quota。

App 额度语义：App token 的 relay 预扣、结算、退款只作用于 token 所属 App 的额度池
（以及该 App 的订阅额度），不扣平台 `quota`。目前没有 OAuth 接口读取 App 订阅状态或
App 订单历史：充值和商品可用重新读 `/oauth/userinfo` 的 `app_quota` 确认到账，套餐只能
依赖本地订单记录。

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
| 用户列表（App 视角） | `GET /api/user/?app_id=<id>`、`GET /api/user/search?...&app_id=<id>`；返回的每个用户带该 App 的 `app_quota` / `app_used_quota`（无钱包为 0） |
| 调整额度 | `POST /api/user/manage` `{"id":<user>,"action":"add_quota","mode":"add" / "subtract" / "override","value":<quota>,"app_id":<id>}`；`app_id` 省略或 `0` 调整平台 `quota`，正数只调整该 App 额度池 |

管理端仍使用 §2 的 `{"success", "message", "data"}` 信封和 `ApiError` 错误结构；`app_id`
只影响数据范围，不改变鉴权要求。
