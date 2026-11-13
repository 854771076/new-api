import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { createServerSupabase } from '@/lib/supabase/server';

const ALLOWED_BUCKETS = new Set(['media-official', 'media-user']);

export async function GET(req: NextRequest) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;
  const bucket = req.nextUrl.searchParams.get('bucket');
  const path = req.nextUrl.searchParams.get('path');
  if (!bucket || !path || path.length > 1024 || !ALLOWED_BUCKETS.has(bucket)) {
    return NextResponse.json({ error: 'INVALID' }, { status: 400 });
  }
  // Storage RLS resolves exact tenant/owner rows, including legacy object paths.
  // A service-role signer would bypass those checks for arbitrary supplied paths.
  const supabase = await createServerSupabase();
  const { data, error } = await supabase.storage.from(bucket).createSignedUrl(path, 300);
  if (error || !data?.signedUrl) return NextResponse.json({ error: 'NOT_FOUND' }, { status: 404 });
  return NextResponse.json({ url: data.signedUrl }, { headers: { 'Cache-Control': 'no-store' } });
}
