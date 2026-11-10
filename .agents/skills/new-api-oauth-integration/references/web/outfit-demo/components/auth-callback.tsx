// components/auth-callback.tsx
// Supabase Auth → new-api OIDC 登录回调。
//
// 关键：Supabase Custom OIDC Provider 走授权码流程时，网关返回的
// access_token 会被 Supabase **存进 session.provider_token**，
// refresh_token 存在 session.provider_refresh_token；id_token 的 claims
// 会被映射进 user.identities[].identity_data。
//
// 网关令牌只由 gatewayAuth 在本地持有（内存 + sessionStorage）并自动轮换，
// 不写入 user_metadata / app_metadata。
'use client';

import { useEffect, useRef } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { getSupabase } from '@/lib/supabase/client';
import { seedGatewayTokensFromSession } from '@/lib/gatewayAuth';

export function AuthCallback() {
  const router = useRouter();
  const params = useSearchParams();
  // React StrictMode 会重复执行 effect，而授权码只能兑换一次；第二次兑换必然
  // 报 invalid_grant 并把用户踢回登录页，所以这里只允许执行一次。
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;

    const code = params.get('code');
    if (!code) {
      router.replace('/');
      return;
    }

    void (async () => {
      const supabase = getSupabase();
      const { data, error } = await supabase.auth.exchangeCodeForSession(code);
      if (error || !data.session || !data.user) {
        console.error('[AuthCallback] exchange failed', error);
        router.replace('/login');
        return;
      }

      // session.provider_token / provider_refresh_token 是网关签发的令牌，
      // 交给 gatewayAuth 持有并在过期前自动刷新。
      seedGatewayTokensFromSession(data.session);

      try {
        // 让 Edge Function 用当前 Supabase access_token 校验身份并写入
        // user_profiles（user_profiles 的客户端写权限已撤销）。
        // 同时带上网关 access_token：Supabase 不会把 provider_token 存进
        // app_metadata，只有这里能拿到；函数会先校验其 sub 与已验证身份一致
        // 才会用它调 /oauth/userinfo 补全 display_name。
        const { error: syncError } = await supabase.functions.invoke('user-sync', {
          method: 'POST',
          body: { gateway_access_token: data.session.provider_token ?? '' },
        });
        if (syncError) {
          console.warn('[AuthCallback] user-sync failed', syncError.message);
        }
      } catch (e) {
        // 同步失败不阻断登录。
        console.warn('[AuthCallback] user-sync failed', e);
      }

      // 兑换后会话已写进 Cookie，跳首页会由 Server Component 重新读取。
      router.replace('/');
      router.refresh();
    })();
  }, [params, router]);

  return <div className="p-6 text-center">登录完成，正在跳转…</div>;
}
