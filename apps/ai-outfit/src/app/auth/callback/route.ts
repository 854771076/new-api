// app/auth/callback/route.ts — 服务端处理 OAuth 回调，授权码只兑换一次。
import { cookies } from 'next/headers';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';

export async function GET(request: NextRequest) {
  const supabase = await createServerSupabase();

  // 已经登录时直接进入工作室，避免重复访问旧回调地址导致的一次性授权码兑换失败。
  const { data: { user } } = await supabase.auth.getUser();
  if (user) {
    return NextResponse.redirect(new URL('/studio', request.url));
  }

  const { searchParams } = request.nextUrl;

  if (searchParams.get('error')) {
    return NextResponse.redirect(new URL('/login?error=oauth_denied', request.url));
  }

  const code = searchParams.get('code');
  if (!code) {
    return NextResponse.redirect(new URL('/login?error=missing_code', request.url));
  }

  const { error } = await supabase.auth.exchangeCodeForSession(code);
  if (error) {
    if (
      error.code === 'pkce_code_verifier_not_found' ||
      /flow state|code verifier|pkce/i.test(error.message ?? '')
    ) {
      // 失效的 PKCE 授权码没有可用 session，清掉残留的 auth cookie 再让用户重新登录。
      const store = await cookies();
      for (const c of store.getAll()) {
        if (/^sb-.*auth-token/.test(c.name)) store.delete(c.name);
      }
      return NextResponse.redirect(new URL('/login?error=session_expired', request.url));
    }
    return NextResponse.redirect(new URL('/login?error=oauth_failed', request.url));
  }

  return NextResponse.redirect(new URL('/studio', request.url));
}
