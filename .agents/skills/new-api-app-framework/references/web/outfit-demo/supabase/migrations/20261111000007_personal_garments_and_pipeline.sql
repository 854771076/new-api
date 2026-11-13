-- 20261111000007_personal_garments_and_pipeline.sql
-- 个人服装归属（source='user' + user_id）与六阶段换装管线状态字段。
-- 仅 Supabase Postgres；可重复执行（全部 add column if not exists / drop+create policy）。
-- 不删除任何数据：历史行 source 保持 'official'、user_id 为 null，解析状态默认 'ready'。

-- ========== 1. outfits：激活个人服装归属并增加按需解析状态 ==========
-- source/user_id 已在初始迁移存在（source check 已含 'user'），这里补齐管线列。
alter table public.outfits
  add column if not exists parse_status text not null default 'ready'
    check (parse_status in ('pending', 'parsing', 'ready', 'failed'));
-- 个人服装原图沿用 thumb_path/preview_path（上传原图），解析后的单件商品图独立存放。
alter table public.outfits
  add column if not exists product_bucket text,
  add column if not exists product_path text,
  add column if not exists original_bucket text,
  add column if not exists original_path text,
  add column if not exists garment_type text,
  add column if not exists bbox jsonb,
  add column if not exists semantic_hints jsonb not null default '{}'::jsonb,
  add column if not exists parse_error text,
  add column if not exists parse_attempts int not null default 0;

-- 个人服装归属索引（官方行 user_id 为 null，不入该部分索引）。
create index if not exists outfits_client_user on public.outfits (client_id, user_id)
  where user_id is not null;
-- 待解析/解析中的个人服装扫描索引。
create index if not exists outfits_parse_status on public.outfits (client_id, parse_status)
  where source = 'user';

-- 归属一致性：个人服装必须有 owner，官方服装不得绑定 owner。
alter table public.outfits drop constraint if exists outfits_source_owner_check;
alter table public.outfits add constraint outfits_source_owner_check
  check (
    (source = 'user' and user_id is not null)
    or (source = 'official' and user_id is null)
  );

-- 历史官方行解析状态语义为「可直接使用制作图」，确保默认值已落盘。
update public.outfits set parse_status = 'ready'
  where source = 'official' and parse_status is distinct from 'ready';

-- ========== 2. characters：人物标准化（persona）阶段 ==========
alter table public.characters
  add column if not exists persona_status text not null default 'ready'
    check (persona_status in ('pending', 'normalizing', 'ready', 'failed'));
-- 标准化人物图沿用 cover_path 存放（image_path 始终是用户原图）。
alter table public.characters
  add column if not exists persona_version int not null default 0,
  add column if not exists persona_error text;

create index if not exists characters_persona_status on public.characters (client_id, persona_status)
  where persona_status <> 'ready';

-- ========== 3. outfit_generations：报价、幂等、管线阶段与冻结快照 ==========
alter table public.outfit_generations drop constraint if exists outfit_generations_status_check;
alter table public.outfit_generations add constraint outfit_generations_status_check
  check (status in ('pending', 'quoting', 'parsing', 'generating', 'moderating', 'succeeded', 'failed'));

alter table public.outfit_generations
  add column if not exists stage text not null default 'pending';
alter table public.outfit_generations drop constraint if exists outfit_generations_stage_check;
alter table public.outfit_generations add constraint outfit_generations_stage_check
  check (stage in ('pending', 'quoted', 'parsing', 'board', 'generating', 'moderating', 'done', 'failed'));

alter table public.outfit_generations
  add column if not exists idempotency_key text,
  add column if not exists quote jsonb not null default '{}'::jsonb,
  add column if not exists quote_accepted_at timestamptz,
  add column if not exists garment_manifest jsonb not null default '[]'::jsonb,
  add column if not exists board_bucket text,
  add column if not exists board_path text,
  add column if not exists persona_snapshot jsonb,
  add column if not exists wear_snapshot jsonb,
  add column if not exists provider_snapshot jsonb,
  add column if not exists pipeline_version text,
  add column if not exists parse_fee_quota bigint not null default 0,
  add column if not exists tryon_fee_quota bigint not null default 0,
  add column if not exists charged_quota bigint not null default 0,
  add column if not exists refunded_quota bigint not null default 0;

-- 同一用户对同一报价/请求的重复提交返回原任务（部分唯一索引：仅约束已设置 key 的行）。
create unique index if not exists gens_idempotency_key
  on public.outfit_generations (client_id, user_id, idempotency_key)
  where idempotency_key is not null;
create index if not exists gens_stage on public.outfit_generations (client_id, stage)
  where stage <> 'done';

-- ========== 4. RLS：官方素材对全体可见，个人服装仅 owner/管理员可见 ==========
-- outfits 旧的全员读策略与管理员写策略整体替换。
drop policy if exists outfits_read on public.outfits;
drop policy if exists outfits_write_admin on public.outfits;
drop policy if exists outfits_official_read on public.outfits;
drop policy if exists outfits_personal_owner_all on public.outfits;
drop policy if exists outfits_admin_all on public.outfits;

create policy outfits_official_read on public.outfits for select to authenticated
  using (
    client_id = public.current_app_client_id()
    and source = 'official'
  );
create policy outfits_personal_owner_all on public.outfits for all to authenticated
  using (
    client_id = public.current_app_client_id()
    and source = 'user'
    and (user_id = auth.uid() or public.is_app_admin())
  )
  with check (
    client_id = public.current_app_client_id()
    and source = 'user'
    and user_id = auth.uid()
  );
create policy outfits_admin_all on public.outfits for all to authenticated
  using (public.is_app_admin() and client_id = public.current_app_client_id())
  with check (public.is_app_admin() and client_id = public.current_app_client_id());

-- ========== 5. Storage：个人服装原图与商品图仅 owner/管理员可读 ==========
-- 官方桶策略不动；media-user 增加个人服装路径分支（与 characters/generations 并列）。
-- 服装板是某次换装的派生产物，归到 outfit_generations 的 board_path 分支。
drop policy if exists media_user_read on storage.objects;
create policy media_user_read on storage.objects for select to authenticated
  using (
    bucket_id = 'media-user' and (
      exists (select 1 from public.characters c
        where c.client_id = public.current_app_client_id() and c.bucket = storage.objects.bucket_id
          and (c.user_id = auth.uid() or public.is_app_admin())
          and (storage.objects.name = c.image_path or storage.objects.name = c.cover_path))
      or exists (select 1 from public.outfit_generations g
        where g.client_id = public.current_app_client_id() and g.result_bucket = storage.objects.bucket_id
          and (g.user_id = auth.uid() or public.is_app_admin())
          and (storage.objects.name = g.result_path or storage.objects.name = g.board_path))
      or exists (
        select 1 from public.outfits o
        where o.client_id = public.current_app_client_id()
          and o.source = 'user' and o.bucket = storage.objects.bucket_id
          and (o.user_id = auth.uid() or public.is_app_admin())
          and storage.objects.name in (o.thumb_path, o.preview_path, o.original_path, o.product_path)
      )
    )
  );

-- ========== 6. 列级写权限：浏览器一律只读，个人服装写入仅 BFF service_role ==========
-- 与 migration 004/005 一致：authenticated 保留 select，撤销新增列的直写权限。
revoke insert, update, delete on public.outfits, public.characters, public.outfit_generations
  from public, anon, authenticated;
grant select on public.outfits, public.characters, public.outfit_generations to authenticated;
grant all on public.outfits, public.characters, public.outfit_generations to service_role;

do $$
declare t text; cols text;
begin
  foreach t in array array['outfits', 'characters', 'outfit_generations'] loop
    select string_agg(quote_ident(attname), ',') into cols from pg_attribute
      where attrelid = format('public.%I', t)::regclass and attnum > 0 and not attisdropped;
    execute format('revoke insert (%s), update (%s) on public.%I from public, anon, authenticated', cols, cols, t);
  end loop;
end;
$$;
