'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { getSupabase } from '@/lib/supabase/client';

export function LogoutButton({ label = '退出登录' }: { label?: string }) {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  async function handle() {
    setLoading(true);
    await getSupabase().auth.signOut();
    router.push('/login');
  }
  return (
    <button className="btn btn-ghost btn-sm" disabled={loading} onClick={handle}>
      {loading ? '登出中…' : label}
    </button>
  );
}
