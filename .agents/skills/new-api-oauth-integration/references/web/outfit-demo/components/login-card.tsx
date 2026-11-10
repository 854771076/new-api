// components/login-card.tsx — 触发 Supabase Auth custom provider 登录。
// Supabase 已在 Auth → Configuration → Providers → Custom 上配置好 new-api 作为
// OIDC Provider（identifier 形如 "custom:newapi"）。signInWithOAuth 的 provider
// 参数必须与 identifier 完全一致，包括 custom: 前缀。
'use client';

import { useState } from 'react';
import { getSupabase } from '@/lib/supabase/client';

// ⚠️ Supabase Custom Provider identifier 强制 "custom:" 前缀，必须精确匹配运营配置值
const OIDC_PROVIDER = 'custom:newapi';

export function LoginCard() {
  const [error, setError] = useState<string | null>(null);

  async function handleLogin() {
    setError(null);
    const { error: authError } = await getSupabase().auth.signInWithOAuth({
      provider: OIDC_PROVIDER,
      options: {
        redirectTo: `${window.location.origin}/auth/callback`,
        // Supabase custom provider 的 extra_scopes：要求网关返回
        // api:read + api:write，这样 access_token 才能调 relay
        scopes: 'openid profile email api:read api:write',
      },
    });
    if (authError) setError(`登录失败: ${authError.message}`);
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-base-200">
      <div className="card w-full max-w-sm bg-base-100 shadow-xl">
        <div className="card-body text-center">
          <h1 className="card-title justify-center text-2xl">AI App Starter</h1>
          <p className="text-sm opacity-60">通过网关统一身份登录</p>
          <button type="button" onClick={handleLogin} className="btn btn-neutral w-full">
            使用网关账号登录
          </button>
          {error && (
            <div role="alert" className="alert alert-error text-sm">
              <span>{error}</span>
            </div>
          )}
          <p className="text-xs opacity-50">新用户会自动在网关 + Supabase Auth 两侧创建账号</p>
        </div>
      </div>
    </div>
  );
}
