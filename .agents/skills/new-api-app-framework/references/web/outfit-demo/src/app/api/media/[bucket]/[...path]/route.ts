// GET /api/media/[bucket]/[...path] — 返回 storage signed URL（≤1h）。
// Storage RLS applies to every signed URL.
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';

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
  if (!objectPath || path.some(part => !part || part === '.' || part === '..' || part.includes('\\'))) {
    return NextResponse.json({ error: 'INVALID_PATH' }, { status: 400 });
  }

  const { data, error } = await supabase.storage
    .from(bucket)
    .createSignedUrl(objectPath, EXPIRY_S);

  if (error || !data?.signedUrl) {
    return NextResponse.json({ error: 'MEDIA_NOT_FOUND' }, { status: 404, headers: { 'Cache-Control': 'no-store' } });
  }

  const response = NextResponse.redirect(data.signedUrl, 302);
  response.headers.set('Cache-Control', 'private, no-store');
  return response;
}
