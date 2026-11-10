import { redirect } from 'next/navigation';
import Link from 'next/link';
import { createServerSupabase } from '@/lib/supabase/server';
import { MediaCard } from '@/components/MediaCard';
import { getSignedUrls } from '@/lib/media-urls';

export const dynamic = 'force-dynamic';

const PAGE_SIZE = 60;

export default async function PosesPage({
  searchParams,
}: {
  searchParams: Promise<{ system?: string; angle?: string; q?: string; page?: string }>;
}) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const sp = await searchParams;
  const page = Math.max(1, parseInt(sp.page ?? '1', 10) || 1);
  const from = (page - 1) * PAGE_SIZE;
  const q = sp.q?.trim();
  const activeSystem = sp.system?.trim();
  const activeAngle = sp.angle?.trim();

  // 1. system + angle distinct
  const [{ data: systems }, { data: angles }] = await Promise.all([
    supabase.from('poses').select('system').eq('is_active', true).not('system', 'is', null).order('system'),
    supabase.from('poses').select('angle').eq('is_active', true).not('angle', 'is', null).order('angle'),
  ]);
  const allSystems = Array.from(new Set((systems ?? []).map((p) => p.system).filter(Boolean))) as string[];
  const allAngles = Array.from(new Set((angles ?? []).map((p) => p.angle).filter(Boolean))) as string[];

  // 2. 查 poses
  let query = supabase
    .from('poses')
    .select('id, name_zh, system, category, angle, bucket, thumb_path', { count: 'exact' })
    .eq('is_active', true);
  if (activeSystem) query = query.eq('system', activeSystem);
  if (activeAngle) query = query.eq('angle', activeAngle);
  if (q) {
    const pattern = `%${q}%`;
    query = query.or(
      `name_zh.ilike.${pattern},name_en.ilike.${pattern},description_zh.ilike.${pattern},description_en.ilike.${pattern}`,
    );
  }

  const { data: poses, count: total, error } = await query
    .order('system').order('category').order('sort_order')
    .range(from, from + PAGE_SIZE - 1);

  if (error) {
    return (
      <div className="alert alert-error">
        <span>读取姿势库失败：{error.message}</span>
      </div>
    );
  }

  const totalPages = Math.max(1, Math.ceil((total ?? 0) / PAGE_SIZE));

  // 3. 批量 signed URL
  const paths = (poses ?? []).map((p) => p.thumb_path);
  const signedMap = await getSignedUrls('media-official', paths);

  const buildHref = (overrides: Record<string, string | undefined>) => {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries({ q, system: activeSystem, angle: activeAngle, ...overrides })) {
      if (v) params.set(k, v);
    }
    const s = params.toString();
    return s ? `/poses?${s}` : '/poses';
  };

  // 4. Empty state
  if (!poses || poses.length === 0) {
    return (
      <div className="space-y-6">
        <h1 className="text-2xl font-bold">姿势库</h1>

        <form action="/poses" method="get" className="flex w-full max-w-md gap-2">
          <input type="hidden" name="system" value={activeSystem ?? ''} />
          <input type="hidden" name="angle" value={activeAngle ?? ''} />
          <input name="q" type="text" defaultValue={q ?? ''} placeholder="搜索姿势名" className="input flex-1" />
          <button type="submit" className="btn btn-primary">搜索</button>
        </form>

        <div className="alert alert-warning alert-soft">
          <div>
            <h3 className="font-bold">没找到匹配的姿势</h3>
            <p>试试清除筛选条件</p>
          </div>
          <Link href="/poses" className="btn btn-sm btn-ghost">清除筛选</Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-bold">姿势库</h1>
        <span className="badge badge-ghost ml-auto">{total ?? 0} 个姿势</span>
      </div>

      {/* 搜索表单 */}
      <form action="/poses" method="get" className="flex w-full max-w-md gap-2">
        <input type="hidden" name="system" value={activeSystem ?? ''} />
        <input type="hidden" name="angle" value={activeAngle ?? ''} />
        <input name="q" type="text" defaultValue={q ?? ''} placeholder="搜索姿势名" className="input flex-1" />
        <button type="submit" className="btn btn-primary">搜索</button>
        {q ? (
          <Link href={buildHref({ q: undefined, page: '1' })} className="btn btn-outline">清除</Link>
        ) : null}
      </form>

      {/* system tabs */}
      {allSystems.length > 0 ? (
        <div role="tablist" className="tabs tabs-border flex-wrap">
          <a role="tab" className={`tab ${!activeSystem ? 'tab-active' : ''}`} href={buildHref({ system: undefined, page: '1' })}>全部系统</a>
          {allSystems.map((sys) => (
            <a key={sys} role="tab" className={`tab ${activeSystem === sys ? 'tab-active' : ''}`} href={buildHref({ system: sys, page: '1' })}>{sys}</a>
          ))}
        </div>
      ) : null}

      {/* angle tabs */}
      {allAngles.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          <Link href={buildHref({ angle: undefined, page: '1' })} className={`btn btn-xs normal-case ${!activeAngle ? 'btn-primary' : 'btn-ghost bg-base-200 text-base-content/70 hover:bg-base-300'}`}>全部角度</Link>
          {allAngles.map((a) => (
            <Link key={a} href={buildHref({ angle: a, page: '1' })} className={`btn btn-xs normal-case ${activeAngle === a ? 'btn-primary' : 'btn-ghost bg-base-200 text-base-content/70 hover:bg-base-300'}`}>{a}</Link>
          ))}
        </div>
      ) : null}

      {/* 图片网格 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
        {poses.map((p) => (
          <MediaCard
            key={p.id}
            title={p.name_zh}
            subtitle={`${p.category ?? ''}${p.angle ? ' · ' + p.angle : ''}`}
            signedUrl={signedMap[p.thumb_path]}
            aspectRatio="aspect-[3/4]"
            compact
          />
        ))}
      </div>

      {/* 分页 */}
      {totalPages > 1 ? (
        <div className="flex flex-wrap justify-center gap-1">
          {Array.from({ length: Math.min(7, totalPages) }, (_, i) => i + 1).map((p) => (
            <Link
              key={p}
              href={buildHref({ page: String(p) })}
              className={`btn btn-sm ${p === page ? 'btn-primary' : 'btn-outline'}`}
            >
              {p}
            </Link>
          ))}
          <span className="btn btn-sm btn-ghost">共 {total} 个</span>
        </div>
      ) : null}
    </div>
  );
}
