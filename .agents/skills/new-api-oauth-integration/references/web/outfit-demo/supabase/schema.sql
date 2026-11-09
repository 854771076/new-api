-- ============================================================================
-- supabase/schema.sql — 换装 App 业务 schema（底座 + 换装业务层）
--
-- 这是 new-api 网关 Supabase App Starter 的换装业务模板。
-- 通用底座（app_config / user_profiles / ai_generations）来自脚手架；
-- official_outfits / user_outfits / character_profiles 是换装业务核心。
--
-- 所有业务表都有 client_id 字段；RLS 通过 current_app_client_id() 强制
-- client_id 匹配 + auth.uid() 双隔离。多 App 共用同一 Supabase 项目时改
-- app_config.client_id 即可切换隔离，不触碰 RLS 策略。
--
-- ⚠️ PostgreSQL 会在 CREATE FUNCTION 时解析函数体里引用的表，所以
--    current_app_client_id() 和 seed_outfit_demo() 必须在所有表都建好后
--    再创建。本文件按此顺序组织。
-- ============================================================================

-- ============================================================================
-- 第一部分：脚手架通用底座
-- ============================================================================

-- App 配置（每个 Supabase 项目共用一个 client_id）
-- 单行表：id 固定为 1，避免 `limit 1` 取到不确定的行。
create table if not exists public.app_config (
  id           int primary key default 1 check (id = 1),
  client_id    text not null,   -- 网关 OAuthApp.client_id
  app_name     text,
  created_at   timestamptz not null default now()
);

-- 用户扩展表（首次 OIDC 登录时 Edge Function 写）
create table if not exists public.user_profiles (
  id                 uuid primary key references auth.users(id) on delete cascade,
  gateway_user_id    int unique,
  display_name       text,
  avatar_url         text,
  client_id          text not null,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now()
);

create index if not exists idx_user_profiles_gateway_user_id
  on public.user_profiles(gateway_user_id);

-- AI 用量（业务侧只读副本；网关侧真实用量在网关 model.Log）
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

-- ============================================================================
-- 第二部分：换装业务表
-- ============================================================================

-- 官方服装库：运营维护，所有用户可读。
-- category: top | bottom | shoes | accessory | fullset
-- is_active: 运营上下架开关
create table if not exists public.official_outfits (
  id            uuid primary key default gen_random_uuid(),
  client_id     text not null,
  name          text not null,
  category      text not null check (category in ('top','bottom','shoes','accessory','fullset')),
  description   text,
  image_url     text,
  tags          text[] default '{}',
  price_credits int not null default 0,   -- 解锁这套服装要花的"积分"（业务层展示用；真实账单走网关）
  is_active     boolean not null default true,
  created_by    uuid references auth.users(id),    -- 运营；可空
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create index if not exists idx_official_outfits_client_cat
  on public.official_outfits(client_id, category);
create index if not exists idx_official_outfits_active
  on public.official_outfits(is_active) where is_active;

-- 幂等 seed 保护：同一 App 下服装名+分类唯一
do $$ begin
  if not exists (select 1 from pg_constraint where conname = 'official_outfits_unique') then
    alter table public.official_outfits
      add constraint official_outfits_unique unique (client_id, name, category);
  end if;
end $$;

-- 用户服装：从官方库"解锁"过来的 + 自己上传的自定义服装。
-- source_type: official | custom
create table if not exists public.user_outfits (
  id                 uuid primary key default gen_random_uuid(),
  user_id            uuid not null references auth.users(id) on delete cascade,
  client_id          text not null,
  name               text not null,
  category           text not null check (category in ('top','bottom','shoes','accessory','fullset')),
  image_url          text,
  source_type        text not null check (source_type in ('official','custom')),
  official_outfit_id uuid references public.official_outfits(id) on delete set null,
  owned              boolean not null default true,
  unlocked_at        timestamptz,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now()
);

create index if not exists idx_user_outfits_user
  on public.user_outfits(user_id);
create index if not exists idx_user_outfits_user_cat
  on public.user_outfits(user_id, category);

-- 人物形象 + 搭配结果
create table if not exists public.character_profiles (
  id               uuid primary key default gen_random_uuid(),
  user_id          uuid not null references auth.users(id) on delete cascade,
  client_id        text not null,
  name             text not null default '我的角色',
  avatar_url       text,
  outfit_top       uuid references public.user_outfits(id) on delete set null,
  outfit_bottom    uuid references public.user_outfits(id) on delete set null,
  outfit_shoes     uuid references public.user_outfits(id) on delete set null,
  outfit_accessory uuid references public.user_outfits(id) on delete set null,
  background_url   text,
  favorite         boolean not null default false,
  saved_at         timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create index if not exists idx_character_profiles_user
  on public.character_profiles(user_id);

-- ============================================================================
-- 第三部分：安全函数（必须在所有表之后创建，Postgres 会解析函数体）
-- ============================================================================

-- 当前 App 的 client_id：从 app_config 的唯一行（id = 1）里取。多 App 共用
-- 同一 Supabase 项目时，改 app_config.client_id 即可让 RLS 切换隔离范围，
-- 不用改策略本身。
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

-- ============================================================================
-- 第四部分：启用 RLS
-- ============================================================================

alter table public.app_config          enable row level security;
alter table public.user_profiles       enable row level security;
alter table public.ai_generations      enable row level security;
alter table public.official_outfits    enable row level security;
alter table public.user_outfits        enable row level security;
alter table public.character_profiles  enable row level security;

-- ============================================================================
-- 第五部分：RLS 策略
-- ============================================================================

-- app_config: 所有已登录用户可读；写只允许 service_role
drop policy if exists "app_config readable by authenticated users"
  on public.app_config;
create policy "app_config readable by authenticated users"
  on public.app_config for select
  using (auth.role() = 'authenticated');

drop policy if exists "app_config editable only by service_role"
  on public.app_config;
create policy "app_config editable only by service_role"
  on public.app_config for all
  using (auth.role() = 'service_role');

-- user_profiles: 客户端只读自己的行。写入（尤其 gateway_user_id）只能由
-- service_role / Edge Function 完成，防止用户改写自己的网关身份映射。
drop policy if exists "user_profiles own row" on public.user_profiles;
drop policy if exists "user_profiles own row read" on public.user_profiles;
create policy "user_profiles own row read"
  on public.user_profiles for select
  using (id = auth.uid() and client_id = public.current_app_client_id());

-- 表级授权兜底：撤销客户端所有写权限，只保留 SELECT。
revoke all on public.user_profiles from anon, authenticated;
grant select on public.user_profiles to authenticated;

-- ai_generations: 自己行的 CRUD
drop policy if exists "ai_generations own row" on public.ai_generations;
create policy "ai_generations own row"
  on public.ai_generations for all
  using (user_id = auth.uid() and client_id = public.current_app_client_id())
  with check (user_id = auth.uid() and client_id = public.current_app_client_id());

-- official_outfits: 所有已登录用户可读（官方库是公共资产）；运营可写
drop policy if exists "official_outfits readable by all authenticated"
  on public.official_outfits;
create policy "official_outfits readable by all authenticated"
  on public.official_outfits for select
  using (client_id = public.current_app_client_id() and auth.role() = 'authenticated');

drop policy if exists "official_outfits editable only by service_role"
  on public.official_outfits;
create policy "official_outfits editable only by service_role"
  on public.official_outfits for all
  using (client_id = public.current_app_client_id() and auth.role() = 'service_role');

-- user_outfits: 自己的服装 + client_id 匹配
drop policy if exists "user_outfits own row" on public.user_outfits;
create policy "user_outfits own row"
  on public.user_outfits for all
  using (user_id = auth.uid() and client_id = public.current_app_client_id())
  with check (user_id = auth.uid() and client_id = public.current_app_client_id());

-- character_profiles: 自己的角色 + client_id 匹配
drop policy if exists "character_profiles own row" on public.character_profiles;
create policy "character_profiles own row"
  on public.character_profiles for all
  using (user_id = auth.uid() and client_id = public.current_app_client_id())
  with check (user_id = auth.uid() and client_id = public.current_app_client_id());

-- ============================================================================
-- 第六部分：Mock Seed 函数（最后创建，因为引用所有业务表）
-- ============================================================================

-- 仅 service_role 可执行的演示数据初始化（SQL Editor 以 postgres 身份运行，
-- 或由运维显式调用）。anon / authenticated 直接调用会被拒绝。
create or replace function public.seed_outfit_demo(app_client_id text)
returns table(official_count int)
language plpgsql
security definer set search_path = public, pg_temp
as $$
declare
  v_official int := 0;
begin
  -- app_config：单行表，固定写 id = 1
  insert into public.app_config (id, client_id, app_name)
  values (1, app_client_id, 'outfit-demo')
  on conflict (id) do update
    set client_id = excluded.client_id,
        app_name  = excluded.app_name;

  -- 官方服装库（8 件）
  insert into public.official_outfits
    (client_id, name, category, description, image_url, tags, price_credits, is_active, created_by)
  values
    (app_client_id, '纯白 T 恤',      'top',       '经典百搭', 'https://example.com/img/tshirt-white.png',  '{basic,casual}',       0, true, null),
    (app_client_id, '牛仔夹克',      'top',       '街头风',   'https://example.com/img/denim-jacket.png',  '{street,fashion}',    50, true, null),
    (app_client_id, '运动短裤',      'bottom',     '舒适日常', 'https://example.com/img/sport-shorts.png',  '{sport,summer}',        0, true, null),
    (app_client_id, '宽松牛仔裤',    'bottom',     '复古风',   'https://example.com/img/loose-jeans.png',   '{retro,street}',      30, true, null),
    (app_client_id, '白色运动鞋',    'shoes',      '全能搭',   'https://example.com/img/white-sneakers.png','{basic,sport}',        0, true, null),
    (app_client_id, '复古皮靴',      'shoes',      '帅气',     'https://example.com/img/leather-boots.png', '{retro,fashion}',     80, true, null),
    (app_client_id, '黑框眼镜',      'accessory',  '学院风',   'https://example.com/img/glasses.png',       '{academic,basic}',    20, true, null),
    (app_client_id, '棒球帽',        'accessory',  '街头',     'https://example.com/img/cap.png',           '{street,sport}',       0, true, null)
  on conflict do nothing;

  get diagnostics v_official = row_count;

  official_count := v_official;
  return next;
end;
$$;

-- seed 函数可以写 app_config 和官方数据，必须只允许 service_role 执行。
revoke all on function public.seed_outfit_demo(text) from public;
revoke all on function public.seed_outfit_demo(text) from anon, authenticated;
grant execute on function public.seed_outfit_demo(text) to service_role;
