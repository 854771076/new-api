// GET /api/media/[bucket]/[...path] — 返回 storage signed URL（≤1h）。
// 服务端用 service_role 调用；必须有 Supabase session 且目标 bucket 已存在。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';

const ALLOWED_BUCKETS = new Set(['media-official', 'media-user']);
const EXPIRY_S = 3600;

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ bucket: string; path: string[] }> },
) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });

  const { bucket, path } = await ctx.params;
  if (!ALLOWED_BUCKETS.has(bucket)) return NextResponse.json({ error: 'INVALID_BUCKET' }, { status: 400 });
  const objectPath = path.join('/');
  if (!objectPath) return NextResponse.json({ error: 'MISSING_PATH' }, { status: 400 });

  const admin = createAdminSupabase();
  const { data, error } = await admin.storage
    .from(bucket)
    .createSignedUrl(objectPath, EXPIRY_S);

  if (error || !data?.signedUrl) {
    return NextResponse.json({ error: error?.message ?? 'SIGNED_URL_FAILED' }, { status: 500 });
  }

  return NextResponse.redirect(data.signedUrl, 302);
}
