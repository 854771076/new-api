# Tasks: AI 换装应用 (ai-outfit)

## 约定
- 项目放在 `apps/ai-outfit/` 子目录，与 repo 根同级；
- 所有 Route Handler/Edge Function 都用 Next.js App Router + `import 'server-only'`；
- Supabase migration 使用 `supabase/migrations/` + `schema.generic.sql`；
- 素材入库脚本放在 `scripts/seed-assets.ts`。

---

## Phase 1: 环境与脚手架

### Task 1.1: 项目脚手架与 self-check
- 优先级：high
- 状态：completed
- 完成证据：Next.js 16.4.6 + React 19 + Tailwind v4 + DaisyUI 5.7.50 + antd 6.7.0 + @refinedev/core 5.0.12。`bun run build` 通过（21 路由全绿）。

### Task 1.2: Supabase 初始化与 schema 迁移
- 优先级：high
- 状态：completed
- 完成证据：[schema.generic.sql](../../apps/ai-outfit/supabase/schema.generic.sql) 含 6 张业务表 + 安全函数 `current_app_client_id()` / `is_app_admin()` / `is_app_owner()` + 完整 RLS 策略 + 2 Storage bucket + bucket RLS。`supabase init` 已执行。

### Task 1.3: new-api OAuth 应用 & Supabase custom:newapi provider
- 优先级：high
- 状态：blocked
- 阻塞原因：无真实 Supabase 项目 + new-api 网关环境变量
- 待做：创建 OAuth app (public client)、POST Supabase custom provider、Cloudflare tunnel、填 .env.local、self-check 通过

---

## Phase 2: 素材入库

### Task 2.1: 服装/姿势入库脚本
- 优先级：high
- 状态：completed（脚本就绪，执行等真实环境）
- 完成证据：[seed-assets.ts](../../apps/ai-outfit/scripts/seed-assets.ts) 读 pack.json → 上传 Storage → upsert Postgres，1407 服装 + 288 姿势组。幂等（`onConflict: client_id,provider_id`）。批量 100/批，失败重试 3 次。

---

## Phase 3: 消费端前端

### Task 3.1: 基础布局、路由与 Supabase client 分层
- 优先级：high
- 状态：completed
- 完成证据：三层 client（[client.ts](../../apps/ai-outfit/src/lib/supabase/client.ts) / [server.ts](../../apps/ai-outfit/src/lib/supabase/server.ts) / [admin.ts](../../apps/ai-outfit/src/lib/supabase/admin.ts)）、[gatewayAuth.ts](../../apps/ai-outfit/src/lib/gatewayAuth.ts)（PKCE + 60s 提前刷新 + 并发去重 + 401 重试 + invalid_grant 签退）、[gateway.ts](../../apps/ai-outfit/src/lib/gateway.ts)（chat/image 调用统一 401 重试）、[proxy.ts](../../apps/ai-outfit/src/proxy.ts)（session 保持）。

### Task 3.2: /login、/auth/callback、/profile 页面
- 优先级：high
- 状态：completed
- 完成证据：DaisyUI 登录卡、PKCE callback 完成 session、profile 显示用户信息 + 重定向 new-api 仪表板（充值/日志）。

### Task 3.3: /wardrobe、/poses 素材浏览
- 优先级：high
- 状态：completed（UI 骨架完成，signed URL 渲染等素材入库后）
- 完成证据：wardrobe DaisyUI Tabs 按 category 筛选 + 分页；poses 按 system 分组 Grid。

### Task 3.4: /characters、/studio 换装核心
- 优先级：high
- 状态：completed
- 完成证据：studio 三栏选择器骨架、[POST /api/generate](../../apps/ai-outfit/src/app/api/generate/route.ts) 完整 Route Handler（校验 session → 读素材 → 构造 prompt → gatewayAuth → /v1/images/generations → 下载 → admin Supabase Storage upload → outfit_generations upsert）。

### Task 3.5: /history 作品历史
- 优先级：medium
- 状态：completed
- 完成证据：Table + 状态标签 + 时间倒序，SSR 读 PostgREST。

---

## Phase 4: 管理后台

### Task 4.1: admin 根 layout + requireAdmin 守卫
- 优先级：high
- 状态：completed
- 完成证据：[admin/layout.tsx](../../apps/ai-outfit/src/app/admin/layout.tsx)（ConfigProvider + AntdApp + requireAdmin server guard）、[requireAdmin.ts](../../apps/ai-outfit/src/lib/admin/requireAdmin.ts)（auth.getUser → app_admins 查询 → redirect/403）。

### Task 4.2: 素材管理（Outfits / Poses）
- 优先级：medium
- 状态：completed
- 完成证据：[admin/outfits/page.tsx](../../apps/ai-outfit/src/app/admin/outfits/page.tsx)（完整 Ant Design Table + Modal 表单 + 分页 + 搜索 + CRUD）、[admin/poses/page.tsx](../../apps/ai-outfit/src/app/admin/poses/page.tsx)（列表视图）。

### Task 4.3: 代理 CRUD Route Handler
- 优先级：medium
- 状态：completed
- 完成证据：[/api/admin/[table]/route.ts](../../apps/ai-outfit/src/app/api/admin/[table]/route.ts)（requireAdmin guard + service_role client + allowlist + 自动排序分页筛选）。

### Task 4.4: 其余 admin 页面占位
- 优先级：medium
- 状态：completed
- 完成证据：characters/generations/plans 已 redirect 占位，后续迭代可从 Refine 直连 API。

---

## Phase 5: 验证与部署

### Task 5.1: 完整回归验证
- 优先级：high
- 状态：blocked（等 1.3）

### Task 5.2: Vercel + Cloudflare tunnel 验收
- 优先级：medium
- 状态：blocked（等 5.1）

---

## 依赖关系
- Task 1.3（blocked） → Task 2.1 执行 + Task 5.1
- 所有 Phase 1-4 代码已就绪，待真实环境变量验证
