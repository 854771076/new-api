// lib/storage.ts — service_role 直连 Storage 的字节读取，仅供受信任的服务端管线使用。
import 'server-only';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { GatewayError } from '@/lib/gateway-errors';

/** 用 service_role 下载私有 Storage 对象；绕过 RLS，因此只能在 BFF/管线内部按已授权资源调用。 */
export async function downloadStorageObject(bucket: string, path: string): Promise<Buffer> {
  const admin = createAdminSupabase();
  const { data, error } = await admin.storage.from(bucket).download(path);
  if (error || !data) throw new GatewayError(503, '素材读取失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
  return Buffer.from(await data.arrayBuffer());
}
