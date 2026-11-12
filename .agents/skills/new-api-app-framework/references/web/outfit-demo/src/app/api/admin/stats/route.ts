// GET /api/admin/stats — 运营概览统计；仅管理员，走 service_role。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { createAdminSupabase } from '@/lib/supabase/admin';

interface RecentFailure {
  id: string;
  model: string;
  prompt: string | null;
  error: string | null;
  created_at: string;
}

type CountResult = { count: number | null; error: { message: string } | null };
const json = (body: unknown, status = 200) => NextResponse.json(body, { status, headers: { 'Cache-Control': 'no-store' } });

export async function GET(req: NextRequest) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;

  const admin = createAdminSupabase();
  // 当天 00:00（UTC），只用于「今日生成」计数。
  const todayStart = `${new Date().toISOString().slice(0, 10)}T00:00:00.000Z`;

  const [total, today, succeeded, failed, outfits, poses, characters, failures] = await Promise.all([
    admin.from('outfit_generations').select('*', { count: 'exact', head: true }).eq('client_id', check.clientId),
    admin.from('outfit_generations').select('*', { count: 'exact', head: true }).eq('client_id', check.clientId).gte('created_at', todayStart),
    admin.from('outfit_generations').select('*', { count: 'exact', head: true }).eq('client_id', check.clientId).eq('status', 'succeeded'),
    admin.from('outfit_generations').select('*', { count: 'exact', head: true }).eq('client_id', check.clientId).eq('status', 'failed'),
    admin.from('outfits').select('*', { count: 'exact', head: true }).eq('client_id', check.clientId).eq('is_active', true),
    admin.from('poses').select('*', { count: 'exact', head: true }).eq('client_id', check.clientId).eq('is_active', true),
    admin.from('characters').select('*', { count: 'exact', head: true }).eq('client_id', check.clientId),
    admin
      .from('outfit_generations')
      .select('id, model, prompt, error, created_at')
      .eq('client_id', check.clientId)
      .eq('status', 'failed')
      .order('created_at', { ascending: false })
      .limit(5),
  ]);

  const failureResult = failures as unknown as {
    data: RecentFailure[] | null;
    error: { message: string } | null;
  };
  const countResults = [total, today, succeeded, failed, outfits, poses, characters] as unknown as CountResult[];
  const queryError = countResults.find((result) => result.error)?.error ?? failureResult.error;
  if (queryError) return json({ error: 'LOAD_FAILED' }, 500);

  const totalCount = total.count ?? 0;
  const succeededCount = succeeded.count ?? 0;

  return json({
    data: {
      generations_total: totalCount,
      generations_today: today.count ?? 0,
      generations_succeeded: succeededCount,
      generations_failed: failed.count ?? 0,
      success_rate: totalCount === 0 ? 0 : Math.round((succeededCount / totalCount) * 1000) / 10,
      outfits_active: outfits.count ?? 0,
      poses_active: poses.count ?? 0,
      characters_total: characters.count ?? 0,
      recent_failures: failureResult.data ?? [],
    },
  });
}
