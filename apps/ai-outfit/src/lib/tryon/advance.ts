// lib/tryon/advance.ts — 可恢复换装推进器：把六阶段管线拆成单步推进。
// 每步由浏览器带 session 调用一次 /advance（网关访问令牌依赖请求 cookie，无法后台执行），
// 且单步耗时 < Vercel Hobby 60s 超时。阶段产物都落库，中断后重进即可从持久化状态续跑。
import 'server-only';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { config } from '@/lib/config';
import { GatewayError } from '@/lib/gateway-errors';
import { ensurePersona } from './persona';
import {
  resolveWearItems, OUTFIT_SELECT, type ResolveWearContext, type WearCandidate,
} from './wear';
import { parsePersonalGarment, type ParseableGarment } from './parse-garment';
import { runBoardAndTryon, type TryonOrder } from './orchestrate';
import { rollbackParsedGarments } from './rollback';
import { needsParsing } from './quote';
import type { GenerationQuote, GenerationStatus, PersonaSnapshot, PoseSnapshot, ProviderSnapshot } from '@/lib/types';

export interface AdvanceResult {
  status: 'pending' | 'succeeded' | 'failed';
  stage: string;
  result_bucket: string | null;
  result_path: string | null;
  error: string | null;
}

interface TaskRow {
  id: string;
  client_id: string;
  user_id: string;
  character_id: string;
  outfit_ids: string[];
  model: string;
  status: GenerationStatus;
  stage: string;
  quote: GenerationQuote | null;
  persona_snapshot: PersonaSnapshot | null;
  provider_snapshot: ProviderSnapshot | null;
  pose_id: string | null;
  pose_snapshot: PoseSnapshot | null;
  result_bucket: string | null;
  result_path: string | null;
  error: string | null;
}

/**
 * 单步推进一个任务直到本步结束，返回当前状态。幂等：重复调用会从持久化状态推导下一步，
 * 已终态任务直接返回，不重复执行。
 */
export async function advanceTryon(recordId: string, userId: string): Promise<AdvanceResult> {
  const admin = createAdminSupabase();
  const clientId = config.gatewayClientId;

  const { data: task } = await admin.from('outfit_generations')
    .select('id, client_id, user_id, character_id, outfit_ids, model, status, stage, quote, persona_snapshot, provider_snapshot, pose_id, pose_snapshot, result_bucket, result_path, error')
    .eq('id', recordId).eq('client_id', clientId).eq('user_id', userId)
    .maybeSingle();
  if (!task) throw new GatewayError(404, '生成任务不存在', 'GENERATION_NOT_FOUND');

  const row = task as TaskRow;
  if (row.status === 'succeeded' || row.status === 'failed') {
    return {
      status: row.status,
      stage: row.stage,
      result_bucket: row.result_bucket,
      result_path: row.result_path,
      error: row.error,
    };
  }

  const provider = (row.provider_snapshot ?? {}) as ProviderSnapshot;
  const imageModel = provider.model || row.model;
  const visionModel = provider.vision_model ?? '';
  const size = provider.size ?? '1024x1536';
  const quality = typeof provider.quality === 'string' && provider.quality.trim() ? provider.quality.trim() : undefined;

  const [{ data: character }, { data: outfits }] = await Promise.all([
    admin.from('characters').select('id, name, bucket, image_path, cover_path, persona_status, persona_version')
      .eq('id', row.character_id).eq('client_id', clientId).eq('user_id', userId).maybeSingle(),
    admin.from('outfits').select(OUTFIT_SELECT).in('id', row.outfit_ids).eq('client_id', clientId),
  ]);
  if (!character) throw new GatewayError(404, '人物不存在或无权限', 'CHARACTER_NOT_FOUND');
  const candidates = ((outfits ?? []) as (WearCandidate & { is_active?: boolean })[])
    .filter((o) => o.is_active);

  // Step 1：人物标准化（ready 且存在 cover 时复用，不重复生成/扣费）。
  if (!row.persona_snapshot) {
    try {
      const persona = await ensurePersona(admin, {
        character, clientId, userId, imageModel, size,
      });
      const personaSnapshot: PersonaSnapshot = {
        character_id: character.id,
        persona_version: persona.personaVersion,
        bucket: persona.bucket,
        path: persona.path,
      };
      await admin.from('outfit_generations').update({ stage: 'parsing', persona_snapshot: personaSnapshot })
        .eq('id', recordId).eq('client_id', clientId).eq('user_id', userId);
      return { status: 'pending', stage: 'parsing', result_bucket: null, result_path: null, error: null };
    } catch (error) {
      return markFailed(admin, row, clientId, userId, error);
    }
  }

  // Step 2：逐件解析未就绪个人服装（官方服装与已就绪商品图直接跳过）。
  const nextGarment = candidates.find(needsParsing);
  if (nextGarment) {
    const parseable: ParseableGarment = {
      id: nextGarment.id,
      category: nextGarment.category,
      name_zh: nextGarment.name_zh,
      bucket: nextGarment.bucket,
      original_bucket: nextGarment.original_bucket,
      original_path: nextGarment.original_path,
      thumb_path: nextGarment.thumb_path,
      parse_status: nextGarment.parse_status as ParseableGarment['parse_status'],
      parse_attempts: nextGarment.parse_attempts ?? 0,
    };
    try {
      await parsePersonalGarment(admin, parseable, { clientId, userId, visionModel, imageModel });
      return { status: 'pending', stage: 'parsing', result_bucket: null, result_path: null, error: null };
    } catch (error) {
      return markFailed(admin, row, clientId, userId, error);
    }
  }

  // Step 3：拼板 + 三图换装 + 落盘（单步 ~50s，合并为一个 < 60s 推进步）。
  if (!row.result_path) {
    const personaSnapshot = row.persona_snapshot as PersonaSnapshot;
    const persona = {
      bucket: personaSnapshot.bucket,
      path: personaSnapshot.path,
      personaVersion: personaSnapshot.persona_version,
    };
    const ctx: ResolveWearContext = { clientId, userId, visionModel, imageModel };
    // 姿势参考图优先用落库快照，缺失时按 pose_id 回查（兼容迁移前已建任务）。
    const poseSnapshot = row.pose_snapshot ?? await (async () => {
      if (!row.pose_id) return null;
      const { data: pose } = await admin.from('poses')
        .select('id, bucket, preview_path')
        .eq('id', row.pose_id).eq('client_id', clientId).maybeSingle();
      return pose ? { pose_id: pose.id, bucket: pose.bucket, path: pose.preview_path } : null;
    })();
    if (!poseSnapshot) {
      return markFailed(admin, row, clientId, userId, new GatewayError(404, '姿势不存在', 'POSE_NOT_FOUND'));
    }
    const order: TryonOrder = {
      recordId,
      clientId,
      userId,
      character,
      candidates,
      quote: row.quote ?? {
        parse_fee_quota: 0,
        tryon_fee_quota: 0,
        total_quota: 0,
        unparsed_garment_ids: [],
        item_count: candidates.length,
      },
      pose: { bucket: poseSnapshot.bucket, path: poseSnapshot.path },
      imageModel,
      visionModel,
      size,
      quality,
    };
    try {
      const wear = await resolveWearItems(admin, candidates, ctx);
      const result = await runBoardAndTryon(admin, order, persona, personaSnapshot, wear);
      const { error: updateErr } = await admin.from('outfit_generations').update({
        status: 'succeeded',
        stage: 'done',
        result_bucket: result.resultBucket,
        result_path: result.resultPath,
        board_bucket: result.boardBucket,
        board_path: result.boardPath,
        garment_manifest: result.manifest,
        persona_snapshot: result.personaSnapshot,
        wear_snapshot: result.wearSnapshot,
        provider_snapshot: result.providerSnapshot,
        charged_quota: row.quote?.total_quota ?? 0,
        gateway_request_id: result.gatewayRequestId ?? null,
        gateway_usage: result.gatewayUsage ?? null,
      }).eq('id', recordId).eq('client_id', clientId).eq('user_id', userId);
      if (updateErr) throw new GatewayError(503, '生成结果保存失败，请稍后重试', 'UPSTREAM_DATABASE_ERROR');
      return {
        status: 'succeeded',
        stage: 'done',
        result_bucket: result.resultBucket,
        result_path: result.resultPath,
        error: null,
      };
    } catch (error) {
      return markFailed(admin, row, clientId, userId, error);
    }
  }

  // result_path 已存在但状态未终结（理论不应发生），按成功返回。
  return { status: 'succeeded', stage: 'done', result_bucket: row.result_bucket, result_path: row.result_path, error: null };
}

/**
 * 把任务标记为终态 failed：回滚本次新解析服装、记录错误并退还报价额度。
 * 人物标准化、逐件解析、拼板换装任一阶段失败都走这里（与旧同步管线一致：任一阶段失败即整单失败）。
 */
async function markFailed(
  admin: ReturnType<typeof createAdminSupabase>,
  row: TaskRow,
  clientId: string,
  userId: string,
  error: unknown,
): Promise<AdvanceResult> {
  await rollbackParsedGarments(admin, row.quote?.unparsed_garment_ids ?? [], { clientId, userId });
  const safe = error instanceof GatewayError
    ? error
    : new GatewayError(502, '换装生成失败，请稍后重试', 'UPSTREAM_GENERATE_FAILED');
  await admin.from('outfit_generations').update({
    status: 'failed',
    stage: 'failed',
    error: safe.message,
    refunded_quota: row.quote?.total_quota ?? 0,
  }).eq('id', row.id).eq('client_id', clientId).eq('user_id', userId);
  return { status: 'failed', stage: 'failed', result_bucket: null, result_path: null, error: safe.message };
}
