// src/lib/supabase.ts
// Supabase client 封装——自动创建 + 会话持久化 + 全局单例。
import { createClient, type SupabaseClient } from '@supabase/supabase-js';
import { config } from './config';

let _instance: SupabaseClient | null = null;

export function getSupabase(): SupabaseClient {
  if (!_instance) {
    _instance = createClient(config.supabaseUrl, config.supabaseAnonKey, {
      auth: {
        persistSession: true,
        autoRefreshToken: true,
        flowType: 'pkce',
        detectSessionInUrl: false,
      },
    });
  }
  return _instance;
}
