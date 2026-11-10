'use client';

import { useState } from 'react';
import { getSupabase } from '@/lib/supabase/client';

const ERROR_MESSAGES: Record<string, string> = {
  session_expired: '登录状态已失效，请重新登录。',
  oauth_denied: '授权被取消或未完成，请重新登录。',
  oauth_failed: '登录失败，请重试。',
  missing_code: '登录回调缺少授权码，请重新登录。',
};

export function LoginCard({ errorCode }: { errorCode?: string }) {
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const errorMessage = errorCode
    ? (ERROR_MESSAGES[errorCode] ?? '登录未完成，请重试。')
    : null;

  async function handleLogin() {
    setLoading(true);
    setErr(null);
    try {
      const { error } = await getSupabase().auth.signInWithOAuth({
        provider: 'custom:newapi',
        options: { redirectTo: window.location.origin + '/auth/callback' },
      });
      if (error) setErr(error.message);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="card bg-base-100 shadow-xl">
      <div className="card-body p-8">
        <h2 className="mb-6 text-center text-2xl font-bold">欢迎使用 AI 换装</h2>
        {errorMessage ? (
          <div role="alert" className="alert alert-error alert-soft mt-4 text-sm">
            <span>{errorMessage}</span>
          </div>
        ) : null}
        <button
          type="button"
          className="btn btn-primary btn-block mt-4"
          disabled={loading}
          onClick={handleLogin}
        >
          {loading ? <span className="loading loading-spinner" /> : null}
          {errorCode ? '重新登录' : '使用 new-api 登录'}
        </button>
        {err ? (
          <details className="mt-4 text-sm text-error">
            <summary className="cursor-pointer">登录失败详情</summary>
            <pre className="mt-2 whitespace-pre-wrap break-words font-sans">{err}</pre>
          </details>
        ) : null}
      </div>
    </div>
  );
}
