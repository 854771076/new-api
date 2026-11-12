revoke insert, update, delete on public.characters, public.outfit_generations from public, anon, authenticated;
grant select on public.characters, public.outfit_generations to authenticated;
grant all on public.characters, public.outfit_generations to service_role;

drop policy if exists characters_owner_all on public.characters;
drop policy if exists characters_admin_all on public.characters;
drop policy if exists characters_read on public.characters;
create policy characters_read on public.characters for select to authenticated
  using (client_id = public.current_app_client_id() and (user_id = auth.uid() or public.is_app_admin()));

drop policy if exists gens_owner_all on public.outfit_generations;
drop policy if exists gens_admin_all on public.outfit_generations;
drop policy if exists gens_read on public.outfit_generations;
create policy gens_read on public.outfit_generations for select to authenticated
  using (client_id = public.current_app_client_id() and (user_id = auth.uid() or public.is_app_admin()));

-- Legacy objects remain readable only through their now server-controlled owner rows.
drop policy if exists media_official_read on storage.objects;
drop policy if exists media_user_read on storage.objects;
drop policy if exists media_user_upload on storage.objects;
create policy media_official_read on storage.objects for select to authenticated
  using (
    bucket_id = 'media-official' and (
      exists (select 1 from public.outfits o
        where o.client_id = public.current_app_client_id() and o.bucket = storage.objects.bucket_id
          and (o.is_active or public.is_app_admin()) and storage.objects.name in (o.thumb_path, o.preview_path))
      or exists (select 1 from public.poses p
        where p.client_id = public.current_app_client_id() and p.bucket = storage.objects.bucket_id
          and (p.is_active or public.is_app_admin()) and storage.objects.name in (p.thumb_path, p.preview_path))
    )
  );
create policy media_user_read on storage.objects for select to authenticated
  using (
    bucket_id = 'media-user' and (
      exists (select 1 from public.characters c
        where c.client_id = public.current_app_client_id() and c.bucket = storage.objects.bucket_id
          and (c.user_id = auth.uid() or public.is_app_admin())
          and (storage.objects.name = c.image_path or storage.objects.name = c.cover_path))
      or exists (select 1 from public.outfit_generations g
        where g.client_id = public.current_app_client_id() and g.result_bucket = storage.objects.bucket_id
          and (g.user_id = auth.uid() or public.is_app_admin()) and storage.objects.name = g.result_path)
    )
  );

-- Column grants must not preserve old REST locator writes after a table-level revoke.
revoke insert (id, client_id, user_id, name, bucket, image_path, cover_path, description, is_public, created_at, updated_at),
  update (id, client_id, user_id, name, bucket, image_path, cover_path, description, is_public, created_at, updated_at)
  on public.characters from public, anon, authenticated;
revoke insert (id, client_id, user_id, character_id, outfit_ids, pose_id, prompt, model, status, result_bucket, result_path, error, gateway_request_id, gateway_usage, created_at, updated_at),
  update (id, client_id, user_id, character_id, outfit_ids, pose_id, prompt, model, status, result_bucket, result_path, error, gateway_request_id, gateway_usage, created_at, updated_at)
  on public.outfit_generations from public, anon, authenticated;
