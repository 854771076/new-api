// components/outfit-generator.tsx — AI 换装生成页的交互部分。
// 服装列表由 Server Component 取好后传入；这里只负责选择、流式生成和写回历史。
'use client';

import { useState } from 'react';
import { config } from '@/lib/config';
import { streamChatCompletion } from '@/lib/gateway';
import { getSupabase } from '@/lib/supabase/client';
import type { OutfitSummary } from '@/lib/types';

const MODEL = 'gpt-4o-mini';

const CATEGORY_LABEL: Record<string, string> = {
  top: '上衣',
  bottom: '下装',
  shoes: '鞋子',
  accessory: '配件',
};

export function OutfitGenerator({ outfits }: { outfits: OutfitSummary[] }) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [characterDesc, setCharacterDesc] = useState('');
  const [reply, setReply] = useState('');
  const [busy, setBusy] = useState(false);

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!characterDesc.trim() || busy) return;
    setBusy(true);
    setReply('');

    const selectedOutfits = outfits.filter((o) => selected.has(o.id));
    const prompt = `用户形象描述：${characterDesc}

已选择服装：
${selectedOutfits.map((o) => `- ${o.name}（${o.category}）：${o.description ?? ''}`).join('\n')}

请根据用户形象和已选服装，给出完整的穿搭建议。包括：
1. 整体风格定位
2. 每件单品的搭配理由
3. 配色建议
4. 场合适配
5. 改进建议`;

    await streamChatCompletion(MODEL, [{ role: 'user', content: prompt }], {
      onDelta: (t) => setReply((prev) => prev + t),
      onDone: async (fullText) => {
        // user_id / client_id 都是 NOT NULL，必须显式写入；PostgREST 的错误
        // 通过返回值返回而不是抛异常，所以要显式检查。
        try {
          const {
            data: { user },
          } = await getSupabase().auth.getUser();
          if (user) {
            const { error } = await getSupabase()
              .from('ai_generations')
              .insert({
                user_id: user.id,
                client_id: config.gatewayClientId,
                model: MODEL,
                prompt: characterDesc,
                response: fullText,
              });
            if (error) {
              console.error('[OutfitGenerator] failed to save history:', error.message);
            }
          }
        } catch (err) {
          console.error('[OutfitGenerator] failed to save history:', err);
        }
        setBusy(false);
      },
      onError: (err) => {
        setReply(`⚠️ ${err.status} ${err.message}`);
        setBusy(false);
      },
    });
  }

  const grouped = outfits.reduce<Record<string, OutfitSummary[]>>((acc, o) => {
    (acc[o.category] ??= []).push(o);
    return acc;
  }, {});

  return (
    <div className="mx-auto max-w-4xl p-6">
      <h1 className="mb-4 text-2xl font-semibold">AI 换装</h1>

      <form onSubmit={handleSubmit} className="space-y-6">
        <div>
          <label className="mb-2 block text-sm font-medium">你的形象描述</label>
          <textarea
            value={characterDesc}
            onChange={(e) => setCharacterDesc(e.target.value)}
            rows={3}
            placeholder="例如：25岁女性，身高165cm，偏瘦，肤色白皙，日常通勤风格…"
            className="textarea textarea-bordered w-full resize-none"
          />
        </div>

        <div>
          <label className="mb-2 block text-sm font-medium">选择服装（可多选）</label>
          {Object.entries(grouped).map(([category, items]) => (
            <div key={category} className="mb-4">
              <h3 className="mb-2 text-sm opacity-60">{CATEGORY_LABEL[category] ?? category}</h3>
              <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
                {items.map((o) => {
                  const active = selected.has(o.id);
                  return (
                    <button
                      key={o.id}
                      type="button"
                      onClick={() => toggle(o.id)}
                      className={`btn h-auto flex-col items-start gap-1 px-3 py-3 ${
                        active ? 'btn-neutral' : 'btn-outline'
                      }`}
                    >
                      <span className="font-medium">{o.name}</span>
                      <span className="text-xs font-normal opacity-70">
                        {o.price_credits > 0 ? `${o.price_credits} 积分` : '免费'}
                      </span>
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>

        <button
          type="submit"
          disabled={busy || selected.size === 0 || !characterDesc.trim()}
          className="btn btn-neutral w-full"
        >
          {busy && <span className="loading loading-spinner loading-sm" />}
          {busy ? '生成中…' : `生成穿搭建议（已选 ${selected.size} 件）`}
        </button>
      </form>

      {reply && (
        <div className="card mt-6 border border-base-300 bg-base-100">
          <div className="card-body whitespace-pre-wrap">{reply}</div>
        </div>
      )}
    </div>
  );
}
