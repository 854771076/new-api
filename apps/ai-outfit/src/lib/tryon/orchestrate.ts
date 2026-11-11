// lib/tryon/orchestrate.ts — 六阶段换装管线编排：人物标准化 → 按需解析 → 服装板/清单 →
// 固定两图 v4 换装 → 落盘作品，并冻结全部快照。整单失败回滚本次新解析服装。
import 'server-only';
import type { SupabaseClient } from '@supabase/supabase-js';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { getSignedUrl } from '@/lib/media-urls';
import { downloadStorageObject } from '@/lib/storage';
import { generateReferenceImage, GatewayError } from '@/lib/gateway';
import { readGeneratedImage } from '@/lib/generated-image';
import { ensurePersona, type PersonaInput } from './persona';
import { resolveWearItems, toManifestInputs, type ResolveWearContext, type ResolvedWear, type WearCandidate } from './wear';
import { buildGarmentBoard } from './image';
import { buildGarmentManifest } from './manifest';
import { rollbackNewlyParsed } from './rollback';
import { TRYON_V4_PROMPT } from './prompts';
import type {
  GenerationQuote, GarmentManifestEntry, PersonaSnapshot, ProviderSnapshot, WearSnapshotEntry,
} from '@/lib/types';

export interface TryonOrder {
  recordId: string;
  clientId: string;
  userId: string;
  character: PersonaInput['character'];
  candidates: WearCandidate[];
  /** 已在报价阶段确定并冻结的报价；编排时只复核，不重新计价。 */
  quote: GenerationQuote;
  imageModel: string;
  visionModel: string;
  size: string;
}

export interface TryonResult {
  resultBucket: string;
  resultPath: string;
  boardBucket: string;
  boardPath: string;
  manifest: GarmentManifestEntry[];
  personaSnapshot: PersonaSnapshot;
  wearSnapshot: WearSnapshotEntry[];
  providerSnapshot: ProviderSnapshot;
  gatewayRequestId?: string;
  gatewayUsage?: unknown;
  wear: ResolvedWear[];
}

/**
 * 执行一次完整换装。调用方负责先落 pending 任务；本函数更新 stage 并在成功后回写作品，
 * 失败时把任务交给调用方标记 failed，并在此处完成新解析服装的回滚。
 */
export async function runTryon(supabase: SupabaseClient, order: TryonOrder): Promise<TryonResult> {
  const admin = createAdminSupabase();
  const ctx: ResolveWearContext = {
    clientId: order.clientId,
    userId: order.userId,
    visionModel: order.visionModel,
    imageModel: order.imageModel,
  };

  // 1. 人物标准化（ready 则复用 cover，不重复生成/扣费）。
  await updateStage(admin, order.recordId, order, 'parsing');
  const persona = await ensurePersona(supabase, {
    character: order.character,
    clientId: order.clientId,
    userId: order.userId,
    imageModel: order.imageModel,
  });
  const personaSnapshot: PersonaSnapshot = {
    character_id: order.character.id,
    persona_version: persona.personaVersion,
    bucket: persona.bucket,
    path: persona.path,
  };

  // 2. 按需解析个人服装，统一得到可上板制作图。
  const wear = await resolveWearItems(supabase, order.candidates, ctx);

  // 解析之后的任何失败（拼板、上传、签名、换装）都按整单失败处理：回滚本次新解析服装，
  // 保证商品图只在整单成功后才成为可复用资产（人物标准化与已 ready 资产不在回滚范围）。
  try {
    return await runBoardAndTryon(admin, order, persona, personaSnapshot, wear);
  } catch (error) {
    await rollbackNewlyParsed(admin, wear, { clientId: order.clientId, userId: order.userId });
    throw error;
  }
}

async function runBoardAndTryon(
  admin: SupabaseClient,
  order: TryonOrder,
  persona: Awaited<ReturnType<typeof ensurePersona>>,
  personaSnapshot: PersonaSnapshot,
  wear: ResolvedWear[],
): Promise<TryonResult> {
  // 3. 下载制作图字节并拼 3×2 白底服装板（板与清单共用同一稳定排序）。
  await updateStage(admin, order.recordId, order, 'board');
  const boardItems = await Promise.all(wear.map(async (w) => ({
    id: w.outfitId,
    image: await downloadStorageObject(w.bucket, w.path),
  })));
  const board = await buildGarmentBoard(boardItems);
  if (board.orderedIds.length !== wear.length) {
    throw new GatewayError(502, '服装板与所选服装不一致，请重试', 'AI_TRYON_GARMENT_BOARD_FAILED');
  }

  const manifest = buildGarmentManifest(toManifestInputs(wear));
  const boardBucket = 'media-user';
  const boardPath = `${order.clientId}/${order.userId}/generations/${order.recordId}/board.jpg`;
  const { error: boardUploadError } = await admin.storage.from(boardBucket).upload(boardPath, board.image, {
    contentType: 'image/jpeg',
    upsert: false,
  });
  if (boardUploadError) throw new GatewayError(503, '服装板保存失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');

  const wearSnapshot: WearSnapshotEntry[] = wear.map((w) => ({
    outfit_id: w.outfitId,
    source: w.source,
    category: w.category,
    bucket: w.bucket,
    path: w.path,
  }));
  const providerSnapshot: ProviderSnapshot = { model: order.imageModel, size: order.size };

  // 4. 固定两图 [人物参考, 服装板] + v4 冻结提示词换装。
  await updateStage(admin, order.recordId, order, 'generating');
  const [personaUrl, boardUrl] = await Promise.all([
    getSignedUrl(persona.bucket, persona.path),
    getSignedUrl(boardBucket, boardPath),
  ]);
  if (!personaUrl || !boardUrl) {
    throw new GatewayError(503, '参考图签名失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
  }
  const prompt = TRYON_V4_PROMPT
    .replaceAll('{item_count}', String(wear.length))
    .replace('{garment_manifest}', manifest.json);

  let resultBytes: Buffer;
  let requestId: string | undefined;
  let usage: unknown;
  try {
    const result = await generateReferenceImage({
      model: order.imageModel,
      prompt,
      reference_images: [personaUrl, boardUrl],
      size: order.size,
    });
    const image = result.images[0];
    if (!image) throw new Error('empty try-on image');
    resultBytes = await readGeneratedImage(image);
    requestId = result.request_id;
    usage = result.usage ?? null;
  } catch (error) {
    throw error instanceof GatewayError
      ? error
      : new GatewayError(502, '换装生成失败，请稍后重试', 'UPSTREAM_GENERATE_FAILED');
  }

  // 5. 落盘作品。整单成功后新解析服装保持 ready（已在解析阶段提交为可复用商品图）。
  const resultBucket = 'media-user';
  const resultPath = `${order.clientId}/${order.userId}/generations/${order.recordId}/result.png`;
  const { error: resultUploadError } = await admin.storage.from(resultBucket).upload(resultPath, resultBytes, {
    contentType: 'image/png',
    upsert: false,
  });
  if (resultUploadError) {
    throw new GatewayError(503, '作品保存失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
  }

  return {
    resultBucket,
    resultPath,
    boardBucket,
    boardPath,
    manifest: manifest.entries,
    personaSnapshot,
    wearSnapshot,
    providerSnapshot,
    gatewayRequestId: requestId,
    gatewayUsage: usage,
    wear,
  };
}

async function updateStage(
  admin: SupabaseClient,
  recordId: string,
  order: TryonOrder,
  stage: 'parsing' | 'board' | 'generating',
) {
  await admin.from('outfit_generations').update({ stage })
    .eq('id', recordId).eq('client_id', order.clientId).eq('user_id', order.userId);
}
