// lib/supabase/admin.ts — service_role client，仅 trusted server 代码使用。
import 'server-only';
import { createClient, type SupabaseClient } from '@supabase/supabase-js';
import { config } from '@/lib/config';

let instance: SupabaseClient | null = null;

export function createAdminSupabase() {
  if (instance) return instance;
  const serviceRoleKey = process.env.SUPABASE_SECRET_KEY ?? '';
  if (!serviceRoleKey) {
    throw new Error('SUPABASE_SECRET_KEY is required for admin client');
  }
  instance = createClient(config.supabaseUrl, serviceRoleKey, {
    auth: { autoRefreshToken: false, persistSession: false },
  });
  return instance;
}
