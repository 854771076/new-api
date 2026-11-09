// src/pages/AuthCallback.tsx
// Supabase Auth → new-api OIDC 登录回调。
//
// 关键：Supabase Custom OIDC Provider 走授权码流程时，网关返回的
// access_token 会被 Supabase **存进 session.provider_token**，
// id_token 的 claims 会被映射进 user.identities[0].identity_data。
//
// 我们需要把：
//   gateway_access_token = session.provider_token   （网关 access_token）
//   gateway_user_id      = identity_data.sub        （id_token.sub，网关 User.Id）
// 持久化到 auth.users.app_metadata，后面所有 gateway client 都从那里读。
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
      if (error || !data.session || !data.user) {
        console.error('[AuthCallback] exchange failed', error);
        navigate('/login', { replace: true });
        return;
      }

      // ✅ 网关 access_token：Supabase 把 custom provider 返回的上游 access_token
      //    直接存进 session.provider_token（不是 identity_data 里！）
      const gatewayAccessToken = data.session.provider_token ?? null;

      // ✅ 网关 User.Id：来自 id_token.sub，Supabase 映射到 identity_data.sub
      const identityData = data.user.identities?.[0]?.identity_data as
        | { sub?: string; email?: string; name?: string; username?: string }
        | undefined;
      const gatewayUserId = identityData?.sub ?? null;

      console.info('[AuthCallback]', {
        gateway_user_id: gatewayUserId,
        has_gateway_access_token: !!gatewayAccessToken,
        provider: data.session.provider,
      });

      // 持久化到 app_metadata（幂等，后续登录覆盖）
      await supabase.auth.updateUser({
        data: {
          gateway_user_id: gatewayUserId,
          gateway_access_token: gatewayAccessToken,
          gateway_provider: data.session.provider,
        },
      });

      navigate('/', { replace: true });
    });
  }, [navigate, params]);

  return <div className="p-6 text-center">登录完成，正在跳转…</div>;
}
