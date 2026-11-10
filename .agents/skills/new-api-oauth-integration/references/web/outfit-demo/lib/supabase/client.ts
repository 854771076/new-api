// lib/supabase/client.ts
// 浏览器端 Supabase client——用 @supabase/ssr 的 createBrowserClient，
// 会话写到 Cookie（而不是 localStorage），这样 Server Component / middleware
// 能读到同一个登录态；自动刷新、全局单例仍然由 createBrowserClient 保证。
// 只能被 Client Component 引用（Node 环境没有 document.cookie）。
import { createBrowserClient } from '@supabase/ssr';
import type { SupabaseClient } from '@supabase/supabase-js';
import { config } from '@/lib/config';

let instance: SupabaseClient | null = null;

export function getSupabase(): SupabaseClient {
  if (!instance) {
    instance = createBrowserClient(config.supabaseUrl, config.supabaseAnonKey, {
      auth: {
        persistSession: true,
        autoRefreshToken: true,
        // OAuth 回调用授权码流程（?code=），与回调页的 exchangeCodeForSession
        // 保持一致；隐式流程不会触发回调页逻辑。
        flowType: 'pkce',
        // 关闭自动兑换：/auth/callback 页负责 code 兑换、网关令牌注入和
        // user-sync。开启后会和回调页重复兑换同一个 code。
        detectSessionInUrl: false,
      },
    });
  }
  return instance;
}
