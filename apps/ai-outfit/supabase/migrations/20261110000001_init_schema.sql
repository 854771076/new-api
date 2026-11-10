-- supabase/schema.generic.sql — ai-outfit 完整数据库 + RLS + Storage。
-- 适用于 Supabase Postgres；先于任何应用部署执行。

-- ========== 0. 工具函数 ==========
-- 读取 JWT app_metadata.client_id；security definer 让 RLS 能查询 auth.users 元数据。
create or replace function public.current_app_client_id()
returns text language sql stable security definer set search_path = public as $$
  select coalesce((auth.jwt() -> 'app_metadata' ->> 'client_id'), '');
$$;

-- ========== 1. 业务表 ==========

-- 运营素材：服装套装
create table if not exists public.outfits (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  provider_id text not null,
  category text not null,
  tags text[] default '{}',
  name_zh text not null,
  name_en text,
  description_zh text,
  description_en text,
  keywords text[] default '{}',
  bucket text not null,
  thumb_path text not null,
  preview_path text not null,
  source text not null default 'official' check (source in ('official','user')),
  user_id uuid references auth.users on delete set null,
  sort_order int not null default 0,
  is_active boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (client_id, provider_id)
);
create index if not exists outfits_client_cat_act on public.outfits (client_id, category, is_active);
create index if not exists outfits_client_sort on public.outfits (client_id, sort_order);

-- 运营素材：姿势
create table if not exists public.poses (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  provider_id text not null,
  pose_set text not null,
  system text not null,
  category text not null,
  angle text not null,
  name_zh text not null,
  name_en text,
  description_zh text,
  description_en text,
  bucket text not null,
  thumb_path text not null,
  preview_path text not null,
  sort_order int not null default 0,
  is_active boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (client_id, provider_id)
);
create index if not exists poses_client_filters on public.poses (client_id, system, category, angle, is_active);

-- 用户上传的人物/参考图
create table if not exists public.characters (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  user_id uuid not null references auth.users on delete cascade,
  name text not null,
  bucket text not null,
  image_path text not null,
  cover_path text,
  description text,
  is_public boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists characters_client_user on public.characters (client_id, user_id);

-- AI 换装生成记录
create table if not exists public.outfit_generations (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  user_id uuid not null references auth.users on delete cascade,
  character_id uuid references public.characters on delete set null,
  outfit_ids uuid[] not null default '{}',
  pose_id uuid references public.poses on delete set null,
  prompt text,
  model text not null,
  status text not null check (status in ('pending','succeeded','failed')),
  result_bucket text,
  result_path text,
  error text,
  gateway_request_id text,
  gateway_usage jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists gens_client_user_created on public.outfit_generations (client_id, user_id, created_at desc);
create index if not exists gens_client_status on public.outfit_generations (client_id, status);

-- 运营管理：套餐
create table if not exists public.price_plans (
  id uuid primary key default gen_random_uuid(),
  client_id text not null,
  name_zh text not null,
  name_en text,
  description text,
  price_quota bigint not null,
  model text not null,
  is_active boolean not null default true,
  sort_order int not null default 0,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

-- 管理员授权
create table if not exists public.app_admins (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references auth.users on delete cascade unique,
  role text not null default 'admin' check (role in ('owner','admin')),
  created_by uuid references auth.users on delete set null,
  created_at timestamptz not null default now()
);

-- ========== 2. is_app_admin() 辅助函数 ==========
create or replace function public.is_app_admin()
returns boolean language sql stable security definer set search_path = public as $$
  select exists (
    select 1 from public.app_admins
    where user_id = auth.uid() and public.current_app_client_id() = public.current_app_client_id()
  );
$$;

create or replace function public.is_app_owner()
returns boolean language sql stable security definer set search_path = public as $$
  select exists (
    select 1 from public.app_admins
    where user_id = auth.uid() and role = 'owner'
  );
$$;

-- ========== 3. RLS ==========
alter table public.outfits enable row level security;
alter table public.poses enable row level security;
alter table public.characters enable row level security;
alter table public.outfit_generations enable row level security;
alter table public.price_plans enable row level security;
alter table public.app_admins enable row level security;

drop policy if exists outfits_read on public.outfits;
drop policy if exists outfits_write_admin on public.outfits;
create policy outfits_read on public.outfits for select
  using (auth.role() = 'authenticated' and client_id = public.current_app_client_id());
create policy outfits_write_admin on public.outfits for all
  using (public.is_app_admin() and client_id = public.current_app_client_id())
  with check (public.is_app_admin() and client_id = public.current_app_client_id());

drop policy if exists poses_read on public.poses;
drop policy if exists poses_write_admin on public.poses;
create policy poses_read on public.poses for select
  using (auth.role() = 'authenticated' and client_id = public.current_app_client_id());
create policy poses_write_admin on public.poses for all
  using (public.is_app_admin() and client_id = public.current_app_client_id())
  with check (public.is_app_admin() and client_id = public.current_app_client_id());

drop policy if exists characters_owner_all on public.characters;
drop policy if exists characters_admin_all on public.characters;
create policy characters_owner_all on public.characters for all
  using (auth.uid() = user_id and client_id = public.current_app_client_id())
  with check (auth.uid() = user_id and client_id = public.current_app_client_id());
create policy characters_admin_all on public.characters for all
  using (public.is_app_admin() and client_id = public.current_app_client_id())
  with check (public.is_app_admin() and client_id = public.current_app_client_id());

drop policy if exists gens_owner_all on public.outfit_generations;
drop policy if exists gens_admin_all on public.outfit_generations;
create policy gens_owner_all on public.outfit_generations for all
  using (auth.uid() = user_id and client_id = public.current_app_client_id())
  with check (auth.uid() = user_id and client_id = public.current_app_client_id());
create policy gens_admin_all on public.outfit_generations for all
  using (public.is_app_admin() and client_id = public.current_app_client_id())
  with check (public.is_app_admin() and client_id = public.current_app_client_id());

drop policy if exists plans_read on public.price_plans;
drop policy if exists plans_admin_all on public.price_plans;
create policy plans_read on public.price_plans for select
  using (auth.role() = 'authenticated' and client_id = public.current_app_client_id());
create policy plans_admin_all on public.price_plans for all
  using (public.is_app_admin() and client_id = public.current_app_client_id())
  with check (public.is_app_admin() and client_id = public.current_app_client_id());

drop policy if exists app_admins_admin_read on public.app_admins;
drop policy if exists app_admins_admin_write on public.app_admins;
create policy app_admins_admin_read on public.app_admins for select
  using (public.is_app_admin());
create policy app_admins_owner_write on public.app_admins for all
  using (public.is_app_owner())
  with check (public.is_app_owner());

-- ========== 4. Storage buckets ==========
insert into storage.buckets (id, name, public) values
  ('media-official', 'media-official', false),
  ('media-user', 'media-user', false)
on conflict (id) do nothing;

-- ========== 5. Storage RLS（按路径前缀 client_id/user_id 隔离） ==========
-- 简化：服务端 route handler 用 service_role 拿 signed URL；这里只限制匿名写。
drop policy if exists media_official_read on storage.objects;
drop policy if exists media_user_read on storage.objects;
drop policy if exists media_user_upload on storage.objects;

create policy media_official_read on storage.objects for select
  using (bucket_id = 'media-official' and auth.role() = 'authenticated');

create policy media_user_read on storage.objects for select
  using (bucket_id = 'media-user' and auth.role() = 'authenticated');

create policy media_user_upload on storage.objects for insert
  with check (
    bucket_id = 'media-user'
    and auth.role() = 'authenticated'
    and name like public.current_app_client_id() || '/%'
  );
