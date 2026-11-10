import { createServerClient } from '@supabase/ssr';
import { NextResponse, type NextRequest } from 'next/server';

// 刷新 Supabase 会话 Cookie（Next.js 16 起 middleware 文件约定改名为 proxy）。
// Server Component 只能读 Cookie，无法在渲染时写回轮换后的令牌，所以统一由
// proxy 调 getUser() 触发刷新，再把新 Cookie 写进响应。
export async function proxy(request: NextRequest) {
  let response = NextResponse.next({ request });

  const supabase = createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL ?? '',
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? '',
    {
      cookies: {
        getAll() {
          return request.cookies.getAll();
        },
        setAll(cookiesToSet) {
          for (const { name, value } of cookiesToSet) {
            request.cookies.set(name, value);
          }
          response = NextResponse.next({ request });
          for (const { name, value, options } of cookiesToSet) {
            response.cookies.set(name, value, options);
          }
        },
      },
    },
  );

  // 必须调用 getUser()：它在令牌过期时刷新，并通过 setAll 写回响应。
  await supabase.auth.getUser();

  return response;
}

export const config = {
  // 静态资源和图片不需要会话刷新。
  matcher: ['/((?!_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp|ico)$).*)'],
};
