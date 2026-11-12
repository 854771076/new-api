-- 20261110000002_add_models_and_prompt_templates.sql — 可配置生成模型 + 提示词模板。
-- 适用于 Supabase Postgres；可重复执行。

-- ========== 1. 业务表 ==========

-- 后台可配置的生成模型；client_id = '' 表示该部署的全局默认行
create table if not exists public.ai_models (
  id uuid primary key default gen_random_uuid(),
  client_id text not null default '',
  model_id text not null,
  display_name text not null,
  provider text,
  usage text not null default 'outfit' check (usage in ('outfit','portrait','general')),
  description_zh text,
  default_size text,
  default_quality text,
  is_active boolean not null default true,
  is_default boolean not null default false,
  sort_order int not null default 0,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (client_id, model_id)
);
create index if not exists ai_models_client_act_sort on public.ai_models (client_id, is_active, sort_order);
create unique index if not exists ai_models_one_default on public.ai_models (client_id) where is_default;

-- 后台可配置的提示词模板；client_id = '' 表示该部署的全局默认行
create table if not exists public.prompt_templates (
  id uuid primary key default gen_random_uuid(),
  client_id text not null default '',
  usage text not null default 'outfit' check (usage in ('outfit','portrait','general')),
  name text not null,
  template text not null,
  negative_prompt text,
  is_active boolean not null default true,
  is_default boolean not null default false,
  sort_order int not null default 0,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index if not exists prompt_templates_one_default on public.prompt_templates (client_id, usage) where is_default;

-- ========== 2. updated_at 自动更新 ==========
create or replace function public.touch_updated_at()
returns trigger language plpgsql set search_path = public as $$
begin
  new.updated_at = now();
  return new;
end;
$$;

drop trigger if exists ai_models_touch_updated_at on public.ai_models;
create trigger ai_models_touch_updated_at before update on public.ai_models
  for each row execute function public.touch_updated_at();

drop trigger if exists prompt_templates_touch_updated_at on public.prompt_templates;
create trigger prompt_templates_touch_updated_at before update on public.prompt_templates
  for each row execute function public.touch_updated_at();

-- ========== 3. RLS ==========
alter table public.ai_models enable row level security;
alter table public.prompt_templates enable row level security;

drop policy if exists ai_models_read on public.ai_models;
drop policy if exists ai_models_admin_read on public.ai_models;
drop policy if exists ai_models_write_admin on public.ai_models;
create policy ai_models_read on public.ai_models for select
  using (
    auth.role() = 'authenticated'
    and is_active
    and client_id in (public.current_app_client_id(), '')
  );
create policy ai_models_admin_read on public.ai_models for select
  using (public.is_app_admin());
create policy ai_models_write_admin on public.ai_models for all
  using (public.is_app_admin() and client_id in (public.current_app_client_id(), ''))
  with check (public.is_app_admin() and client_id in (public.current_app_client_id(), ''));

drop policy if exists prompt_templates_read on public.prompt_templates;
drop policy if exists prompt_templates_admin_read on public.prompt_templates;
drop policy if exists prompt_templates_write_admin on public.prompt_templates;
create policy prompt_templates_read on public.prompt_templates for select
  using (
    auth.role() = 'authenticated'
    and is_active
    and client_id in (public.current_app_client_id(), '')
  );
create policy prompt_templates_admin_read on public.prompt_templates for select
  using (public.is_app_admin());
create policy prompt_templates_write_admin on public.prompt_templates for all
  using (public.is_app_admin() and client_id in (public.current_app_client_id(), ''))
  with check (public.is_app_admin() and client_id in (public.current_app_client_id(), ''));

-- ========== 4. 预置数据（幂等） ==========
insert into public.ai_models (client_id, model_id, display_name, provider, usage, default_size, is_active, is_default, sort_order)
select '', 'gpt-image-1', 'GPT Image 1', 'openai', 'outfit', '1024x1024', true, true, 0
where not exists (select 1 from public.ai_models);

insert into public.prompt_templates (client_id, usage, name, template, negative_prompt, is_active, is_default, sort_order)
select
  '',
  'outfit',
  '默认换装模板',
  '以参考图中的人物为唯一主体，保持其面部特征、发型、肤色和体型完全不变。服装替换为：{{outfits}}。姿势参考：{{pose}}。{{extra}} 输出高质量写实摄影风格，光线柔和自然，比例正确，不要出现多余肢体。',
  '不要改变人物脸型和五官，不要多余的肢体，不要文字和水印',
  true,
  true,
  0
where not exists (select 1 from public.prompt_templates);
