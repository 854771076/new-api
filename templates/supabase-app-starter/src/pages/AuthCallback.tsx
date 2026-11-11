// src/pages/AuthCallback.tsx
// Supabase Auth → new-api OIDC 登录回调。
//
// 关键：Supabase Custom OIDC Provider 走授权码流程时，网关返回的
// access_token 会被 Supabase **存进 session.provider_token**，
// refresh_token 存在 session.provider_refresh_token；id_token 的 claims
// 会被映射进 user.identities[].identity_data。
//
// 网关令牌只由 gatewayAuth 在本地持有（内存 + sessionStorage）并自动轮换，
// 不写入 user_metadata / app_metadata。
import { useEffect, useRef } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { getSupabase } from '@/lib/supabase';
import { seedGatewayTokensFromSession } from '@/lib/gatewayAuth';

export function AuthCallback() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const exchange = useRef<{ code: string; result: Promise<boolean> } | null>(null);

  useEffect(() => {
    const code = params.get('code');
    if (!code) {
      navigate('/', { replace: true });
      return;
    }

    if (exchange.current?.code !== code) {
      const supabase = getSupabase();
      exchange.current = {
        code,
        result: supabase.auth.exchangeCodeForSession(code).then(async ({ data, error }) => {
          if (error || !data.session || !data.user) {
            console.error('[AuthCallback] exchange failed', error?.message);
            return false;
          }
          seedGatewayTokensFromSession(data.session);
          const { error: syncError } = await supabase.functions.invoke('user-sync', {
            method: 'POST',
          });
          if (syncError) console.warn('[AuthCallback] user-sync failed', syncError.message);
          return true;
        }),
      };
    }
    let active = true;
    void exchange.current.result.then((success) => {
      if (active) navigate(success ? '/' : '/login', { replace: true });
    }).catch(() => {
      if (active) navigate('/login', { replace: true });
    });
    return () => { active = false; };
  }, [navigate, params]);

  return <div className="p-6 text-center">登录完成，正在跳转…</div>;
}
