// lib/supabase/server.ts — 服务端 Supabase client，从请求 Cookie 取会话。
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
          // Server Component 写 Cookie 会抛错；会话刷新由 proxy 完成。
        }
      },
    },
  });
}
