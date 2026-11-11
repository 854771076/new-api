// app/auth/callback/route.ts — 服务端处理 OAuth 回调，授权码只兑换一次。
import { cookies } from 'next/headers';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import type { User } from '@supabase/supabase-js';
import { persistGatewaySession, discardGatewayCallbackSession } from '@/lib/gateway-session';

export const runtime = 'nodejs';

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

  const { data, error } = await supabase.auth.exchangeCodeForSession(code);
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

  let verifiedUser: User | null = null;
  try {
    if (!data.session) throw new Error('Missing session');
    const verified = await supabase.auth.getUser(data.session.access_token);
    if (verified.error || !verified.data.user) throw new Error('Invalid session');
    verifiedUser = verified.data.user;
    // Replace the cookie session with Supabase credentials only. The provider pair
    // belongs solely in the encrypted server vault, never browser session storage.
    const sanitized = await supabase.auth.setSession({
      access_token: data.session.access_token,
      refresh_token: data.session.refresh_token,
    });
    if (sanitized.error || !sanitized.data.session || sanitized.data.session.provider_token || sanitized.data.session.provider_refresh_token) {
      throw new Error('Cookie session could not be sanitized');
    }
    await persistGatewaySession(data.session, verifiedUser);
  } catch {
    if (data.session && verifiedUser) {
      await discardGatewayCallbackSession(data.session, verifiedUser).catch(() => undefined);
    }
    await supabase.auth.signOut({ scope: 'local' }).catch(() => undefined);
    const store = await cookies();
    for (const cookie of store.getAll()) {
      if (/^sb-.*auth-token/.test(cookie.name)) store.delete(cookie.name);
    }
    return NextResponse.redirect(new URL('/login?error=oauth_failed', request.url));
  }

  return NextResponse.redirect(new URL('/studio', request.url));
}
