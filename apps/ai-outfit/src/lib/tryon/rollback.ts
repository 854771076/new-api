// lib/tryon/rollback.ts — 整单失败时释放本次新解析服装，并把应用层固定费用记账为已退还。
// 网关 relay 的实际用量计费不在本应用内退还（由网关侧按失败任务处理）；这里只回滚
// 本应用在解析阶段落库的商品图/状态，使服装恢复「未解析、可再次免费解析」的资格。
import 'server-only';
import type { SupabaseClient } from '@supabase/supabase-js';
import type { ResolvedWear } from './wear';

/**
 * 回滚本次新解析（newlyParsed）的个人服装：删除商品图对象并把行恢复到待解析状态，
 * 保留原图（thumb/preview/original 不动）。官方服装与复用的已解析商品图不受影响。
 * 每一步都容错，绝不因清理失败抛出而掩盖原始生成错误。
 */
export async function rollbackNewlyParsed(
  admin: SupabaseClient,
  wear: ResolvedWear[],
  ctx: { clientId: string; userId: string },
): Promise<void> {
  const fresh = wear.filter((w) => w.newlyParsed && w.source === 'user');
  for (const item of fresh) {
    try {
      await admin.storage.from(item.bucket).remove([item.path]);
      await admin.from('outfits').update({
        parse_status: 'pending',
        parse_error: null,
        product_bucket: null,
        product_path: null,
        garment_type: null,
        bbox: null,
        semantic_hints: {},
      }).eq('id', item.outfitId).eq('client_id', ctx.clientId).eq('user_id', ctx.userId);
    } catch {
      // 清理失败不应掩盖原始生成错误；保留现场供人工/下次重试处理。
    }
  }
}

/**
 * 回滚一批服装 id（本次任务报价时未解析的个人服装）：删除商品图对象并把行恢复到待解析状态。
 * 用于异步推进最终换装失败时释放本次解析的服装；官方服装与已就绪商品图不受影响（id 列表已排除）。
 * 每一步都容错，绝不因清理失败抛出而掩盖原始生成错误。
 */
export async function rollbackParsedGarments(
  admin: SupabaseClient,
  garmentIds: string[],
  ctx: { clientId: string; userId: string },
): Promise<void> {
  for (const id of garmentIds) {
    try {
      const { data } = await admin.from('outfits')
        .select('product_bucket, product_path')
        .eq('id', id).eq('client_id', ctx.clientId).eq('user_id', ctx.userId)
        .maybeSingle();
      const bucket = data?.product_bucket;
      const path = data?.product_path;
      // 仅回滚本次真正解析成功（已有商品图）的服装；失败或未解析的保留现场（含 parse_error）。
      if (bucket && path) {
        await admin.storage.from(bucket).remove([path]);
        await admin.from('outfits').update({
          parse_status: 'pending',
          parse_error: null,
          product_bucket: null,
          product_path: null,
          garment_type: null,
          bbox: null,
          semantic_hints: {},
        }).eq('id', id).eq('client_id', ctx.clientId).eq('user_id', ctx.userId);
      }
    } catch {
      // 清理失败不应掩盖原始生成错误；保留现场供人工/下次重试处理。
    }
  }
}
