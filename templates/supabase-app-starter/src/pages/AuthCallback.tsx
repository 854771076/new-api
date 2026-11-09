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
import { useEffect } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { getSupabase } from '@/lib/supabase';
import { seedGatewayTokensFromSession } from '@/lib/gatewayAuth';

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
      if (error || !data.session || !data.user) {
        console.error('[AuthCallback] exchange failed', error);
        navigate('/login', { replace: true });
        return;
      }

      // session.provider_token / provider_refresh_token 是网关签发的令牌，
      // 交给 gatewayAuth 持有并在过期前自动刷新。
      seedGatewayTokensFromSession(data.session);

      try {
        // 让 Edge Function 用当前 Supabase access_token 校验身份并写入
        // user_profiles（user_profiles 的客户端写权限已撤销）。
        const { error: syncError } = await supabase.functions.invoke('user-sync', {
          method: 'POST',
        });
        if (syncError) {
          console.warn('[AuthCallback] user-sync failed', syncError.message);
        }
      } catch (e) {
        // 同步失败不阻断登录。
        console.warn('[AuthCallback] user-sync failed', e);
      }

      navigate('/', { replace: true });
    });
  }, [navigate, params]);

  return <div className="p-6 text-center">登录完成，正在跳转…</div>;
}
