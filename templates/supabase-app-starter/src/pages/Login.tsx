// src/pages/Login.tsx — 触发 Supabase Auth custom provider 登录。
// Supabase 已在 Auth → Configuration → Providers → Custom 上配置好 new-api 作为
// OIDC Provider。这里只要 signInWithIdTokenCredentials / signInWithOAuth 选到
// 那个 provider 即可。脚手架默认用 provider slug = "newapi"（运营配置时填的 slug）。
import { getSupabase } from '@/lib/supabase';

const OIDC_PROVIDER = 'newapi'; // ← 改成 Supabase 后台自定义 provider 的 slug

export function Login() {
  async function handleLogin() {
    const supabase = getSupabase();
    const origin = typeof window !== 'undefined' ? window.location.origin : '';
    const { error } = await supabase.auth.signInWithOAuth({
      provider: OIDC_PROVIDER,
      options: {
        redirectTo: `${origin}/auth/callback`,
        // Supabase custom provider 的 extra_scopes：要求网关返回
        // api:read + api:write，这样 access_token 才能调 relay
        scopes: 'openid profile email api:read api:write',
      },
    });
    if (error) alert(`登录失败: ${error.message}`);
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-slate-50">
      <div className="p-8 bg-white rounded-xl shadow max-w-sm w-full text-center">
        <h1 className="text-2xl font-semibold mb-2">AI App Starter</h1>
        <p className="text-slate-500 text-sm mb-6">
          通过网关统一身份登录
        </p>
        <button
          onClick={handleLogin}
          className="w-full py-3 rounded-lg bg-slate-900 text-white hover:bg-slate-700 transition"
        >
          使用网关账号登录
        </button>
        <p className="mt-4 text-xs text-slate-400">
          新用户会自动在网关 + Supabase Auth 两侧创建账号
        </p>
      </div>
    </div>
  );
}
