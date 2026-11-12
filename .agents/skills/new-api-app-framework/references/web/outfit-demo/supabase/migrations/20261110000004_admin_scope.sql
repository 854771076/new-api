begin;

-- Preserve every existing grant and its UUID, role, author and timestamps.
-- Do not infer authorization from editable user metadata or select an arbitrary
-- identity. An operator must resolve unmapped/ambiguous grants before retrying.
lock table public.app_admins in access exclusive mode;
lock table auth.identities in share mode;
alter table public.app_admins add column if not exists client_id text;
do $$
begin
  if exists (
    select 1 from public.app_admins a
    where (select count(*) from auth.identities i where i.user_id = a.user_id and i.provider = 'custom:newapi') <> 1
      or not exists (
        select 1 from auth.identities i where i.user_id = a.user_id and i.provider = 'custom:newapi'
          and jsonb_typeof(i.identity_data->'aud') = 'array'
          and jsonb_typeof(i.identity_data->'aud'->0) = 'string'
          and nullif(btrim(i.identity_data->'aud'->>0), '') is not null
          and (a.client_id is null or a.client_id = i.identity_data->'aud'->>0)
      )
  ) then
    raise exception 'Admin grants have unmapped, ambiguous or inconsistent protected provider identities';
  end if;
end;
$$;
update public.app_admins a set client_id = i.identity_data->'aud'->>0
from auth.identities i where i.user_id = a.user_id and i.provider = 'custom:newapi' and a.client_id is null;
alter table public.app_admins alter column client_id set not null;
alter table public.app_admins drop constraint if exists app_admins_user_id_key;
create unique index if not exists app_admins_client_user_key on public.app_admins (client_id, user_id);

create or replace function public.current_app_client_id()
returns text language sql stable security definer set search_path = '' as $$
  select coalesce((
    select case when count(*) = 1
      and bool_and(pg_catalog.jsonb_typeof(i.identity_data->'aud') = 'array'
        and pg_catalog.jsonb_typeof(i.identity_data->'aud'->0) = 'string'
        and nullif(pg_catalog.btrim(i.identity_data->'aud'->>0), '') is not null)
      then min(i.identity_data->'aud'->>0) end
    from auth.identities i where i.user_id = auth.uid() and i.provider = 'custom:newapi'
  ), '');
$$;
create or replace function public.is_app_admin()
returns boolean language sql stable security definer set search_path = '' as $$
  select exists (select 1 from public.app_admins a where a.user_id = auth.uid()
    and a.client_id = public.current_app_client_id() and a.client_id <> '' and a.role in ('owner', 'admin'));
$$;
create or replace function public.is_app_owner()
returns boolean language sql stable security definer set search_path = '' as $$
  select exists (select 1 from public.app_admins a where a.user_id = auth.uid()
    and a.client_id = public.current_app_client_id() and a.client_id <> '' and a.role = 'owner');
$$;
revoke all on function public.current_app_client_id(), public.is_app_admin(), public.is_app_owner() from public, anon;
grant execute on function public.current_app_client_id(), public.is_app_admin(), public.is_app_owner() to authenticated, service_role;

drop policy if exists app_admins_admin_read on public.app_admins;
drop policy if exists app_admins_admin_write on public.app_admins;
drop policy if exists app_admins_owner_write on public.app_admins;
create policy app_admins_admin_read on public.app_admins for select to authenticated
  using (public.is_app_admin() and client_id = public.current_app_client_id());
-- Role grants remain out-of-band; no browser grant/self-promotion endpoint.
revoke insert, update, delete on public.app_admins from public, anon, authenticated;
grant select on public.app_admins to authenticated;
grant select, insert, update, delete on public.app_admins to service_role;

drop policy if exists ai_models_admin_read on public.ai_models;
drop policy if exists ai_models_write_admin on public.ai_models;
create policy ai_models_admin_read on public.ai_models for select to authenticated
  using (public.is_app_admin() and client_id in (public.current_app_client_id(), ''));
create policy ai_models_write_admin on public.ai_models for all to authenticated
  using (public.is_app_admin() and client_id = public.current_app_client_id() and client_id <> '')
  with check (public.is_app_admin() and client_id = public.current_app_client_id() and client_id <> '');
drop policy if exists prompt_templates_admin_read on public.prompt_templates;
drop policy if exists prompt_templates_write_admin on public.prompt_templates;
create policy prompt_templates_admin_read on public.prompt_templates for select to authenticated
  using (public.is_app_admin() and client_id in (public.current_app_client_id(), ''));
create policy prompt_templates_write_admin on public.prompt_templates for all to authenticated
  using (public.is_app_admin() and client_id = public.current_app_client_id() and client_id <> '')
  with check (public.is_app_admin() and client_id = public.current_app_client_id() and client_id <> '');

-- Admin mutation is BFF-only: direct REST must not bypass field validation,
-- immutable media locators or the atomic default switch. Price rows untouched.
revoke insert, update, delete on public.outfits, public.poses, public.price_plans, public.ai_models, public.prompt_templates from public, anon, authenticated;
grant select on public.outfits, public.poses, public.price_plans, public.ai_models, public.prompt_templates to authenticated;
-- Remove any legacy column-level mutation privileges as well.
do $$
declare t text; cols text;
begin
  foreach t in array array['app_admins','outfits','poses','price_plans','ai_models','prompt_templates'] loop
    select string_agg(quote_ident(attname), ',') into cols from pg_attribute
      where attrelid = format('public.%I', t)::regclass and attnum > 0 and not attisdropped;
    execute format('revoke insert (%s), update (%s) on public.%I from public, anon, authenticated', cols, cols, t);
  end loop;
end;
$$;

-- One statement/transaction for create/edit/delete and default switching. Lock
-- the whole table+tenant scope (including prompt usage moves) across workers.
-- No dynamic DML, no caller-owned columns, no shared-default writes.
create or replace function public.admin_mutate_configuration(
  p_table text, p_client_id text, p_actor_id uuid, p_operation text, p_id uuid, p_fields jsonb
) returns jsonb language plpgsql volatile security invoker set search_path = '' as $$
declare
  allowed text[];
  required text[];
  field text;
  value jsonb;
  model_row public.ai_models%rowtype;
  prompt_row public.prompt_templates%rowtype;
  result jsonb;
begin
  if p_table is null or p_table not in ('ai_models','prompt_templates')
    or p_operation is null or p_operation not in ('POST','PATCH','DELETE')
    or p_client_id is null or btrim(p_client_id) = ''
    or jsonb_typeof(p_fields) is distinct from 'object'
    or (p_operation = 'POST' and p_id is not null)
    or (p_operation <> 'POST' and p_id is null) then
    raise exception 'Invalid mutation' using errcode = '22023';
  end if;
  if not exists (select 1 from public.app_admins a where a.client_id = p_client_id
    and a.user_id = p_actor_id and a.role in ('owner','admin')) then
    raise exception 'Forbidden' using errcode = '42501';
  end if;
  if p_table = 'ai_models' then
    allowed := array['model_id','display_name','provider','usage','description_zh','default_size','default_quality','is_active','is_default','sort_order'];
    required := array['model_id','display_name'];
  else
    allowed := array['name','template','negative_prompt','usage','is_active','is_default','sort_order'];
    required := array['name','template'];
  end if;
  if p_operation = 'DELETE' then
    if p_fields <> '{}'::jsonb then raise exception 'Invalid fields' using errcode = '22023'; end if;
  else
    if p_fields = '{}'::jsonb then raise exception 'Empty mutation' using errcode = '22023'; end if;
    for field, value in select * from jsonb_each(p_fields) loop
      if not field = any(allowed) then raise exception 'Invalid field' using errcode = '22023'; end if;
      if field in ('is_active','is_default') then
        if jsonb_typeof(value) <> 'boolean' then raise exception 'Invalid boolean' using errcode = '22023'; end if;
      elsif field = 'sort_order' then
        if jsonb_typeof(value) <> 'number' or value::text !~ '^[0-9]+$' or (value::text)::numeric > 2147483647 then
          raise exception 'Invalid order' using errcode = '22023';
        end if;
      else
        if jsonb_typeof(value) = 'null' and field in ('provider','description_zh','default_size','default_quality','negative_prompt') then continue; end if;
        if jsonb_typeof(value) <> 'string' then raise exception 'Invalid text' using errcode = '22023'; end if;
        if field = 'usage' then
          if (value #>> '{}') not in ('outfit','portrait','general') then raise exception 'Invalid usage' using errcode = '22023'; end if;
        elsif field in ('model_id','display_name','name','template') then
          if btrim(value #>> '{}') = '' or length(value #>> '{}') > (case when field = 'template' then 50000 else 200 end) then
            raise exception 'Invalid required text' using errcode = '22023';
          end if;
        elsif length(value #>> '{}') > (case when field in ('default_size','default_quality') then 100 when field = 'provider' then 200 else 10000 end) then
          raise exception 'Text too long' using errcode = '22023';
        end if;
      end if;
    end loop;
    if p_operation = 'POST' and not (p_fields ?& required) then raise exception 'Missing required fields' using errcode = '22023'; end if;
  end if;

  perform pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended('admin-config:' || p_table || ':' || p_client_id, 0));
  if p_table = 'ai_models' then
    if p_operation = 'POST' then
      select * into model_row from jsonb_populate_record(null::public.ai_models,
        jsonb_build_object('id', gen_random_uuid(), 'client_id', p_client_id, 'usage','outfit', 'is_active',true, 'is_default',false, 'sort_order',0) || p_fields);
    else
      select * into model_row from public.ai_models where id = p_id and client_id = p_client_id for update;
      if not found then raise exception 'Not found' using errcode = 'P0002'; end if;
      if p_operation = 'DELETE' then
        delete from public.ai_models where id = p_id and client_id = p_client_id;
        return jsonb_build_object('id', p_id);
      end if;
      select * into model_row from jsonb_populate_record(model_row, p_fields);
    end if;
    if model_row.is_default then
      update public.ai_models set is_default = false where client_id = p_client_id and is_default and id <> model_row.id;
    end if;
    if p_operation = 'POST' then
      insert into public.ai_models (id, client_id, model_id, display_name, provider, usage, description_zh, default_size, default_quality, is_active, is_default, sort_order)
        values (model_row.id, p_client_id, model_row.model_id, model_row.display_name, model_row.provider, model_row.usage, model_row.description_zh, model_row.default_size, model_row.default_quality, model_row.is_active, model_row.is_default, model_row.sort_order)
        returning to_jsonb(ai_models.*) into result;
    else
      update public.ai_models set model_id = model_row.model_id, display_name = model_row.display_name, provider = model_row.provider,
        usage = model_row.usage, description_zh = model_row.description_zh, default_size = model_row.default_size, default_quality = model_row.default_quality,
        is_active = model_row.is_active, is_default = model_row.is_default, sort_order = model_row.sort_order
        where id = p_id and client_id = p_client_id returning to_jsonb(ai_models.*) into result;
    end if;
  else
    if p_operation = 'POST' then
      select * into prompt_row from jsonb_populate_record(null::public.prompt_templates,
        jsonb_build_object('id', gen_random_uuid(), 'client_id', p_client_id, 'usage','outfit', 'is_active',true, 'is_default',false, 'sort_order',0) || p_fields);
    else
      select * into prompt_row from public.prompt_templates where id = p_id and client_id = p_client_id for update;
      if not found then raise exception 'Not found' using errcode = 'P0002'; end if;
      if p_operation = 'DELETE' then
        delete from public.prompt_templates where id = p_id and client_id = p_client_id;
        return jsonb_build_object('id', p_id);
      end if;
      select * into prompt_row from jsonb_populate_record(prompt_row, p_fields);
    end if;
    if prompt_row.is_default then
      update public.prompt_templates set is_default = false where client_id = p_client_id and usage = prompt_row.usage and is_default and id <> prompt_row.id;
    end if;
    if p_operation = 'POST' then
      insert into public.prompt_templates (id, client_id, usage, name, template, negative_prompt, is_active, is_default, sort_order)
        values (prompt_row.id, p_client_id, prompt_row.usage, prompt_row.name, prompt_row.template, prompt_row.negative_prompt, prompt_row.is_active, prompt_row.is_default, prompt_row.sort_order)
        returning to_jsonb(prompt_templates.*) into result;
    else
      update public.prompt_templates set usage = prompt_row.usage, name = prompt_row.name, template = prompt_row.template,
        negative_prompt = prompt_row.negative_prompt, is_active = prompt_row.is_active, is_default = prompt_row.is_default, sort_order = prompt_row.sort_order
        where id = p_id and client_id = p_client_id returning to_jsonb(prompt_templates.*) into result;
    end if;
  end if;
  return result;
end;
$$;
revoke all on function public.admin_mutate_configuration(text, text, uuid, text, uuid, jsonb) from public, anon, authenticated;
grant execute on function public.admin_mutate_configuration(text, text, uuid, text, uuid, jsonb) to service_role;

commit;
