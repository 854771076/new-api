import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { config } from '@/lib/config';
import { clampLimit, clampPage, mediaUrl } from '@/lib/list-api';

export const runtime = 'nodejs';

// 官方姿势库分页：支持 system/category/angle 精确筛选与 q 关键词搜索。
export async function GET(req: NextRequest) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });

  const sp = req.nextUrl.searchParams;
  const system = sp.get('system')?.trim();
  const category = sp.get('category')?.trim();
  const angle = sp.get('angle')?.trim();
  const q = sp.get('q')?.trim();
  const page = clampPage(sp.get('page'));
  const limit = clampLimit(sp.get('limit'));
  const from = (page - 1) * limit;

  let query = supabase.from('poses')
    .select('id, name_zh, system, category, angle, bucket, thumb_path', { count: 'exact' })
    .eq('client_id', config.gatewayClientId)
    .eq('is_active', true);
  if (system) query = query.eq('system', system);
  if (category) query = query.eq('category', category);
  if (angle) query = query.eq('angle', angle);
  if (q) {
    const pattern = `%${q}%`.replaceAll('\\', '\\\\').replaceAll('"', '\\"');
    query = query.or(
      `name_zh.ilike."${pattern}",name_en.ilike."${pattern}",description_zh.ilike."${pattern}",description_en.ilike."${pattern}"`,
    );
  }

  const { data, count, error } = await query
    .order('sort_order', { ascending: true }).order('id', { ascending: true })
    .range(from, from + limit - 1);
  if (error) return NextResponse.json({ error: 'DB_ERROR', message: '读取姿势失败，请稍后重试' }, { status: 503 });

  const items = (data ?? []).map((p) => ({
    id: p.id,
    name: p.name_zh ?? '未命名',
    subtitle: [p.system, p.category, p.angle].filter(Boolean).join(' · '),
    signedUrl: mediaUrl(p.bucket ?? 'media-official', p.thumb_path),
  }));
  return NextResponse.json({ ok: true, data: items, total: count ?? 0, page, pageSize: limit });
}
