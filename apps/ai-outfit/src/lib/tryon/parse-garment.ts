// lib/tryon/parse-garment.ts — 个人服装按需解析：识别 → 裁剪 → 单件白底商品图（v1）。
// 仅处理 source='user' 且 parse_status!='ready' 的服装；官方服装直接用制作图，不进入本模块。
import 'server-only';
import type { SupabaseClient } from '@supabase/supabase-js';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { getSignedUrl } from '@/lib/media-urls';
import { GatewayError } from '@/lib/gateway-errors';
import { generateReferenceImage } from '@/lib/gateway';
import { readGeneratedImage } from '@/lib/generated-image';
import { recognizeGarment, type RecognitionResult } from './recognize';
import { cropGarmentReference } from './image';
import { PRODUCT_IMAGE_V1_PROMPT } from './prompts';
import { resolveTryonModel } from './models';
import { downloadStorageObject } from '@/lib/storage';
import type { GarmentBBox, GarmentSemanticHints, Outfit } from '@/lib/types';

export interface ParseableGarment {
  id: string;
  category: string;
  name_zh: string;
  bucket: string;
  original_bucket: string | null;
  original_path: string | null;
  thumb_path: string;
  parse_status: Outfit['parse_status'];
  parse_attempts: number;
}

export interface ParsedGarment {
  id: string;
  productBucket: string;
  productPath: string;
  garmentType: string;
  bbox: GarmentBBox;
  semanticHints: GarmentSemanticHints;
}

interface ParseContext {
  clientId: string;
  userId: string;
  visionModel?: string;
  imageModel?: string;
}

/**
 * 解析单件个人服装并持久化商品图与语义画像。
 * 成功才把 parse_status 置 ready 并落 product_*；失败保留原图与旧商品图（若有），
 * 置 failed 并累计 parse_attempts。
 */
export async function parsePersonalGarment(
  supabase: SupabaseClient,
  garment: ParseableGarment,
  ctx: ParseContext,
): Promise<ParsedGarment> {
  const admin = createAdminSupabase();
  const originalBucket = garment.original_bucket ?? garment.bucket;
  const originalPath = garment.original_path ?? garment.thumb_path;

  const [visionModel, imageModel] = await Promise.all([
    ctx.visionModel ? Promise.resolve(ctx.visionModel) : resolveTryonModel(supabase, 'vision').then((r) => r.model),
    ctx.imageModel ? Promise.resolve(ctx.imageModel) : resolveTryonModel(supabase, 'image').then((r) => r.model),
  ]);

  const originalUrl = await getSignedUrl(originalBucket, originalPath);
  if (!originalUrl) {
    await failParse(admin, garment, ctx, '服装原图无法读取，请重新上传');
    throw new GatewayError(400, '服装原图无法读取，请重新上传', 'AI_TRYON_GARMENT_SOURCE_MISSING');
  }

  await admin.from('outfits').update({ parse_status: 'parsing', parse_error: null })
    .eq('id', garment.id).eq('client_id', ctx.clientId).eq('user_id', ctx.userId);

  // 1. 视觉识别（原图尺寸用于 bbox 边界校验）。recognizeGarment 内部已做 zod + 坐标校验。
  let recognition: RecognitionResult;
  try {
    const dims = await fetchImageDimensions(originalBucket, originalPath);
    recognition = await recognizeGarment({
      model: visionModel,
      imageUrl: originalUrl,
      expectedCategory: garment.category,
      width: dims.width,
      height: dims.height,
    });
  } catch (error) {
    await failParse(admin, garment, ctx, error instanceof GatewayError ? error.message : '服装识别失败');
    throw error instanceof GatewayError
      ? error
      : new GatewayError(502, '服装识别失败，请更换图片后重试', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }

  // 2. 按识别框 16% 边距裁剪。
  const originalBytes = await downloadStorageObject(originalBucket, originalPath);
  let cropBytes: Buffer;
  try {
    cropBytes = await cropGarmentReference(originalBytes, recognition.bbox);
  } catch (error) {
    await failParse(admin, garment, ctx, '服装裁剪失败');
    throw error instanceof GatewayError
      ? error
      : new GatewayError(502, '服装裁剪失败，请更换图片后重试', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }
  const cropUrl = await uploadTemporary(admin, ctx, garment.id, cropBytes);

  // 3. 裁剪图 + 权威原图生成单件白底商品图（v1）。
  let productBytes: Buffer;
  try {
    const prompt = PRODUCT_IMAGE_V1_PROMPT
      .replace('{garment_type}', recognition.garment_type)
      .replace('{hints}', JSON.stringify(recognition.semantic_hints));
    const result = await generateReferenceImage({
      model: imageModel,
      prompt,
      reference_images: [cropUrl, originalUrl],
      size: '1024x1024',
    });
    const image = result.images[0];
    if (!image) throw new Error('empty product image');
    productBytes = await readGeneratedImage(image);
  } catch (error) {
    await failParse(admin, garment, ctx, '商品图生成失败');
    throw error instanceof GatewayError
      ? error
      : new GatewayError(502, '商品图生成失败，请稍后重试', 'AI_TRYON_GARMENT_PRODUCT_FAILED');
  } finally {
    await admin.storage.from('media-user').remove([temporaryPath(ctx, garment.id)]).catch(() => undefined);
  }

  // 4. 商品图落盘并提交解析结果（整单提交语义由生成编排层在成功后放行复用）。
  const productBucket = 'media-user';
  const productPath = `${ctx.clientId}/${ctx.userId}/outfits/products/${garment.id}.png`;
  const { error: uploadError } = await admin.storage.from(productBucket).upload(productPath, productBytes, {
    contentType: 'image/png',
    upsert: false,
  });
  if (uploadError) {
    await failParse(admin, garment, ctx, '商品图保存失败');
    throw new GatewayError(503, '商品图保存失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
  }

  const { error: updateError } = await admin.from('outfits').update({
    parse_status: 'ready',
    parse_error: null,
    product_bucket: productBucket,
    product_path: productPath,
    garment_type: recognition.garment_type,
    bbox: recognition.bbox,
    semantic_hints: recognition.semantic_hints,
  }).eq('id', garment.id).eq('client_id', ctx.clientId).eq('user_id', ctx.userId).select('id').maybeSingle();

  if (updateError) {
    await admin.storage.from(productBucket).remove([productPath]);
    await failParse(admin, garment, ctx, '服装解析结果保存失败');
    throw new GatewayError(503, '服装解析结果保存失败，请稍后重试', 'UPSTREAM_DATABASE_ERROR');
  }

  return {
    id: garment.id,
    productBucket,
    productPath,
    garmentType: recognition.garment_type,
    bbox: recognition.bbox,
    semanticHints: recognition.semantic_hints,
  };
}

async function failParse(
  admin: SupabaseClient,
  garment: ParseableGarment,
  ctx: ParseContext,
  message: string,
) {
  await admin.from('outfits').update({
    parse_status: 'failed',
    parse_error: message,
    parse_attempts: (garment.parse_attempts ?? 0) + 1,
  }).eq('id', garment.id).eq('client_id', ctx.clientId).eq('user_id', ctx.userId);
}

function temporaryPath(ctx: ParseContext, garmentId: string): string {
  return `${ctx.clientId}/${ctx.userId}/outfits/tmp/${garmentId}-crop.jpg`;
}

async function uploadTemporary(admin: SupabaseClient, ctx: ParseContext, garmentId: string, bytes: Buffer): Promise<string> {
  const path = temporaryPath(ctx, garmentId);
  const { error } = await admin.storage.from('media-user').upload(path, bytes, {
    contentType: 'image/jpeg',
    upsert: true,
  });
  if (error) throw new GatewayError(503, '裁剪图暂存失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
  const url = await getSignedUrl('media-user', path);
  if (!url) throw new GatewayError(503, '裁剪图签名失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
  return url;
}

async function fetchImageDimensions(bucket: string, path: string): Promise<{ width: number; height: number }> {
  const bytes = await downloadStorageObject(bucket, path);
  const sharp = (await import('sharp')).default;
  const meta = await sharp(bytes, { limitInputPixels: 40_000_000, animated: false }).metadata();
  if (!meta.width || !meta.height) {
    throw new GatewayError(400, '服装原图尺寸无效，无法识别', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }
  return { width: meta.width, height: meta.height };
}
