// src/pages/AIGenerate.tsx — AI 流式生成页。
// 直接直连网关 relay；Supabase 侧只用做 RLS 隔离和历史存储（可选）。
import { useState } from 'react';
import { config } from '@/lib/config';
import { streamChatCompletion } from '@/lib/gateway';
import { getSupabase } from '@/lib/supabase';

const DEFAULT_MODEL = 'gpt-4o-mini'; // ← 改成你想默认用的模型

export function AIGenerate() {
  const [prompt, setPrompt] = useState('');
  const [reply, setReply]   = useState('');
  const [busy, setBusy]     = useState(false);
  const [model, setModel]   = useState(DEFAULT_MODEL);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!prompt.trim() || busy) return;
    setBusy(true);
    setReply('');

    await streamChatCompletion(
      model,
      [{ role: 'user', content: prompt }],
      {
        onDelta: (t) => setReply((prev) => prev + t),
        onDone: async (fullText) => {
          // 可选：把生成存到 ai_generations 表（RLS 自动隔离）。
          // user_id / client_id 都是 NOT NULL，必须显式写入。
          try {
            const { data: { user } } = await getSupabase().auth.getUser();
            if (user) {
              const { error } = await getSupabase().from('ai_generations').insert({
                user_id: user.id,
                client_id: config.gatewayClientId,
                model,
                prompt,
                response: fullText,
              });
              if (error) {
                console.error('[AIGenerate] failed to save history:', error.message);
              }
            }
          } catch (err) {
            console.error('[AIGenerate] failed to save history:', err);
          }
          setBusy(false);
        },
        onError: (err) => {
          setReply(`⚠️ ${err.status} ${err.message}`);
          setBusy(false);
        },
      },
    );
  }

  return (
    <div className="p-6 max-w-2xl mx-auto">
      <h1 className="text-2xl font-semibold mb-4">AI 生成</h1>

      <form onSubmit={handleSubmit} className="space-y-3">
        <select
          value={model}
          onChange={(e) => setModel(e.target.value)}
          className="w-full px-3 py-2 rounded-lg border bg-white"
        >
          {/* 按网关实际可用模型改列表；也可以做成动态拉取 /v1/models */}
          <option value="gpt-4o-mini">gpt-4o-mini</option>
          <option value="gpt-4o">gpt-4o</option>
          <option value="claude-3-5-sonnet">claude-3-5-sonnet</option>
        </select>

        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={4}
          placeholder="输入你的 prompt…"
          className="w-full px-3 py-2 rounded-lg border bg-white resize-none"
        />
        <button
          type="submit"
          disabled={busy}
          className="w-full py-3 rounded-lg bg-slate-900 text-white disabled:opacity-40 hover:bg-slate-700 transition"
        >
          {busy ? '生成中…' : '生成'}
        </button>
      </form>

      {reply && (
        <div className="mt-6 p-4 rounded-lg bg-white border whitespace-pre-wrap">{reply}</div>
      )}
    </div>
  );
}
