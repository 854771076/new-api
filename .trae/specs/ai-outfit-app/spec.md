# Spec: AI 换装应用 (ai-outfit)

## 问题与目标

### 问题
为 AI 换装产品搭建完整的技术栈，使产品团队能够：
1. 维护官方素材库（服装 1407 套 + 姿势 1439 张）并支持用户上传；
2. 让用户选择人物 + 服装单品/套装 + 姿势，调用 AI 生图完成换装；
3. 通过 new-api OAuth 统一用户身份，复用其充值、积分、消费和订阅能力；
4. 在独立管理后台完成常用运营模型的增删改查筛选排序。

### 用户
- 终端用户：通过 new-api OAuth 登录，浏览素材库、创建换装作品、查看积分和历史；
- 运营/管理员：审核素材、管理服装/姿势/人物数据、管理套餐和价格、查看订单。

### 目标
- 一个可运行的 Next.js App Router + Supabase 外部应用，接入 new-api 作为身份和 AI 网关；
- 素材库从本地 `pack.json` 批量入库 Supabase Storage + Postgres；
- 换装工作流：人物上传/选择 → 服装（单品/套装）选择 → 姿势选择 → AI 生图 → 作品保存；
- 用户端页面：`/login`、`/wardrobe`（服装库）、`/poses`（姿势库）、`/characters`（我的人物）、`/studio`（换装工作室）、`/history`（作品历史）、`/profile`（积分/充值/消费）；
- 管理后台：独立路由树 `/(admin)`，Ant Design 5 + Refine，覆盖服装/姿势/人物/用户/作品/套餐/订单的 CRUD。

### 非目标
- 不实现 new-api 本身的改动（除非为修复技能 bug）；
- 不实现订阅/充值的支付渠道（重定向到 new-api 仪表板）；
- 不做图像生成模型训练；
- 不做移动端打包。

## 技术栈

| 层 | 选择 | 理由 |
|----|------|------|
| 框架 | Next.js 16 App Router | new-api-oauth-integration 技能默认；Server Components 优先 |
| 语言 | TypeScript strict | 严格类型 |
| UI | Tailwind CSS v4 + DaisyUI 5（消费端） | 技能默认 |
| 管理后台 | Ant Design 5 + `@refinedev/core` | 技能 Step 8 规范，独立路由树 |
| 后端 | Supabase (Postgres + RLS + Storage + Edge Functions) | 技能 Step 5 规范 |
| 身份 | Supabase Auth + new-api OAuth (PKCE, public client) | 技能 Step 2.4 规范 |
| AI 网关 | new-api `/v1/*` (OAuth access token) | 技能 Step 3 规范 |
| 包管理 | Bun | 技能默认 |
| 部署 | Vercel | 技能 Step 7 规范 |

## 数据模型 (Supabase Postgres + RLS)

### 表结构

```sql
-- 运营素材：服装套装
create table public.outfits (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,              -- OAuth app id，隔离多租户
  provider_id text not null,            -- 素材来源 id (如 AUT-0001)
  category text not null,               -- 春季/夏季/秋季/冬季/内衣/派对礼服/泳装/性感服饰
  tags text[] default '{}',
  name_zh text not null,
  name_en text,
  description_zh text,
  description_en text,
  keywords text[] default '{}',
  bucket text not null,                 -- Supabase Storage bucket
  thumb_path text not null,             -- 缩略图
  preview_path text not null,           -- 预览图
  source text default 'official',       -- official | user
  user_id uuid references auth.users,   -- 当 source='user' 时必填
  sort_order int default 0,
  is_active boolean default true,
  created_at timestamptz default now(),
  updated_at timestamptz default now()
);
create index on public.outfits (client_id, category, is_active);
create index on public.outfits (client_id, sort_order);

-- 运营素材：姿势
create table public.poses (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  provider_id text not null,            -- 如 full-sexy-P001-front
  pose_set text not null,               -- P001 一个姿势组
  system text not null,                 -- 上半身单人/全身单人·标准系/...
  category text not null,               -- 站姿/坐姿/...
  angle text not null,                  -- 正面/俯视/侧面/斜侧/仰视
  name_zh text not null,
  name_en text,
  description_zh text,
  description_en text,
  bucket text not null,
  thumb_path text not null,
  preview_path text not null,
  sort_order int default 0,
  is_active boolean default true,
  created_at timestamptz default now(),
  updated_at timestamptz default now()
);
create index on public.poses (client_id, system, category, angle, is_active);

-- 用户上传的人物/参考图
create table public.characters (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  user_id uuid not null references auth.users,
  name text not null,
  bucket text not null,
  image_path text not null,
  cover_path text,
  description text,
  is_public boolean default false,
  created_at timestamptz default now(),
  updated_at timestamptz default now()
);
create index on public.characters (client_id, user_id);

-- AI 换装生成记录
create table public.outfit_generations (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  user_id uuid not null references auth.users,
  character_id uuid references public.characters,
  outfit_ids uuid[] default '{}',       -- 选中的服装
  pose_id uuid references public.poses,
  prompt text,
  model text not null,                  -- new-api 模型名
  status text not null,                 -- pending/succeeded/failed
  result_bucket text,
  result_path text,
  error text,
  gateway_request_id text,              -- new-api 网关日志关联
  gateway_usage jsonb,                  -- token/积分消耗
  created_at timestamptz default now(),
  updated_at timestamptz default now()
);
create index on public.outfit_generations (client_id, user_id, created_at desc);
create index on public.outfit_generations (client_id, status);

-- 运营管理：套餐（管理后台读写，前端只读）
create table public.price_plans (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  name_zh text not null,
  name_en text,
  description text,
  price_quota bigint not null,          -- 单次生成消耗 quota
  model text not null,
  is_active boolean default true,
  sort_order int default 0,
  created_at timestamptz default now(),
  updated_at timestamptz default now()
);

-- 管理员授权
create table public.app_admins (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references auth.users unique,
  role text not null default 'admin',   -- owner | admin
  created_by uuid references auth.users,
  created_at timestamptz default now()
);

-- current_app_client_id() 安全定界函数
create or replace function public.current_app_client_id()
returns text language sql stable security definer set search_path = public as $$
  select coalesce((auth.jwt() -> 'app_metadata' ->> 'client_id'), '');
$$;
```

### Storage buckets
- `media-official`（private）：官方服装/姿势图片，RLS 限制 `client_id`；
- `media-user`（private）：用户上传人物图 + 生成结果，RLS 限制 `auth.uid()` + `client_id`。

### RLS 策略（核心）
- 每张业务表启用 `enable row level security`；
- 所有业务表强制 `client_id = current_app_client_id()`；
- `outfits`/`poses`：authenticated 用户可读；admin 可写；
- `characters`/`outfit_generations`：owner 可读写；admin 可读写；
- `app_admins`：仅 owner/admin 可读写。

## 核心流程

### 用户登录 (Supabase + new-api OIDC)
```
浏览器 → /login → supabase.auth.signInWithOAuth({ provider: 'custom:newapi' })
  → Supabase Auth → new-api /oauth/authorize
  → 用户同意 → new-api 302 /auth/v1/callback?code
  → Supabase 换 token → 浏览器 cookie session + provider_token
```

### 换装生成
```
/ studio → 选人物 → 选服装 → 选姿势 → 生成
  → Route Handler POST /api/generate
  → 读 gateway access_token（内存，<60s 自动刷新）
  → POST new-api /v1/images/generations（reference 风格）
  → 轮询/等待结果 → 下载 → 上传 Supabase Storage
  → 写 outfit_generations 表 → 返回结果
```

### 用户查看积分/充值
- 积分余额：`GET /api/user/self` → 前端显示 `quota`；
- 消费明细：重定向到 new-api 仪表板 `/app.html#/topup` 和 `/app.html#/log`；
- 作品历史：`GET /api/outfit_generations` PostgREST。

## 需求规格 (Acceptance Criteria)

### AC-1 环境与脚手架 [rule]
- 项目能在本地 `bun run dev` 启动，`bun run build` 生产构建成功；
- Supabase 本地 `supabase db push` 成功，RLS 策略生效；
- new-api OAuth 应用已创建，Supabase `custom:newapi` provider 已配置并能完成登录；
- `.env.example` 覆盖所有运行时变量，self-check 脚本通过。

### AC-2 素材入库 [rule]
- 脚本读取 `asset-packs/costume-library/pack.json`，解析 `items[]`，提取 `id/name/description/category/keywords/tags/files`；
- 图片上传 Supabase Storage bucket `media-official`，路径 `{client_id}/outfits/{id}.webp` 和 `{client_id}/outfits-thumbs/{id}.webp`；
- 1407 条 outfit + 1439 条 pose 全部成功写入 Postgres，关键字段（client_id, provider_id, bucket, thumb_path, preview_path, category/system/angle）非空且可索引；
- 脚本幂等：重复执行不产生重复记录（按 `provider_id + client_id` upsert）。

### AC-3 消费端页面 [rule]
- `/login` 按钮触发 Supabase OAuth 流程，登录后跳转 `/studio`；
- `/studio` 三栏选择器：人物列表 → 服装列表（支持按季节/品类筛选）→ 姿势列表（按体系/分类/角度筛选）→ 「开始生成」按钮；
- `/wardrobe` 展示所有官方服装（分页、分类筛选、按 sort_order 排序）；
- `/poses` 展示姿势库（三维筛选）；
- `/characters` 登录用户可上传新人物（图片校验 MIME + 尺寸限制），编辑/删除自己的人物；
- `/history` 展示当前用户作品历史（状态、缩略图、时间、错误信息）；
- `/profile` 显示：显示名、new-api quota、subscription 状态、消费统计，含指向 new-api 仪表板充值/日志的链接。

### AC-4 AI 换装调用 [rule]
- Route Handler `POST /api/generate` 校验 Supabase session，从 session `provider_token` 取 OAuth access token；
- 调用前检查 `exp`，<60s 时刷新；401 重试一次；invalid_grant 签退；
- 构造 `/v1/images/generations` 请求（含人物 + 服装 + 姿势作为 reference），成功后下载结果并上传 `media-user` bucket；
- 写入 `outfit_generations`：`status=succeeded`，写入 `gateway_request_id` 和 `gateway_usage`；
- 失败时写入 `status=failed` + `error`，返回 4xx/5xx 带错误信息。

### AC-5 管理后台 [rule]
- `/(admin)/` 独立路由树，根 layout 挂载 AntdRegistry + ConfigProvider；
- 每个 admin Route Handler 调 `requireAdmin()`（`supabase.auth.getUser()` + `app_admins` 表查询）；
- 六个 Refine resource：Outfits、Poses、Characters、Plans、Users、Generations；
- 每个列表页支持分页、筛选（model/keyword）、排序（created_at desc）；
- 创建/编辑表单用 antd Form + Zod 校验；
- 用户首登且 `sub = INITIAL_ADMIN_SUB` 时，自动向 `app_admins` 插入一行 `role='owner'`。

### AC-6 身份与安全 [rule]
- OAuth 流程全程 state 校验、PKCE 强制、nonce 生成；
- access token 仅在内存（客户端）/server-only 模块（Route Handler）；不写入数据库、日志、sessionStorage；
- 刷新 token 每次轮换，`invalid_grant` 清空本地状态；
- Supabase Auth 回调使用 `SameSite=Lax` HttpOnly cookie；
- RLS 策略能阻止跨 client_id 读，阻止匿名写；
- 所有素材访问走 signed URL（`storage.objects.create_signed_url`），过期时间 ≤ 3600s。

### AC-7 new-api OAuth 集成验证 [rule]
- 新建 OAuth app 时 `ai_model_whitelist` 约束生效（空 = 全部模型，列表 = 白名单）；
- Supabase 自定义 provider 使用 `client_secret=ignored-by-public-client`（public client），`pkce_enabled=true`，`email_optional=true`；
- `/api/user/self` 能读到 quota，余额显示正确；
- AI relay `/v1/*` 使用 OAuth access token，dashboard `/api/*` 不使用 OAuth token（禁止）。

### AC-8 可维护性 [rubric]
- 单路由/组件文件 ≤ 400 行；分离 lib/、app/、components/ 清晰；
- Zod schema 集中在 `lib/schemas/`；
- 所有数据访问走 Supabase client（browser / server / admin 三层）；
- 关键业务逻辑有 Edge Function（user-sync）；
- `tsconfig.json` strict + Next.js 严格检查；
- 测试覆盖：至少 Route Handler 层 `requireAdmin` 防穿透 + 401 重试分支。

**Pass threshold ≥ 7/10 分**，低锚点：文件乱塞、无 schema 校验、token 泄漏；高锚点：清晰分层、schema 复用、Edge Function 职责明确。

## 假设与开放问题

| 项 | 当前假设 | 待确认 |
|----|---------|--------|
| 换装模型 | 使用 new-api 中可用的 `gpt-image-1` 或 `playground-v2.5-1024px-aesthetic` 带 reference | 用户确认具体模型 |
| 姿势库 zip 解包 | 本地产出 thumbs + img 后再入库（pack.json 已列路径） | 不需要先解 zip，已有 img/ 目录 |
| Storage bucket 名 | `media-official` + `media-user`，private | - |
| Supabase 项目 | 新创建或复用已有 | 部署时确认 |
| 首管权限 | OAuth app `created_by` → `INITIAL_ADMIN_SUB`，首次登录自动 grant | - |
| new-api 环境 | 已本地启动（localhost:3000），有 admin 账号 | - |
