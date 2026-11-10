import { redirect } from 'next/navigation';
import { LocalTime } from '@/components/local-time';
import { createServerSupabase } from '@/lib/supabase/server';
import type { AiGeneration } from '@/lib/types';

// 历史记录：从 Supabase 业务表读，RLS 只返回当前用户的行。
export default async function HistoryPage() {
  const supabase = await createServerSupabase();
  const {
    data: { user },
  } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const { data, error } = await supabase
    .from('ai_generations')
    .select('*')
    .order('created_at', { ascending: false })
    .limit(20);

  if (error) {
    return (
      <div className="mx-auto max-w-2xl p-6">
        <div role="alert" className="alert alert-error">
          <span>加载失败：{error.message}</span>
        </div>
      </div>
    );
  }

  const rows = (data ?? []) as AiGeneration[];

  return (
    <div className="mx-auto max-w-2xl p-6">
      <h1 className="mb-4 text-2xl font-semibold">历史记录</h1>
      {rows.length === 0 ? (
        <p className="opacity-60">还没有记录。去"AI 生成"页面跑一条吧。</p>
      ) : (
        <ul className="space-y-3">
          {rows.map((r) => (
            <li key={r.id} className="card border border-base-300 bg-base-100">
              <div className="card-body gap-2 p-4">
                <div className="flex justify-between text-xs opacity-60">
                  <span className="font-medium opacity-100">{r.model}</span>
                  <LocalTime value={r.created_at} />
                </div>
                <p className="text-sm">
                  <strong>Prompt:</strong> {r.prompt}
                </p>
                <p className="whitespace-pre-wrap text-sm">{r.response}</p>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
