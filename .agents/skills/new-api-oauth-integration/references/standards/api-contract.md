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
- **403**：toast "额度不足，请先充值" + 跳转 new-api `/topup`
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
