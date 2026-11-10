import { redirect } from 'next/navigation';
import Link from 'next/link';
import { createServerSupabase } from '@/lib/supabase/server';

export const dynamic = 'force-dynamic';

const STATUS_BADGE: Record<string, { cls: string; label: string }> = {
  pending: { cls: 'badge-warning', label: '生成中' },
  succeeded: { cls: 'badge-success', label: '成功' },
  failed: { cls: 'badge-error', label: '失败' },
};

export default async function HistoryPage() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const { data: rows, error } = await supabase
    .from('outfit_generations')
    .select('id, status, model, prompt, error, created_at, updated_at, result_path')
    .eq('user_id', user.id)
    .order('created_at', { ascending: false })
    .limit(50);

  if (error) return <div className="alert alert-error">读取历史失败：{error.message}</div>;

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-bold">作品历史</h1>
      <div className="overflow-x-auto">
        <table className="table table-sm table-zebra bg-base-100">
          <thead>
            <tr>
              <th>状态</th>
              <th>模型</th>
              <th>提示词</th>
              <th>时间</th>
            </tr>
          </thead>
          <tbody>
            {(rows ?? []).map((g) => {
              const badge = STATUS_BADGE[g.status] ?? { cls: 'badge-ghost', label: g.status };
              return (
                <tr key={g.id}>
                  <td><span className={`badge ${badge.cls}`}>{badge.label}</span></td>
                  <td className="text-xs">{g.model}</td>
                  <td className="max-w-md truncate text-xs">{g.prompt ?? g.error ?? '—'}</td>
                  <td className="text-xs">{new Date(g.created_at).toLocaleString()}</td>
                </tr>
              );
            })}
            {!rows || rows.length === 0 ? (
              <tr><td colSpan={4} className="text-center text-base-content/50">还没有生成过作品</td></tr>
            ) : null}
          </tbody>
        </table>
      </div>
      <div className="flex gap-2">
        <Link href="/studio" className="btn btn-primary btn-sm">去生成新作品</Link>
      </div>
    </div>
  );
}
