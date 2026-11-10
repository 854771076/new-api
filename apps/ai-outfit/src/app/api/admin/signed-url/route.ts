// GET /api/admin/signed-url?bucket=xxx&path=yyy — 管理员获取素材/结果图的临时签名链接。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { createAdminSupabase } from '@/lib/supabase/admin';

const ALLOWED_BUCKETS = new Set(['media-official', 'media-user']);
const EXPIRY_S = 3600;

export async function GET(req: NextRequest) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;

  const sp = new URL(req.url).searchParams;
  const bucket = sp.get('bucket');
  const path = sp.get('path');
  if (!bucket || !path) return NextResponse.json({ error: 'INVALID' }, { status: 400 });
  if (!ALLOWED_BUCKETS.has(bucket)) return NextResponse.json({ error: 'INVALID_BUCKET' }, { status: 400 });

  const admin = createAdminSupabase();
  const { data, error } = await admin.storage.from(bucket).createSignedUrl(path, EXPIRY_S);
  if (error || !data?.signedUrl) {
    return NextResponse.json({ error: error?.message ?? 'SIGNED_URL_FAILED' }, { status: 500 });
  }

  return NextResponse.json({ url: data.signedUrl });
}
