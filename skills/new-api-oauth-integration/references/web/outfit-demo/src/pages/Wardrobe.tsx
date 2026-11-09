// src/pages/Wardrobe.tsx — 服装浏览页
// 展示官方服装和用户自有服装，RLS 自动按 client_id + auth.uid() 隔离
import { useEffect, useState } from 'react';
import { getSupabase } from '@/lib/supabase';

type Outfit = {
  id: string;
  name: string;
  category: string;
  description: string;
  image_url: string;
  tags: string[];
  price_credits: number;
  is_active: boolean;
};

const CATEGORIES = [
  { key: 'all', label: '全部' },
  { key: 'top', label: '上衣' },
  { key: 'bottom', label: '下装' },
  { key: 'shoes', label: '鞋子' },
  { key: 'accessory', label: '配件' },
];

export function Wardrobe() {
  const [outfits, setOutfits] = useState<Outfit[] | null>(null);
  const [category, setCategory] = useState('all');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let query = getSupabase()
      .from('official_outfits')
      .select('*')
      .eq('is_active', true)
      .order('category')
      .order('name');

    if (category !== 'all') {
      query = query.eq('category', category);
    }

    query.then(({ data, error }) => {
      if (error) setError(error.message);
      else setOutfits((data ?? []) as Outfit[]);
    });
  }, [category]);

  if (error) return <div className="p-6 text-red-600">加载失败：{error}</div>;
  if (!outfits) return <div className="p-6">加载中…</div>;

  return (
    <div className="p-6 max-w-4xl mx-auto">
      <h1 className="text-2xl font-semibold mb-4">服装库</h1>

      {/* 分类筛选 */}
      <div className="flex gap-2 mb-6 overflow-x-auto pb-2">
        {CATEGORIES.map((c) => (
          <button
            key={c.key}
            onClick={() => setCategory(c.key)}
            className={`px-4 py-2 rounded-full text-sm whitespace-nowrap transition ${
              category === c.key
                ? 'bg-slate-900 text-white'
                : 'bg-slate-100 hover:bg-slate-200'
            }`}
          >
            {c.label}
          </button>
        ))}
      </div>

      {/* 服装网格 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {outfits.map((o) => (
          <div
            key={o.id}
            className="rounded-lg border bg-white overflow-hidden hover:shadow-md transition"
          >
            <div className="aspect-square bg-slate-100 flex items-center justify-center text-4xl">
              {o.category === 'top' && '👕'}
              {o.category === 'bottom' && '👖'}
              {o.category === 'shoes' && '👟'}
              {o.category === 'accessory' && '🧢'}
            </div>
            <div className="p-3">
              <h3 className="font-medium text-sm">{o.name}</h3>
              <p className="text-xs text-slate-500 mt-1">{o.description}</p>
              <div className="flex items-center justify-between mt-2">
                <span className="text-xs text-slate-400">{o.tags.join(' · ')}</span>
                <span className="text-xs font-medium text-amber-600">
                  {o.price_credits > 0 ? `${o.price_credits} 积分` : '免费'}
                </span>
              </div>
            </div>
          </div>
        ))}
      </div>

      {outfits.length === 0 && (
        <p className="text-center text-slate-500 py-12">该分类下暂无服装</p>
      )}
    </div>
  );
}
