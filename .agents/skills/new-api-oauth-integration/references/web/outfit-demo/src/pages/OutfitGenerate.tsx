// src/pages/OutfitGenerate.tsx — AI 换装生成页
// 选择服装 + 输入形象描述，AI 生成穿搭建议
import { useEffect, useState } from 'react';
import { streamChatCompletion } from '@/lib/gateway';
import { getSupabase } from '@/lib/supabase';

type Outfit = {
  id: string;
  name: string;
  category: string;
  description: string;
  price_credits: number;
};

export function OutfitGenerate() {
  const [outfits, setOutfits] = useState<Outfit[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [characterDesc, setCharacterDesc] = useState('');
  const [reply, setReply] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getSupabase()
      .from('official_outfits')
      .select('id, name, category, description, price_credits')
      .eq('is_active', true)
      .order('category')
      .then(({ data }) => setOutfits((data ?? []) as Outfit[]));
  }, []);

  const toggle = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!characterDesc.trim() || busy) return;
    setBusy(true);
    setReply('');

    const selectedOutfits = outfits.filter((o) => selected.has(o.id));
    const prompt = `用户形象描述：${characterDesc}

已选择服装：
${selectedOutfits.map((o) => `- ${o.name}（${o.category}）：${o.description}`).join('\n')}

请根据用户形象和已选服装，给出完整的穿搭建议。包括：
1. 整体风格定位
2. 每件单品的搭配理由
3. 配色建议
4. 场合适配
5. 改进建议`;

    await streamChatCompletion(
      'gpt-4o-mini',
      [{ role: 'user', content: prompt }],
      {
        onDelta: (t) => setReply((prev) => prev + t),
        onDone: async (fullText) => {
          try {
            await getSupabase().from('ai_generations').insert({
              model: 'gpt-4o-mini',
              prompt: characterDesc,
              response: fullText,
            });
          } catch { /* ignore */ }
          setBusy(false);
        },
        onError: (err) => {
          setReply(`⚠️ ${err.status} ${err.message}`);
          setBusy(false);
        },
      },
    );
  }

  const grouped = outfits.reduce<Record<string, Outfit[]>>((acc, o) => {
    (acc[o.category] ??= []).push(o);
    return acc;
  }, {});

  const categoryLabel: Record<string, string> = {
    top: '上衣', bottom: '下装', shoes: '鞋子', accessory: '配件',
  };

  return (
    <div className="p-6 max-w-4xl mx-auto">
      <h1 className="text-2xl font-semibold mb-4">AI 换装</h1>

      <form onSubmit={handleSubmit} className="space-y-6">
        {/* 形象描述 */}
        <div>
          <label className="block text-sm font-medium mb-2">你的形象描述</label>
          <textarea
            value={characterDesc}
            onChange={(e) => setCharacterDesc(e.target.value)}
            rows={3}
            placeholder="例如：25岁女性，身高165cm，偏瘦，肤色白皙，日常通勤风格…"
            className="w-full px-3 py-2 rounded-lg border bg-white resize-none"
          />
        </div>

        {/* 服装选择 */}
        <div>
          <label className="block text-sm font-medium mb-2">选择服装（可多选）</label>
          {Object.entries(grouped).map(([cat, items]) => (
            <div key={cat} className="mb-4">
              <h3 className="text-sm text-slate-500 mb-2">{categoryLabel[cat] ?? cat}</h3>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                {items.map((o) => (
                  <button
                    key={o.id}
                    type="button"
                    onClick={() => toggle(o.id)}
                    className={`p-3 rounded-lg border text-left text-sm transition ${
                      selected.has(o.id)
                        ? 'border-slate-900 bg-slate-900 text-white'
                        : 'border-slate-200 bg-white hover:border-slate-300'
                    }`}
                  >
                    <div className="font-medium">{o.name}</div>
                    <div className={`text-xs mt-1 ${selected.has(o.id) ? 'text-slate-300' : 'text-slate-500'}`}>
                      {o.price_credits > 0 ? `${o.price_credits} 积分` : '免费'}
                    </div>
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>

        <button
          type="submit"
          disabled={busy || selected.size === 0 || !characterDesc.trim()}
          className="w-full py-3 rounded-lg bg-slate-900 text-white disabled:opacity-40 hover:bg-slate-700 transition"
        >
          {busy ? '生成中…' : `生成穿搭建议（已选 ${selected.size} 件）`}
        </button>
      </form>

      {reply && (
        <div className="mt-6 p-4 rounded-lg bg-white border whitespace-pre-wrap">{reply}</div>
      )}
    </div>
  );
}
