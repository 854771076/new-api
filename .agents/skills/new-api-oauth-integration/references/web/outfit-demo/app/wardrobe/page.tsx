import Link from 'next/link';
import { redirect } from 'next/navigation';
import { createServerSupabase } from '@/lib/supabase/server';
import type { Outfit } from '@/lib/types';

// 服装浏览页：官方服装全部可读，RLS 按 client_id + 登录身份隔离。
const CATEGORIES = [
  { key: 'all', label: '全部' },
  { key: 'top', label: '上衣' },
  { key: 'bottom', label: '下装' },
  { key: 'shoes', label: '鞋子' },
  { key: 'accessory', label: '配件' },
];

const CATEGORY_EMOJI: Record<string, string> = {
  top: '👕',
  bottom: '👖',
  shoes: '👟',
  accessory: '🧢',
};

export default async function WardrobePage({
  searchParams,
}: {
  searchParams: Promise<{ category?: string }>;
}) {
  const supabase = await createServerSupabase();
  const {
    data: { user },
  } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const { category = 'all' } = await searchParams;

  let query = supabase
    .from('official_outfits')
    .select('*')
    .eq('is_active', true)
    .order('category')
    .order('name');

  if (category !== 'all') {
    query = query.eq('category', category);
  }

  const { data, error } = await query;

  if (error) {
    return (
      <div className="mx-auto max-w-4xl p-6">
        <div role="alert" className="alert alert-error">
          <span>加载失败：{error.message}</span>
        </div>
      </div>
    );
  }

  const outfits = (data ?? []) as Outfit[];

  return (
    <div className="mx-auto max-w-4xl p-6">
      <h1 className="mb-4 text-2xl font-semibold">服装库</h1>

      <div className="mb-6 flex gap-2 overflow-x-auto pb-2">
        {CATEGORIES.map((c) => (
          <Link
            key={c.key}
            href={c.key === 'all' ? '/wardrobe' : `/wardrobe?category=${c.key}`}
            className={`btn btn-sm rounded-full whitespace-nowrap ${
              category === c.key ? 'btn-neutral' : 'btn-ghost bg-base-100'
            }`}
          >
            {c.label}
          </Link>
        ))}
      </div>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {outfits.map((o) => (
          <div
            key={o.id}
            className="card overflow-hidden border border-base-300 bg-base-100 transition hover:shadow-md"
          >
            <div className="flex aspect-square items-center justify-center bg-base-200 text-4xl">
              {CATEGORY_EMOJI[o.category] ?? '🧥'}
            </div>
            <div className="card-body gap-1 p-3">
              <h3 className="text-sm font-medium">{o.name}</h3>
              <p className="text-xs opacity-60">{o.description ?? ''}</p>
              <div className="mt-1 flex items-center justify-between">
                <span className="text-xs opacity-50">{(o.tags ?? []).join(' · ')}</span>
                <span className="badge badge-sm badge-warning badge-outline">
                  {o.price_credits > 0 ? `${o.price_credits} 积分` : '免费'}
                </span>
              </div>
            </div>
          </div>
        ))}
      </div>

      {outfits.length === 0 && <p className="py-12 text-center opacity-60">该分类下暂无服装</p>}
    </div>
  );
}
