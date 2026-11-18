-- Mapping and hosted credentials are server-only; business profiles remain separate.
create table public.gateway_proxy_accounts (
  user_id uuid not null references auth.users(id) on delete cascade,
  issuer text not null,
  app_id bigint not null check (app_id > 0),
  client_id text not null,
  identity_source text not null default 'proxy' check (identity_source = 'proxy'),
  gateway_user_id bigint not null check (gateway_user_id > 0),
  token_id bigint not null check (token_id > 0),
  encrypted_key text not null,
  created_at timestamptz not null default now(),
  primary key (user_id, issuer, app_id),
  unique (app_id, gateway_user_id)
);
alter table public.gateway_proxy_accounts enable row level security;
revoke all on public.gateway_proxy_accounts from public, anon, authenticated;
grant select, insert, update, delete on public.gateway_proxy_accounts to service_role;

create table public.gateway_proxy_sessions (
  session_id uuid primary key references auth.sessions(id) on delete cascade,
  user_id uuid not null,
  issuer text not null,
  app_id bigint not null,
  foreign key (user_id, issuer, app_id) references public.gateway_proxy_accounts(user_id, issuer, app_id) on delete cascade
);
alter table public.gateway_proxy_sessions enable row level security;
revoke all on public.gateway_proxy_sessions from public, anon, authenticated;
grant select, insert on public.gateway_proxy_sessions to service_role;

-- Read only the session columns needed for immediate revocation checks.
grant usage on schema auth to service_role;
grant select (id, user_id, not_after) on auth.sessions to service_role;
create function public.gateway_proxy_session_active(p_session_id uuid, p_user_id uuid)
returns boolean language sql stable security invoker set search_path = '' as $$
  select exists (select 1 from auth.sessions s where s.id = p_session_id and s.user_id = p_user_id
    and (s.not_after is null or s.not_after > pg_catalog.now()));
$$;
revoke all on function public.gateway_proxy_session_active(uuid, uuid) from public, anon, authenticated;
grant execute on function public.gateway_proxy_session_active(uuid, uuid) to service_role;

create or replace function public.current_app_client_id()
returns text language sql stable security definer set search_path = '' as $$
  select case
    when (select count(*) from auth.identities i where i.user_id = auth.uid() and i.provider = 'custom:newapi') = 1
      and not exists (select 1 from auth.identities i where i.user_id = auth.uid() and i.provider <> 'custom:newapi')
      and not exists (select 1 from public.gateway_proxy_accounts p where p.user_id = auth.uid())
    then (select i.identity_data->'aud'->>0 from auth.identities i
      where i.user_id = auth.uid() and i.provider = 'custom:newapi'
        and pg_catalog.jsonb_typeof(i.identity_data->'aud') = 'array' limit 1)
    when not exists (select 1 from auth.identities i where i.user_id = auth.uid() and i.provider not in ('email', 'phone'))
      and exists (select 1 from auth.sessions s where s.id::text = auth.jwt()->>'session_id'
        and s.user_id = auth.uid() and (s.not_after is null or s.not_after > pg_catalog.now()))
    then (select p.client_id from public.gateway_proxy_accounts p
      join public.gateway_proxy_sessions s using (user_id, issuer, app_id)
      where p.user_id = auth.uid() and s.session_id::text = auth.jwt()->>'session_id' limit 1)
    else '' end;
$$;
revoke all on function public.current_app_client_id() from public, anon;
grant execute on function public.current_app_client_id() to authenticated, service_role;

create table public.gateway_checkout_requests (
  user_id uuid not null references auth.users(id) on delete cascade,
  app_id bigint not null,
  request_id uuid not null,
  input_hash text not null,
  order_id text,
  created_at timestamptz not null default now(),
  primary key (user_id, app_id, request_id)
);
alter table public.gateway_checkout_requests enable row level security;
revoke all on public.gateway_checkout_requests from public, anon, authenticated;
grant select, insert, update on public.gateway_checkout_requests to service_role;
