-- Existing charge/refund values were copied from quotes, not gateway settlement.
-- Keep the quote snapshot and remove the false claim of a confirmed transaction.
alter table public.outfit_generations
  alter column charged_quota drop not null,
  alter column refunded_quota drop not null,
  alter column charged_quota drop default,
  alter column refunded_quota drop default;
update public.outfit_generations set charged_quota = null, refunded_quota = null;

create or replace function public.bootstrap_proxy_owner(p_user_id uuid, p_client_id text)
returns void language plpgsql volatile security invoker set search_path = '' as $$
begin
  perform pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtext(p_client_id));
  if not exists (select 1 from public.gateway_proxy_accounts p where p.user_id = p_user_id and p.client_id = p_client_id) then
    raise exception 'Invalid proxy owner identity';
  end if;
  if not exists (select 1 from public.app_admins a where a.client_id = p_client_id and a.role = 'owner') then
    insert into public.app_admins(user_id, client_id, role) values (p_user_id, p_client_id, 'owner')
      on conflict (client_id, user_id) do nothing;
  end if;
end;
$$;
revoke all on function public.bootstrap_proxy_owner(uuid, text) from public, anon, authenticated;
grant execute on function public.bootstrap_proxy_owner(uuid, text) to service_role;
