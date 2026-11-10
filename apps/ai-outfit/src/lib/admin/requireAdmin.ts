// lib/admin/requireAdmin.ts — 管理后台服务端守卫。
// 所有 admin Route Handler 和 layout 在 SSR 阶段就调用它，非 owner/admin 自动返回 403。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';

export async function requireAdmin(req?: NextRequest): Promise<{
  ok: true;
  userId: string;
  role: 'owner' | 'admin';
} | { ok: false; response: NextResponse }> {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) {
    const url = req ? new URL('/login', req.url).toString() : '/login';
    return { ok: false, response: NextResponse.redirect(url) };
  }
  const { data: row } = await supabase
    .from('app_admins')
    .select('role')
    .eq('user_id', user.id)
    .maybeSingle();
  if (!row) {
    return { ok: false, response: NextResponse.json({ error: 'FORBIDDEN', message: '非管理员访问被拒绝' }, { status: 403 }) };
  }
  return { ok: true, userId: user.id, role: row.role as 'owner' | 'admin' };
}
