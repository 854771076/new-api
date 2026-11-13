// lib/media-urls.ts — SSR 端批量生成 Supabase Storage signed URL。
// Signed URLs must enforce the caller's Storage RLS, including legacy object ownership.
import 'server-only';
import { createServerSupabase } from '@/lib/supabase/server';

/**
 * 批量生成 signed URL。
 * @param bucket Storage bucket 名称（如 'media-official'）
 * @param paths 对象路径数组，如 ['poses/xxx.webp', 'outfits/yyy.webp']
 * @returns Map<path, signedUrl>；path 为空或生成失败时跳过
 */
export async function getSignedUrls(
  bucket: string,
  paths: (string | null | undefined)[],
  expiresIn = 3600,
): Promise<Record<string, string>> {
  const valid = paths.filter((p): p is string => Boolean(p && p.trim()));
  if (valid.length === 0) return {};

  const supabase = await createServerSupabase();
  const { data, error } = await supabase.storage.from(bucket).createSignedUrls(valid, expiresIn);

  if (error || !data) {
    console.warn('[media-urls] createSignedUrls failed');
    return {};
  }

  const result: Record<string, string> = {};
  for (const item of data) {
    if (item.signedUrl && item.path) result[item.path] = item.signedUrl;
  }
  return result;
}

/**
 * 单条 signed URL 便捷函数。
 */
export async function getSignedUrl(
  bucket: string,
  path: string | null | undefined,
  expiresIn = 3600,
): Promise<string | null> {
  if (!path) return null;
  const map = await getSignedUrls(bucket, [path], expiresIn);
  return map[path] ?? null;
}
