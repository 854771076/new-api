CREATE OR REPLACE FUNCTION public.current_app_client_id()
  RETURNS text
  LANGUAGE sql
  STABLE SECURITY DEFINER
  SET search_path TO 'public'
AS $$
  -- Supabase 自动填充的 app_metadata 只有 provider / providers
  -- new-api OAuth 的 client_id 实际落在 user_metadata.aud 数组里
  -- 同时兜底检查 app_metadata.client_id（如果手动设置过）
  select coalesce(
    auth.jwt() -> 'app_metadata' ->> 'client_id',
    (auth.jwt() -> 'user_metadata' -> 'aud' ->> 0),
    ''
  );
$$;
