-- 20261111000008_tryon_temp_storage_rls.sql
-- 六阶段换装管线的临时裁剪图（service_role 上传、owner 短期签名读取）需要 Storage RLS 放行。
-- 仅放开当前 App + 当前 owner 自己的 outfits/tmp/ 前缀；管理员按 is_app_admin 同样可读。
-- 可重复执行：drop + create policy，不触碰其它桶或策略。

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
      -- 管线临时裁剪图：严格限定 App/owner 目录下的 outfits/tmp/ 前缀，不能用宽松后缀匹配。
      or (
        public.is_app_admin()
        or storage.objects.name like concat(
          public.current_app_client_id(), '/', auth.uid()::text, '/outfits/tmp/%'
        )
      )
    )
  );
