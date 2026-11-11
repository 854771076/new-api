import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { randomUUID } from 'node:crypto';
import sharp from 'sharp';
import { z } from 'zod';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { config } from '@/lib/config';
import { gatewayFailure, requireGatewayOrigin } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
const MAX_SIZE = 4 * 1024 * 1024;
const ALLOWED_MIME = new Set(['image/jpeg', 'image/png', 'image/webp']);
const metadataSchema = z.object({
  name: z.string().trim().min(1).max(100),
  description: z.string().trim().max(1000).nullable(),
});
const patchSchema = metadataSchema.partial().strict().refine(body => Object.keys(body).length > 0);
const idSchema = z.string().uuid();

export async function GET() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });

  const { data, error } = await supabase.from('characters').select('*')
    .eq('client_id', config.gatewayClientId).eq('user_id', user.id)
    .order('created_at', { ascending: false }).order('id');
  if (error) return NextResponse.json({ error: 'DB_ERROR', message: '读取人物失败，请稍后重试' }, { status: 503 });
  return NextResponse.json({ ok: true, data });
}

export async function POST(req: NextRequest) {
  try { requireGatewayOrigin(req); } catch (error) { return gatewayFailure(error); }
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });
  const { data: clientId, error: identityError } = await supabase.rpc('current_app_client_id');
  if (identityError || !clientId || clientId !== config.gatewayClientId) return NextResponse.json({ error: 'FORBIDDEN' }, { status: 403 });
  if (Number(req.headers.get('content-length')) > MAX_SIZE + 32 * 1024) {
    return NextResponse.json({ error: 'FILE_TOO_LARGE', message: '图片不能超过 4MB' }, { status: 413 });
  }
  let form: FormData;
  try { form = await req.formData(); }
  catch { return NextResponse.json({ error: 'INVALID_BODY', message: '请求必须是 multipart/form-data' }, { status: 400 }); }
  const file = form.get('file');
  if (!(file instanceof File) || !file.size || !ALLOWED_MIME.has(file.type)) {
    return NextResponse.json({ error: 'INVALID_FILE', message: '请选择 JPEG、PNG 或 WebP 图片' }, { status: 400 });
  }
  if (file.size > MAX_SIZE) return NextResponse.json({ error: 'FILE_TOO_LARGE', message: '图片不能超过 4MB' }, { status: 413 });
  const fields = metadataSchema.safeParse({
    name: form.get('name') || file.name.replace(/\.[^.]+$/, ''),
    description: form.get('description') || null,
  });
  if (!fields.success) return NextResponse.json({ error: 'INVALID_BODY', message: '名称为 1–100 字，描述最多 1000 字' }, { status: 400 });
  let bytes: Buffer;
  try {
    const image = sharp(Buffer.from(await file.arrayBuffer()), { limitInputPixels: 40_000_000, animated: false });
    const metadata = await image.metadata();
    if (!['png', 'jpeg', 'webp'].includes(metadata.format ?? '')) throw new Error('Invalid image');
    bytes = await image.rotate().png().toBuffer();
    if (bytes.length > 20 * 1024 * 1024) throw new Error('Image too large');
  } catch {
    return NextResponse.json({ error: 'INVALID_IMAGE', message: '图片损坏或尺寸过大，请更换图片' }, { status: 400 });
  }
  const id = randomUUID();
  const path = `${clientId}/${user.id}/characters/${id}.png`;
  const admin = createAdminSupabase();
  const { error: uploadError } = await admin.storage.from('media-user').upload(path, bytes, { contentType: 'image/png', upsert: false });
  if (uploadError) return NextResponse.json({ error: 'STORAGE_ERROR', message: '上传图片失败，请稍后重试' }, { status: 503 });
  const { data, error } = await admin.from('characters').insert({
    id, client_id: clientId, user_id: user.id, ...fields.data,
    bucket: 'media-user', image_path: path, cover_path: null, is_public: false,
  }).select('*').single();
  if (error) {
    await admin.storage.from('media-user').remove([path]);
    return NextResponse.json({ error: 'DB_ERROR', message: '保存人物失败，请稍后重试' }, { status: 503 });
  }
  return NextResponse.json({ ok: true, data });
}

export async function PATCH(req: NextRequest) {
  try { requireGatewayOrigin(req); } catch (error) { return gatewayFailure(error); }
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });
  const { data: clientId, error: identityError } = await supabase.rpc('current_app_client_id');
  if (identityError || !clientId || clientId !== config.gatewayClientId) return NextResponse.json({ error: 'FORBIDDEN' }, { status: 403 });
  const id = idSchema.safeParse(req.nextUrl.searchParams.get('id'));
  const patch = patchSchema.safeParse(await req.json().catch(() => null));
  if (!id.success || !patch.success) return NextResponse.json({ error: 'INVALID_BODY', message: '人物或更新字段无效' }, { status: 400 });
  const { data, error } = await createAdminSupabase().from('characters')
    .update({ ...patch.data, description: patch.data.description === undefined ? undefined : patch.data.description || null, updated_at: new Date().toISOString() })
    .eq('id', id.data).eq('client_id', clientId).eq('user_id', user.id).select('*').maybeSingle();
  if (error) return NextResponse.json({ error: 'DB_ERROR', message: '更新人物失败，请稍后重试' }, { status: 503 });
  if (!data) return NextResponse.json({ error: 'NOT_FOUND', message: '人物不存在或无权限' }, { status: 404 });
  return NextResponse.json({ ok: true, data });
}

export async function DELETE(req: NextRequest) {
  try { requireGatewayOrigin(req); } catch (error) { return gatewayFailure(error); }
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });
  const { data: clientId, error: identityError } = await supabase.rpc('current_app_client_id');
  if (identityError || !clientId || clientId !== config.gatewayClientId) return NextResponse.json({ error: 'FORBIDDEN' }, { status: 403 });
  const id = idSchema.safeParse(req.nextUrl.searchParams.get('id'));
  if (!id.success) return NextResponse.json({ error: 'INVALID_ID' }, { status: 400 });
  const admin = createAdminSupabase();
  const { data: row, error } = await admin.from('characters').select('bucket, image_path')
    .eq('id', id.data).eq('client_id', clientId).eq('user_id', user.id).maybeSingle();
  if (error) return NextResponse.json({ error: 'DB_ERROR', message: '读取人物失败，请稍后重试' }, { status: 503 });
  if (!row) return NextResponse.json({ error: 'NOT_FOUND', message: '人物不存在或无权限' }, { status: 404 });
  const canonicalPath = `${clientId}/${user.id}/characters/${id.data}.png`;
  const legacyPaths = ['png', 'jpg', 'jpeg', 'webp'].map(ext => `characters/${id.data}.${ext}`);
  if (row.bucket !== 'media-user' || (row.image_path !== canonicalPath && !legacyPaths.includes(row.image_path))) {
    return NextResponse.json({ error: 'INVALID_MEDIA_OWNER', message: '人物图片归属异常，请联系管理员' }, { status: 409 });
  }
  const { error: deleteError } = await admin.from('characters').delete()
    .eq('id', id.data).eq('client_id', clientId).eq('user_id', user.id);
  if (deleteError) return NextResponse.json({ error: 'DB_ERROR', message: '删除人物失败，请稍后重试' }, { status: 503 });
  await admin.storage.from('media-user').remove([row.image_path]);
  return NextResponse.json({ ok: true });
}
