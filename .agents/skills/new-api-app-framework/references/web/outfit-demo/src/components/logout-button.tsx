'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';

export function LogoutButton({ label = '退出登录' }: { label?: string }) {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function handle() {
    if (loading) return;
    setLoading(true);
    setError(null);
    try {
      const response = await fetch('/api/auth/logout', { method: 'POST', signal: AbortSignal.timeout(30_000) });
      if (response.status === 401) {
        router.replace('/login');
        router.refresh();
        return;
      }
      const result: unknown = await response.json().catch(() => null);
      if (!response.ok || !result || typeof result !== 'object' || !('success' in result) || result.success !== true) {
        const locallySignedOut = result && typeof result === 'object' && 'error_code' in result && result.error_code === 'UPSTREAM_REVOCATION_FAILED';
        setError(locallySignedOut ? '已退出本地登录，但网关撤销尚未确认。' : '退出未能完成，请重试。');
        return;
      }
      router.replace('/login');
      router.refresh();
    } catch { setError('退出未能完成，请重试。'); }
    finally { setLoading(false); }
  }
  return (
    <div>
      <button type="button" className="btn btn-ghost btn-sm" disabled={loading} aria-busy={loading} onClick={handle}>
        {loading ? '登出中…' : label}
      </button>
      {error ? <div role="alert" className="alert alert-error alert-soft text-sm">{error}</div> : null}
    </div>
  );
}
