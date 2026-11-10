// lib/supabase/server.ts
// 服务端 Supabase client——从请求 Cookie 里取浏览器写入的会话，RLS 依然按
// auth.uid() 生效。Server Component 不能写 Cookie，令牌轮换交给 middleware。
import { cookies } from 'next/headers';
import { createServerClient } from '@supabase/ssr';
import { config } from '@/lib/config';

export async function createServerSupabase() {
  const cookieStore = await cookies();

  return createServerClient(config.supabaseUrl, config.supabaseAnonKey, {
    cookies: {
      getAll() {
        return cookieStore.getAll();
      },
      setAll(cookiesToSet) {
        try {
          for (const { name, value, options } of cookiesToSet) {
            cookieStore.set(name, value, options);
          }
        } catch {
          // 在 Server Component 中写 Cookie 会抛错；会话刷新由 middleware 完成，
          // 这里忽略即可，不能影响页面渲染。
        }
      },
    },
  });
}
