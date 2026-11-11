CREATE OR REPLACE FUNCTION public.current_app_client_id()
  RETURNS text
  LANGUAGE sql
  STABLE SECURITY DEFINER
  SET search_path TO ''
AS $$
  -- new-api OAuth 的 aud 与 migration 3 一样从受保护的 provider identity 读取
  -- 与 migration 4 一致：身份缺失、格式错误或有歧义时拒绝，不以 JWT 元数据绕过检查
  select coalesce((
    select case when count(*) = 1
      and bool_and(pg_catalog.jsonb_typeof(i.identity_data->'aud') = 'array'
        and pg_catalog.jsonb_typeof(i.identity_data->'aud'->0) = 'string'
        and nullif(pg_catalog.btrim(i.identity_data->'aud'->>0), '') is not null)
      then min(i.identity_data->'aud'->>0) end
    from auth.identities i where i.user_id = auth.uid() and i.provider = 'custom:newapi'
  ), '');
$$;
revoke all on function public.current_app_client_id() from public, anon;
grant execute on function public.current_app_client_id() to authenticated, service_role;
