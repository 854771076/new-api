// src/pages/Profile.tsx — 个人中心：用户信息 + 跳转网关查看额度/充值。
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { getSupabase } from '@/lib/supabase';
import { config } from '@/lib/config';
import { getGatewayRefreshToken } from '@/lib/gatewayAuth';

interface ProfileUser {
  name?: string;
  email?: string;
  gatewayUserId?: string;
}

export function Profile() {
  const navigate = useNavigate();
  const [user, setUser] = useState<ProfileUser | null>(null);

  useEffect(() => {
    getSupabase().auth.getUser().then(({ data }) => {
      if (!data.user) { navigate('/login'); return; }

      // 网关 User.Id 来自 custom provider identity 的 sub；找不到时回退到
      // identities[0]（邮箱身份可能排在前面，仅作兜底）。
      const identity =
        data.user.identities?.find((item) => item.provider === 'custom:newapi') ??
        data.user.identities?.[0];
      const identityData = identity?.identity_data as { sub?: string } | undefined;
      const userMeta = (data.user.user_metadata ?? {}) as Record<string, unknown>;

      setUser({
        name: (userMeta.name as string | undefined) ?? data.user.email ?? undefined,
        email: data.user.email ?? undefined,
        gatewayUserId: identityData?.sub,
      });
    });
  }, [navigate]);

  async function handleLogout() {
    const supabase = getSupabase();

    try {
      // 先把网关 refresh token 撤销（RFC 7009）；公共客户端只带 client_id。
      // 本地刷新会轮换 refresh token，必须用 gatewayAuth 当前持有的值；
      // session.provider_refresh_token 可能已被轮换而失效。
      const refreshToken = await getGatewayRefreshToken();
      if (refreshToken) {
        await fetch(`${config.gatewayBaseUrl}/oauth/revoke`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
          body: new URLSearchParams({
            token: refreshToken,
            client_id: config.gatewayClientId,
          }).toString(),
        });
      }
    } catch {
      // 撤销失败不阻断本地登出。
    }

    await supabase.auth.signOut();
    navigate('/login', { replace: true });
  }

  if (!user) return <div className="p-6">加载中…</div>;

  return (
    <div className="p-6 max-w-lg mx-auto">
      <h1 className="text-2xl font-semibold mb-4">个人中心</h1>
      <dl className="divide-y rounded-lg border bg-white">
        <Row k="显示名"          v={user.name} />
        <Row k="邮箱"            v={user.email ?? '—'} />
        <Row k="网关 User.Id"    v={user.gatewayUserId ?? '未同步'} />
      </dl>

      <div className="mt-6 space-y-3">
        <a
          href={config.gatewayBaseUrl + '/app.html#/topup'}
          target="_blank" rel="noreferrer"
          className="block text-center py-3 rounded-lg bg-slate-900 text-white hover:bg-slate-700 transition"
        >
          在网关查看额度 / 充值
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
