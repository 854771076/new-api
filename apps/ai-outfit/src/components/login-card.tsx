'use client';

import { useState } from 'react';
import { ArrowRight, ShieldCheck, Shirt } from 'lucide-react';
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
    ? (Object.hasOwn(ERROR_MESSAGES, errorCode) ? ERROR_MESSAGES[errorCode] : '登录未完成，请重试。')
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
    <section className="card card-border w-full bg-base-100 text-base-content shadow-lg" aria-busy={loading}>
      <div className="card-body items-center gap-6 p-6 sm:p-10">
        <div className="badge badge-outline gap-2 py-4 text-xs">
          <Shirt size={16} aria-hidden="true" /> AI 换装工作室
        </div>
        <div className="space-y-3 text-center">
          <h2 className="text-3xl font-semibold tracking-tight">欢迎使用 AI 换装</h2>
          <p className="text-base leading-relaxed text-base-content/70">让灵感成为下一套造型。<br />选择人物、服装与姿势，开启你的创作。</p>
        </div>
        <div className="divider my-0" />
        <div className="flex w-full items-start gap-3">
          <ShieldCheck size={24} className="mt-0.5 shrink-0 text-primary" aria-hidden="true" />
          <div className="space-y-1">
            <p className="font-medium">使用 new-api 账户安全登录</p>
            <p className="text-sm leading-relaxed text-base-content/70">前往 new-api 完成授权，本应用不收集你的账户密码。</p>
          </div>
        </div>
        {errorMessage ? (
          <div role="alert" className="alert alert-error alert-soft w-full text-sm">
            <span>{errorMessage}</span>
          </div>
        ) : null}
        <button
          type="button"
          className="btn btn-primary btn-block min-h-12 gap-3"
          disabled={loading}
          onClick={handleLogin}
        >
          {loading ? <span className="loading loading-spinner loading-sm" aria-hidden="true" /> : null}
          {errorCode ? '重新登录' : '使用 new-api 登录'}
          {!loading ? <ArrowRight size={18} aria-hidden="true" /> : null}
        </button>
        {err ? (
          <div role="alert" className="alert alert-error alert-soft w-full text-sm">
            <span>登录暂时无法完成，请稍后重试。</span>
          </div>
        ) : null}
        <p className="text-center text-xs text-base-content/70">由 new-api 提供账户授权</p>
      </div>
    </section>
  );
}
