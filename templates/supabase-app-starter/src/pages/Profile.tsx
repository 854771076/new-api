// src/pages/Profile.tsx — 个人中心：用户信息 + 网关额度 + 跳转充值。
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { getSupabase } from '@/lib/supabase';
import { getGatewayQuota } from '@/lib/gateway';

export function Profile() {
  const navigate = useNavigate();
  const [user, setUser] = useState<{ name?: string; email?: string; gatewayUserId?: number } | null>(null);
  const [quota, setQuota] = useState<{ quota: number; used: number } | null>(null);

  useEffect(() => {
    const sb = getSupabase();
    sb.auth.getUser().then(async ({ data }) => {
      if (!data.user) { navigate('/login'); return; }
      const meta = data.user.app_metadata as Record<string, unknown>;
      setUser({
        name:    (meta.name as string) ?? data.user.user_metadata?.name ?? data.user.email,
        email:   data.user.email ?? undefined,
        gatewayUserId: Number(meta.gateway_user_id ?? 0) || undefined,
      });
      setQuota(await getGatewayQuota());
    });
  }, [navigate]);

  async function handleLogout() {
    await getSupabase().auth.signOut();
    navigate('/login', { replace: true });
  }

  if (!user) return <div className="p-6">加载中…</div>;

  return (
    <div className="p-6 max-w-lg mx-auto">
      <h1 className="text-2xl font-semibold mb-4">个人中心</h1>
      <dl className="divide-y rounded-lg border bg-white">
        <Row k="显示名"          v={user.name} />
        <Row k="邮箱"            v={user.email ?? '—'} />
        <Row k="网关 User.Id"    v={user.gatewayUserId?.toString() ?? '未同步'} />
        <Row k="网关额度"
             v={quota ? `¥${(quota.quota / 10000).toFixed(2)}（已用 ¥${(quota.used / 10000).toFixed(2)}）` : '—'} />
      </dl>

      <div className="mt-6 space-y-3">
        <a
          href={import.meta.env.VITE_GATEWAY_BASE_URL + '/app.html#/topup'}
          target="_blank" rel="noreferrer"
          className="block text-center py-3 rounded-lg bg-slate-900 text-white hover:bg-slate-700 transition"
        >
          去网关充值
        </a>
        <button
          onClick={handleLogout}
          className="w-full py-3 rounded-lg border hover:bg-slate-100 transition"
        >
          退出登录
        </button>
      </div>
    </div>
  );
}

function Row({ k, v }: { k: string; v?: string }) {
  return (
    <div className="px-4 py-3 flex justify-between text-sm">
      <dt className="text-slate-500">{k}</dt>
      <dd className="font-medium">{v}</dd>
    </div>
  );
}
