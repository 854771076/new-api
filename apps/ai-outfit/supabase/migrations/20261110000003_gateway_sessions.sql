-- Token vault only: billing balances and orders remain exclusively in new-api.
create table if not exists public.gateway_sessions (
  session_id uuid not null,
  user_id uuid not null references auth.users(id) on delete cascade,
  client_id text not null,
  encrypted_pair text not null,
  version bigint not null default 0,
  lease_owner uuid,
  lease_until timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (session_id, user_id, client_id),
  constraint gateway_sessions_lease_pair check ((lease_owner is null) = (lease_until is null))
);
alter table public.gateway_sessions enable row level security;
revoke all on table public.gateway_sessions from public, anon, authenticated;
grant select, insert, update, delete on table public.gateway_sessions to service_role;

-- Identity data is provider-owned, unlike editable user_metadata. Never use
-- user_metadata.aud for authorization. app_metadata is a trusted admin fallback.
create or replace function public.current_app_client_id()
returns text language sql stable security definer set search_path = ''
as $$
  select coalesce(
    (select nullif(i.identity_data->'aud'->>0, '')
     from auth.identities as i
     where i.user_id = auth.uid() and i.provider = 'custom:newapi'
       and pg_catalog.jsonb_typeof(i.identity_data->'aud') = 'array'
     order by i.id limit 1),
    nullif(auth.jwt()->'app_metadata'->>'client_id', ''),
    ''
  );
$$;
revoke all on function public.current_app_client_id() from public, anon;
grant execute on function public.current_app_client_id() to authenticated, service_role;

-- Database time and an atomic version CAS coordinate independent Vercel workers.
-- Expired leases are NOT re-used: a crashed worker may have already rotated the
-- upstream token. The host invalidates that vault instead of replaying its pair.
create or replace function public.gateway_session_acquire(
  p_session_id uuid, p_user_id uuid, p_client_id text,
  p_version bigint, p_owner uuid
) returns setof public.gateway_sessions
language sql volatile security invoker set search_path = ''
as $$
  update public.gateway_sessions
  set lease_owner = p_owner, lease_until = pg_catalog.clock_timestamp() + interval '30 seconds',
      updated_at = pg_catalog.clock_timestamp()
  where session_id = p_session_id and user_id = p_user_id and client_id = p_client_id
    and version = p_version and lease_owner is null
  returning *;
$$;

create or replace function public.gateway_session_rotate(
  p_session_id uuid, p_user_id uuid, p_client_id text,
  p_owner uuid, p_encrypted_pair text
) returns setof public.gateway_sessions
language sql volatile security invoker set search_path = ''
as $$
  update public.gateway_sessions
  set encrypted_pair = p_encrypted_pair, version = version + 1,
      lease_owner = null, lease_until = null, updated_at = pg_catalog.clock_timestamp()
  where session_id = p_session_id and user_id = p_user_id and client_id = p_client_id
    and lease_owner = p_owner and lease_until > pg_catalog.clock_timestamp()
  returning *;
$$;
-- Only a database-expired lease with the same owner can invalidate a crashed
-- worker's vault. Application clock skew must not delete an active refresh.
create or replace function public.gateway_session_expire(
  p_session_id uuid, p_user_id uuid, p_client_id text, p_owner uuid
) returns setof public.gateway_sessions
language sql volatile security invoker set search_path = ''
as $$
  delete from public.gateway_sessions
  where session_id = p_session_id and user_id = p_user_id and client_id = p_client_id
    and lease_owner = p_owner and lease_until <= pg_catalog.clock_timestamp()
  returning *;
$$;
revoke all on function public.gateway_session_expire(uuid, uuid, text, uuid) from public, anon, authenticated;
grant execute on function public.gateway_session_expire(uuid, uuid, text, uuid) to service_role;
revoke all on function public.gateway_session_acquire(uuid, uuid, text, bigint, uuid) from public, anon, authenticated;
revoke all on function public.gateway_session_rotate(uuid, uuid, text, uuid, text) from public, anon, authenticated;
grant execute on function public.gateway_session_acquire(uuid, uuid, text, bigint, uuid) to service_role;
grant execute on function public.gateway_session_rotate(uuid, uuid, text, uuid, text) to service_role;
