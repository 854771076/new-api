// lib/tryon/wear.ts — 把官方/个人服装统一为可上服装板的制作图，并构造同序清单输入。
// 官方服装直接用 preview/thumb 制作图；已解析个人服装复用 product_*；未解析的现场解析。
import 'server-only';
import type { SupabaseClient } from '@supabase/supabase-js';
import { getSignedUrl } from '@/lib/media-urls';
import { GatewayError } from '@/lib/gateway-errors';
import { parsePersonalGarment, type ParseableGarment } from './parse-garment';
import type { GarmentSource, GarmentSemanticHints } from '@/lib/types';

export interface WearCandidate {
  id: string;
  source: GarmentSource;
  user_id: string | null;
  category: string;
  name_zh: string;
  bucket: string;
  thumb_path: string;
  preview_path: string | null;
  parse_status: string;
  product_bucket: string | null;
  product_path: string | null;
  garment_type: string | null;
  semantic_hints: GarmentSemanticHints | null;
  original_bucket: string | null;
  original_path: string | null;
  parse_attempts: number;
}

export interface ResolvedWear {
  outfitId: string;
  source: GarmentSource;
  category: string;
  name: string;
  garmentType: string | null;
  semanticHints: GarmentSemanticHints;
  /** 送入服装板的制作图 HTTPS signed URL。 */
  imageUrl: string;
  /** 落库 wear_snapshot 的定位（bucket/path）。 */
  bucket: string;
  path: string;
  /** 本次是否新解析（用于整单成功后才放行复用、失败回滚的判定）。 */
  newlyParsed: boolean;
}

export interface ResolveWearContext {
  clientId: string;
  userId: string;
  visionModel?: string;
  imageModel?: string;
}

/**
 * 逐件解析本次选中的服装。官方服装与已 ready 的个人服装只签名、不调用模型；
 * 未 ready 的个人服装按需 parsePersonalGarment。任一件失败即整体失败（不产生半成品任务）。
 */
export async function resolveWearItems(
  supabase: SupabaseClient,
  candidates: WearCandidate[],
  ctx: ResolveWearContext,
): Promise<ResolvedWear[]> {
  const resolved: ResolvedWear[] = [];
  for (const garment of candidates) {
    if (garment.source === 'official') {
      const path = garment.preview_path?.trim() || garment.thumb_path;
      const url = await getSignedUrl(garment.bucket, path);
      if (!url) throw new GatewayError(400, '官方服装缺少制作图，请改选其它服装', 'AI_TRYON_GARMENT_SOURCE_MISSING');
      resolved.push({
        outfitId: garment.id,
        source: 'official',
        category: garment.category,
        name: garment.name_zh,
        garmentType: garment.garment_type,
        semanticHints: garment.semantic_hints ?? {},
        imageUrl: url,
        bucket: garment.bucket,
        path,
        newlyParsed: false,
      });
      continue;
    }

    if (garment.parse_status === 'ready' && garment.product_bucket && garment.product_path) {
      const url = await getSignedUrl(garment.product_bucket, garment.product_path);
      if (!url) throw new GatewayError(400, '个人服装商品图无法读取，请重新解析', 'AI_TRYON_GARMENT_SOURCE_MISSING');
      resolved.push({
        outfitId: garment.id,
        source: 'user',
        category: garment.category,
        name: garment.name_zh,
        garmentType: garment.garment_type,
        semanticHints: garment.semantic_hints ?? {},
        imageUrl: url,
        bucket: garment.product_bucket,
        path: garment.product_path,
        newlyParsed: false,
      });
      continue;
    }

    const parseable: ParseableGarment = {
      id: garment.id,
      category: garment.category,
      name_zh: garment.name_zh,
      bucket: garment.bucket,
      original_bucket: garment.original_bucket,
      original_path: garment.original_path,
      thumb_path: garment.thumb_path,
      parse_status: garment.parse_status as ParseableGarment['parse_status'],
      parse_attempts: garment.parse_attempts ?? 0,
    };
    const parsed = await parsePersonalGarment(supabase, parseable, ctx);
    const url = await getSignedUrl(parsed.productBucket, parsed.productPath);
    if (!url) throw new GatewayError(503, '商品图签名失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
    resolved.push({
      outfitId: garment.id,
      source: 'user',
      category: garment.category,
      name: garment.name_zh,
      garmentType: parsed.garmentType,
      semanticHints: parsed.semanticHints,
      imageUrl: url,
      bucket: parsed.productBucket,
      path: parsed.productPath,
      newlyParsed: true,
    });
  }
  return resolved;
}

/** 由已解析服装构造与服装板同序的清单输入（排序在 buildGarmentManifest 内按 id 稳定完成）。 */
export function toManifestInputs(wear: ResolvedWear[]): {
  id: string;
  source: GarmentSource;
  category: string;
  name: string;
  garment_type: string | null;
  semantic_hints: GarmentSemanticHints;
}[] {
  return wear.map((w) => ({
    id: w.outfitId,
    source: w.source,
    category: w.category,
    name: w.name,
    garment_type: w.garmentType,
    semantic_hints: w.semanticHints,
  }));
}
