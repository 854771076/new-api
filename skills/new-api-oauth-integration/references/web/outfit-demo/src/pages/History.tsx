// src/pages/History.tsx — 历史记录（从 Supabase 业务表读；RLS 隔离）。
import { useEffect, useState } from 'react';
import { getSupabase } from '@/lib/supabase';

type Row = { id: string; model: string; prompt: string; response: string; created_at: string };

export function History() {
  const [rows, setRows] = useState<Row[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getSupabase()
      .from('ai_generations')
      .select('*')
      .order('created_at', { ascending: false })
      .limit(20)
      .then(({ data, error }) => {
        if (error) setError(error.message);
        else setRows((data ?? []) as Row[]);
      });
  }, []);

  if (error) return <div className="p-6 text-red-600">加载失败：{error}</div>;
  if (!rows) return <div className="p-6">加载中…</div>;

  return (
    <div className="p-6 max-w-2xl mx-auto">
      <h1 className="text-2xl font-semibold mb-4">历史记录</h1>
      {rows.length === 0 ? (
        <p className="text-slate-500">还没有记录。去"AI 生成"页面跑一条吧。</p>
      ) : (
        <ul className="space-y-3">
          {rows.map((r) => (
            <li key={r.id} className="p-4 rounded-lg border bg-white">
              <div className="flex justify-between text-xs text-slate-500 mb-2">
                <span className="font-medium text-slate-700">{r.model}</span>
                <time>{new Date(r.created_at).toLocaleString()}</time>
              </div>
              <p className="text-sm mb-2"><strong>Prompt:</strong> {r.prompt}</p>
              <p className="text-sm whitespace-pre-wrap">{r.response}</p>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
