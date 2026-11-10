// components/logout-button.tsx — 退出登录。
// 先吊销网关 refresh token（RFC 7009），再清 Supabase 会话。
'use client';

import { useRouter } from 'next/navigation';
import { config } from '@/lib/config';
import { getGatewayRefreshToken } from '@/lib/gatewayAuth';
import { getSupabase } from '@/lib/supabase/client';

export function LogoutButton() {
  const router = useRouter();

  async function handleLogout() {
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

    await getSupabase().auth.signOut();
    router.replace('/login');
    router.refresh();
  }

  return (
    <button type="button" onClick={handleLogout} className="btn btn-outline w-full">
      退出登录
    </button>
  );
}
