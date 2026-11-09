// src/pages/AuthCallback.tsx
// Supabase Auth → new-api OIDC 登录回调。
// 页面职责：从 URL hash 里取 Supabase session，把网关发的 access_token
// （Supabase 会把 custom provider 返回的 extra_access_token 存进 user.identities[0]）
// 写进 app_metadata.gateway_access_token，然后跳回首页。
import { useEffect } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { getSupabase } from '@/lib/supabase';

export function AuthCallback() {
  const navigate = useNavigate();
  const [params] = useSearchParams();

  useEffect(() => {
    const code = params.get('code');
    if (!code) {
      navigate('/', { replace: true });
      return;
    }

    const supabase = getSupabase();
    supabase.auth.exchangeCodeForSession(code).then(async ({ data, error }) => {
      if (error || !data.user) {
        console.error('exchange failed', error);
        navigate('/login', { replace: true });
        return;
      }

      // new-api custom provider 返回的 access_token 在
      // user.identities[0].identity_data 里（Supabase 的 OIDC 默认映射）。
      const rawIdentity = data.user.identities?.[0]?.identity_data as
        | { gateway_access_token?: string; access_token?: string; provider?: string }
        | undefined;

      const gatewayToken = rawIdentity?.gateway_access_token
        ?? rawIdentity?.access_token;

      // 写进 app_metadata，后面所有 gateway client 都从这里读
      await supabase.auth.updateUser({
        data: {
          gateway_user_id: rawIdentity?.sub,          // 网关 User.Id（来自 id_token.sub）
          gateway_access_token: gatewayToken ?? null,
          gateway_provider: rawIdentity?.provider ?? 'newapi',
        },
      });

      navigate('/', { replace: true });
    });
  }, [navigate, params]);

  return <div className="p-6 text-center">登录完成，正在跳转…</div>;
}
