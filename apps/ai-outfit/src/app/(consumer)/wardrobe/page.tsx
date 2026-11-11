import { redirect } from 'next/navigation';
import Link from 'next/link';
import { createServerSupabase } from '@/lib/supabase/server';
import { MediaCard } from '@/components/MediaCard';
import { getSignedUrls } from '@/lib/media-urls';
import { config } from '@/lib/config';
import { PersonalWardrobeClient, type PersonalGarmentItem } from './_components/PersonalWardrobeClient';

export const dynamic = 'force-dynamic';

const PAGE_SIZE = 20;
type Tab = 'official' | 'personal';

export default async function WardrobePage({
  searchParams,
}: {
  searchParams: Promise<{ category?: string; q?: string; page?: string; tab?: string }>;
}) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const sp = await searchParams;
  const tab: Tab = sp.tab === 'personal' ? 'personal' : 'official';
  const requestedPage = Number(sp.page ?? '1');
  const page = Number.isSafeInteger(requestedPage) && requestedPage > 0 && requestedPage <= Number.MAX_SAFE_INTEGER / PAGE_SIZE ? requestedPage : 1;
  const from = (page - 1) * PAGE_SIZE;
  const q = sp.q?.trim();
  const activeCategory = sp.category?.trim();

  const buildHref = (overrides: Record<string, string | undefined>) => {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries({ q, category: activeCategory, tab, ...overrides })) {
      if (v) params.set(k, v);
    }
    const s = params.toString();
    return s ? `/wardrobe?${s}` : '/wardrobe';
  };

  // 个人衣橱：仅当前用户 source='user' 的服装（RLS 已强制 owner 隔离），独立分支渲染。
  if (tab === 'personal') {
    const { data: personalRows, error: personalErr } = await supabase
      .from('outfits')
      .select('id, name_zh, category, description_zh, parse_status, thumb_path, created_at')
      .eq('client_id', config.gatewayClientId)
      .eq('source', 'user')
      .eq('user_id', user.id)
      .order('created_at', { ascending: false }).order('id');

    if (personalErr) {
      return (
        <div className="space-y-6">
          <h1 className="text-2xl font-bold">服装库</h1>
          <WardrobeTabs tab={tab} buildHref={buildHref} />
          <div className="alert alert-error"><span>读取我的服装失败：{personalErr.message}</span></div>
        </div>
      );
    }

    const personalSigned = await getSignedUrls('media-user', (personalRows ?? []).map((o) => o.thumb_path));
    const items: PersonalGarmentItem[] = (personalRows ?? []).map((o) => ({
      id: o.id,
      name_zh: o.name_zh,
      category: o.category,
      description_zh: o.description_zh,
      parse_status: o.parse_status,
      created_at: o.created_at,
      signed_url: personalSigned[o.thumb_path],
    }));

    return (
      <div className="space-y-6">
        <h1 className="text-2xl font-bold">服装库</h1>
        <WardrobeTabs tab={tab} buildHref={buildHref} />
        <PersonalWardrobeClient initialItems={items} />
      </div>
    );
  }

  // 官方衣橱：source='official' 且启用，对当前 App 全体用户可见。
  const { data: catRows } = await supabase
    .from('outfits')
    .select('category')
    .eq('client_id', config.gatewayClientId)
    .eq('source', 'official')
    .eq('is_active', true)
    .not('category', 'is', null);
  const categories = Array.from(new Set((catRows ?? []).map((r) => r.category).filter(Boolean))) as string[];

  // outfits 查询
  let query = supabase
    .from('outfits')
    .select('id, name_zh, name_en, description_zh, description_en, category, bucket, thumb_path, sort_order', { count: 'exact' })
    .eq('client_id', config.gatewayClientId)
    .eq('source', 'official')
    .eq('is_active', true);
  if (activeCategory) query = query.eq('category', activeCategory);
  if (q) {
    const pattern = `%${q}%`.replaceAll('\\', '\\\\').replaceAll('"', '\\"');
    query = query.or(
      `name_zh.ilike."${pattern}",name_en.ilike."${pattern}",description_zh.ilike."${pattern}",description_en.ilike."${pattern}"`,
    );
  }

  const { data: rows, count: total, error: err } = await query
    .order('sort_order', { ascending: true }).order('id', { ascending: true })
    .range(from, from + PAGE_SIZE - 1);

  if (err) {
    return (
      <div className="space-y-6">
        <h1 className="text-2xl font-bold">服装库</h1>
        <WardrobeTabs tab={tab} buildHref={buildHref} />
        <div className="alert alert-error"><span>读取服装库失败：{err.message}</span></div>
      </div>
    );
  }

  const totalPages = Math.max(1, Math.ceil((total ?? 0) / PAGE_SIZE));

  // signed URL：官方服装全部位于 media-official。
  const signedMap = await getSignedUrls('media-official', (rows ?? []).map((o) => o.thumb_path));

  if (page > totalPages) redirect(buildHref({ page: String(totalPages) }));
  const firstPage = Math.min(Math.max(1, page - 3), Math.max(1, totalPages - 6));

  // Empty state
  if (!rows || rows.length === 0) {
    return (
      <div className="space-y-6">
        <h1 className="text-2xl font-bold">服装库</h1>
        <WardrobeTabs tab={tab} buildHref={buildHref} />

        <form action="/wardrobe" method="get" className="flex w-full max-w-md gap-2">
          <input type="hidden" name="tab" value={tab} />
          <input type="hidden" name="category" value={activeCategory ?? ''} />
          <input
            name="q"
            type="text"
            defaultValue={q ?? ''}
            placeholder="搜索服装名 / 描述"
            className="input flex-1"
          />
          <button type="submit" className="btn btn-primary">搜索</button>
        </form>

        {categories.length > 0 ? (
          <div role="tablist" className="tabs tabs-border flex-wrap">
            <a role="tab" className={`tab ${!activeCategory ? 'tab-active' : ''}`} href={buildHref({ category: undefined, page: '1' })}>全部</a>
            {categories.map((c) => (
              <a key={c} role="tab" className={`tab ${activeCategory === c ? 'tab-active' : ''}`} href={buildHref({ category: c, page: '1' })}>{c}</a>
            ))}
          </div>
        ) : null}

        <div className="alert alert-warning alert-soft">
          <div>
            <h3 className="font-bold">没找到匹配的服装</h3>
            <p>试试清除筛选条件或换个关键词，也可以上传你自己的服装。</p>
          </div>
          <div className="flex gap-2">
            <Link href={buildHref({ q: undefined, page: '1' })} className="btn btn-sm btn-ghost">清除筛选</Link>
            <Link href={buildHref({ tab: 'personal' })} className="btn btn-sm btn-primary">我的服装</Link>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-2xl font-bold">服装库</h1>
        <span className="badge badge-ghost ml-auto">{total ?? 0} 套</span>
      </div>

      <WardrobeTabs tab={tab} buildHref={buildHref} />

      {/* 搜索表单 */}
      <form action="/wardrobe" method="get" className="flex w-full max-w-md gap-2">
        <input type="hidden" name="tab" value={tab} />
        <input type="hidden" name="category" value={activeCategory ?? ''} />
        <input
          name="q"
          type="text"
          defaultValue={q ?? ''}
          placeholder="搜索服装名 / 描述"
          className="input flex-1"
        />
        <button type="submit" className="btn btn-primary">搜索</button>
        {q ? (
          <Link href={buildHref({ q: undefined, page: '1' })} className="btn btn-outline">清除</Link>
        ) : null}
      </form>

      {/* category tabs */}
      {categories.length > 0 ? (
        <div role="tablist" className="tabs tabs-border flex-wrap">
          <a role="tab" className={`tab ${!activeCategory ? 'tab-active' : ''}`} href={buildHref({ category: undefined, page: '1' })}>全部</a>
          {categories.map((c) => (
            <a key={c} role="tab" className={`tab ${activeCategory === c ? 'tab-active' : ''}`} href={buildHref({ category: c, page: '1' })}>{c}</a>
          ))}
        </div>
      ) : null}

      {/* 图片网格 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
        {rows.map((o) => (
          <MediaCard
            key={o.id}
            title={o.name_zh}
            subtitle={o.category ?? null}
            signedUrl={signedMap[o.thumb_path]}
            aspectRatio="aspect-[3/4]"
            compact
          />
        ))}
      </div>

      {/* 分页 */}
      {totalPages > 1 ? (
        <nav aria-label="服装分页" className="flex flex-wrap justify-center gap-1">
          {page > 1 ? <Link href={buildHref({ page: String(page - 1) })} className="btn btn-sm btn-outline">上一页</Link> : <button className="btn btn-sm btn-outline" disabled>上一页</button>}
          {Array.from({ length: Math.min(7, totalPages) }, (_, i) => firstPage + i).map((p) => (
            <Link
              key={p}
              href={buildHref({ page: String(p) })}
              aria-current={p === page ? 'page' : undefined}
              className={`btn btn-sm ${p === page ? 'btn-primary' : 'btn-outline'}`}
            >
              {p}
            </Link>
          ))}
          {page < totalPages ? <Link href={buildHref({ page: String(page + 1) })} className="btn btn-sm btn-outline">下一页</Link> : <button className="btn btn-sm btn-outline" disabled>下一页</button>}
          <span className="self-center px-2 text-sm">第 {page} / {totalPages} 页 · 共 {total} 套</span>
        </nav>
      ) : null}
    </div>
  );
}

function WardrobeTabs({ tab, buildHref }: { tab: Tab; buildHref: (o: Record<string, string | undefined>) => string }) {
  return (
    <div role="tablist" className="tabs tabs-box">
      <a role="tab" className={`tab ${tab === 'official' ? 'tab-active' : ''}`} href={buildHref({ tab: 'official', page: '1' })}>官方服装</a>
      <a role="tab" className={`tab ${tab === 'personal' ? 'tab-active' : ''}`} href={buildHref({ tab: 'personal', page: '1' })}>我的服装</a>
    </div>
  );
}
