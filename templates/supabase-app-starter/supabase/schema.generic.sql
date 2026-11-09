-- ============================================================================
-- supabase/schema.sql — Supabase 通用表结构 + RLS（可反复执行，所有 CREATE
-- 用 IF NOT EXISTS，所有策略只作用于新建表）。
-- ============================================================================
-- 约定：所有业务表携带 client_id（固定字符串，来自脚手架 VITE_GATEWAY_CLIENT_ID）。
-- RLS 策略同时匹配 auth.uid() = 当前会话用户 AND client_id = App 固定值，
-- 保证同一 Supabase 项目可以被多个 App 复用而不互相穿透。
-- ============================================================================

-- ---------------- 通用 App 配置表 ----------------
-- 每个 App 在网关侧已有独立 client_id；在 Supabase 侧 client_id 只是业务表的
-- 普通字段，RLS 强制把所有写入/读取限制在一个固定 client_id 上。
-- 单行表：id 固定为 1，避免 `limit 1` 取到不确定的行。
create table if not exists public.app_config (
  id           int primary key default 1 check (id = 1),
  client_id    text not null,            -- 网关 OAuthApp.client_id（如 nac_xxxxx）
  app_name     text,
  created_at   timestamptz not null default now()
);

-- ---------------- 用户扩展表 ----------------
-- 当 Supabase Auth 通过 OIDC 把网关用户同步过来时，Edge Function user-sync
-- 会在 auth.users.app_metadata.gateway_user_id 里写入网关侧的 user_id。
create table if not exists public.user_profiles (
  id                 uuid primary key references auth.users(id) on delete cascade,
  gateway_user_id    int unique,           -- 网关 User.Id；Edge Function 写入
  display_name       text,
  client_id          text not null,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now()
);

create index if not exists idx_user_profiles_gateway_user_id
  on public.user_profiles(gateway_user_id);
create index if not exists idx_user_profiles_client_id
  on public.user_profiles(client_id);

-- ---------------- AI 用量记录（业务侧只读副本） ----------------
-- 网关侧真实用量在网关自己的 model.Log 里；这里留一份业务能直接读到的记录
-- （可选地由 Edge Function webhook 从网关同步）。脚手架最小链路里这张表
-- 只用来展示"我调用过哪些模型"，所有额度判断仍以网关为准。
create table if not exists public.ai_generations (
  id           uuid primary key default gen_random_uuid(),
  user_id      uuid not null references auth.users(id) on delete cascade,
  client_id    text not null,
  model        text not null,
  prompt       text,
  response     text,
  created_at   timestamptz not null default now()
);

create index if not exists idx_ai_generations_user_id
  on public.ai_generations(user_id);
create index if not exists idx_ai_generations_client_id
  on public.ai_generations(client_id);
create index if not exists idx_ai_generations_created_at
  on public.ai_generations(created_at desc);

-- ---------------- 启用 RLS ----------------
alter table public.app_config        enable row level security;
alter table public.user_profiles     enable row level security;
alter table public.ai_generations    enable row level security;

-- ---------------- RLS 策略 ----------------
-- 所有策略里 client_id 都通过一个 security definer 函数读 app_config 的唯一行
-- 来取，避免把字符串硬编码在策略里。多 App 共用同一个 Supabase 项目时改
-- app_config 即可切换，不需要改策略本身。

create or replace function public.current_app_client_id()
returns text
language sql stable
security definer set search_path = public, pg_temp
as $$
  select client_id from public.app_config where id = 1;
$$;

-- 该函数只服务于 RLS：匿名用户无权调用，已登录用户与 service_role 可调用。
revoke execute on function public.current_app_client_id() from public;
revoke execute on function public.current_app_client_id() from anon;
grant execute on function public.current_app_client_id() to authenticated, service_role;

-- app_config：任意已登录用户可读；auth 已通过 OIDC 校验过身份。
drop policy if exists "app_config readable by all authenticated users"
  on public.app_config;
create policy "app_config readable by all authenticated users"
  on public.app_config for select
  using (auth.role() = 'authenticated');

drop policy if exists "app_config only editable by service role"
  on public.app_config;
create policy "app_config only editable by service role"
  on public.app_config for all
  using (auth.role() = 'service_role');

-- user_profiles：客户端只读自己的行。写入（尤其 gateway_user_id）只能由
-- service_role / Edge Function 完成，防止用户改写自己的网关身份映射。
drop policy if exists "user_profiles own row read"
  on public.user_profiles;
create policy "user_profiles own row read"
  on public.user_profiles for select
  using (id = auth.uid() and client_id = public.current_app_client_id());

drop policy if exists "user_profiles own row insert/update"
  on public.user_profiles;
drop policy if exists "user_profiles own row" on public.user_profiles;

-- 表级授权兜底：撤销客户端所有写权限，只保留 SELECT。
revoke all on public.user_profiles from anon, authenticated;
grant select on public.user_profiles to authenticated;

-- ai_generations：读自己 + 写自己。
drop policy if exists "ai_generations own row read"
  on public.ai_generations;
create policy "ai_generations own row read"
  on public.ai_generations for select
  using (user_id = auth.uid() and client_id = public.current_app_client_id());

drop policy if exists "ai_generations own row insert"
  on public.ai_generations;
create policy "ai_generations own row insert"
  on public.ai_generations for insert
  with check (user_id = auth.uid() and client_id = public.current_app_client_id());
