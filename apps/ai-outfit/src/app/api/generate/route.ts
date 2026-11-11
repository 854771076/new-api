// POST /api/generate — 六阶段换装管线入口。
// 幂等去重 → 重新校验素材/模型 → 冻结报价 → 落任务 → 编排（人物标准化/按需解析/服装板/两图换装）
// → 成功提交作品与快照；失败标记并回滚本次新解析服装。网关 relay 负责实际 App 钱包计费。
import 'server-only';
import { createHash } from 'node:crypto';
import { type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { config } from '@/lib/config';
import { GatewayError } from '@/lib/gateway-errors';
import { getGatewayAccessToken } from '@/lib/gateway-session';
import { gatewaySuccess, gatewayFailure, requireGatewayOrigin } from '@/lib/gateway-bff';
import { buildQuote } from '@/lib/tryon/quote';
import { runTryon, type TryonOrder } from '@/lib/tryon/orchestrate';
import { PIPELINE_VERSION } from '@/lib/tryon/prompts';
import { resolveTryonModel } from '@/lib/tryon/models';
import type { OutfitGeneration } from '@/lib/types';
import type { WearCandidate } from '@/lib/tryon/wear';
import { z } from 'zod';

// Vercel Hobby 套餐将 Serverless 函数硬限制在 60s（声明更高也会被钳制）。
// 多模型换装管线可能逼近该上限；超时由前端按 504 给出可重试提示，长期方案是异步任务化。
export const maxDuration = 60;

const bodySchema = z.object({
  character_id: z.string().uuid(),
  outfit_ids: z.array(z.string().uuid()).min(1).max(6).refine(ids => new Set(ids).size === ids.length),
  pose_id: z.string().uuid(),
  model: z.string().min(1).max(200),
  size: z.enum(['auto', '1024x1024', '1024x1536', '1536x1024']).optional(),
  prompt_extra: z.string().max(500).optional(),
  // 客户端为同一组选择生成的稳定键；缺省时由服务端按入参派生，仍可去重完全相同的重复提交。
  idempotency_key: z.string().min(8).max(200).optional(),
}).strict();

const OUTFIT_SELECT = 'id, source, category, name_zh, user_id, bucket, thumb_path, preview_path, parse_status, product_bucket, product_path, garment_type, semantic_hints, original_bucket, original_path, parse_attempts, is_active';

export async function POST(req: NextRequest) {
  try { requireGatewayOrigin(req); } catch (error) { return gatewayFailure(error); }
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return gatewayFailure(new GatewayError(401, '请重新登录后生成', 'AUTH_SESSION_EXPIRED'));
  try { await getGatewayAccessToken(); } catch (error) { return gatewayFailure(error); }

  let body: z.infer<typeof bodySchema>;
  try { body = bodySchema.parse(await req.json()); }
  catch {
    return gatewayFailure(new GatewayError(400, '请选择有效素材，服装最多 6 套，提示词最多 500 字', 'INVALID_BODY'));
  }

  const clientId = config.gatewayClientId;
  const admin = createAdminSupabase();
  const idempotencyKey = body.idempotency_key ?? deriveIdempotencyKey(body);

  // 0. 幂等：同用户同键重复提交直接返回原任务，不重复创建/扣费。
  const { data: existing } = await admin.from('outfit_generations')
    .select('id, status, stage, result_bucket, result_path')
    .eq('client_id', clientId).eq('user_id', user.id).eq('idempotency_key', idempotencyKey)
    .maybeSingle();
  if (existing) {
    return gatewaySuccess({ id: existing.id, result_path: existing.result_path, result_bucket: existing.result_bucket, deduped: true });
  }

  // 1. 提交时重新校验素材归属与可用性（防止报价后被下架/替换）。
  const [{ data: character }, { data: outfits }, { data: pose }] = await Promise.all([
    supabase.from('characters')
      .select('id, name, bucket, image_path, cover_path, persona_status, persona_version')
      .eq('id', body.character_id).eq('user_id', user.id).eq('client_id', clientId).maybeSingle(),
    supabase.from('outfits').select(OUTFIT_SELECT)
      .in('id', body.outfit_ids).eq('client_id', clientId),
    supabase.from('poses').select('id, name_zh, system, category, angle, bucket, thumb_path, preview_path')
      .eq('id', body.pose_id).eq('client_id', clientId).eq('is_active', true).maybeSingle(),
  ]);

  if (!character) return gatewayFailure(new GatewayError(404, '人物不存在或无权限', 'CHARACTER_NOT_FOUND'));
  if (!pose) return gatewayFailure(new GatewayError(404, '姿势不存在', 'POSE_NOT_FOUND'));
  const candidates = (outfits ?? []).filter((o) => o.is_active) as WearCandidate[];
  if (candidates.length !== body.outfit_ids.length) {
    return gatewayFailure(new GatewayError(400, '所选服装已失效，请重新选择', 'OUTFIT_NOT_AVAILABLE'));
  }
  // 个人服装只能由所有者在换装中触发解析（RLS 已限定，服务端再显式拦截）。
  if (candidates.some((o) => o.source === 'user' && o.user_id !== user.id)) {
    return gatewayFailure(new GatewayError(403, '存在无权使用的个人服装', 'OUTFIT_FORBIDDEN'));
  }

  // 2. 模型与报价：模型必须可用；报价按当前素材实时计算并冻结进任务。
  const [imageModelRes, visionModelRes] = await Promise.all([
    resolveTryonModel(supabase, 'image'),
    resolveTryonModel(supabase, 'vision'),
  ]);
  if (body.model !== imageModelRes.model) {
    return gatewayFailure(new GatewayError(400, '所选模型不可用', 'MODEL_NOT_AVAILABLE'));
  }
  const size = body.size ?? imageModelRes.defaultSize ?? '1024x1536';
  if (!bodySchema.shape.size.unwrap().safeParse(size).success) {
    return gatewayFailure(new GatewayError(400, '模型图片尺寸配置不可用，请联系管理员', 'INVALID_IMAGE_SIZE'));
  }
  const quote = buildQuote(candidates);

  // 3. 一次性创建任务（部分唯一索引兜底并发重复提交）。
  const { data: pendingRow, error: insertErr } = await admin
    .from('outfit_generations').insert({
      client_id: clientId,
      user_id: user.id,
      character_id: character.id,
      outfit_ids: body.outfit_ids,
      pose_id: pose.id,
      prompt: body.prompt_extra?.trim() || null,
      model: body.model,
      status: 'pending',
      stage: 'quoted',
      idempotency_key: idempotencyKey,
      quote,
      quote_accepted_at: new Date().toISOString(),
      parse_fee_quota: quote.parse_fee_quota,
      tryon_fee_quota: quote.tryon_fee_quota,
      pipeline_version: PIPELINE_VERSION,
    }).select('id').single();

  if (insertErr || !pendingRow) {
    const { data: raced } = await admin.from('outfit_generations')
      .select('id, result_bucket, result_path')
      .eq('client_id', clientId).eq('user_id', user.id).eq('idempotency_key', idempotencyKey)
      .maybeSingle();
    if (raced) return gatewaySuccess({ id: raced.id, result_path: raced.result_path, result_bucket: raced.result_bucket, deduped: true });
    return gatewayFailure(new GatewayError(500, '无法创建生成记录，请稍后重试', 'UPSTREAM_DATABASE_ERROR'));
  }
  const recordId = pendingRow.id;

  // 4. 编排执行。
  const order: TryonOrder = {
    recordId,
    clientId,
    userId: user.id,
    character,
    candidates,
    quote,
    imageModel: imageModelRes.model,
    visionModel: visionModelRes.model,
    size,
  };

  try {
    const result = await runTryon(supabase, order);
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
      charged_quota: quote.total_quota,
      gateway_request_id: result.gatewayRequestId ?? null,
      gateway_usage: result.gatewayUsage ?? null,
    } satisfies Partial<OutfitGeneration>)
      .eq('id', recordId).eq('user_id', user.id).eq('client_id', clientId);

    if (updateErr) throw new GatewayError(500, '生成结果保存失败，请稍后重试', 'UPSTREAM_DATABASE_ERROR');
    return gatewaySuccess({ id: recordId, result_path: result.resultPath, result_bucket: result.resultBucket });
  } catch (error) {
    const safe = error instanceof GatewayError
      ? error
      : new GatewayError(502, '生成失败，请稍后重试', 'UPSTREAM_GENERATE_FAILED');
    await admin.from('outfit_generations').update({
      status: 'failed',
      stage: 'failed',
      error: safe.message,
      refunded_quota: quote.total_quota,
    }).eq('id', recordId).eq('user_id', user.id).eq('client_id', clientId);
    return gatewayFailure(safe);
  }
}

function deriveIdempotencyKey(body: z.infer<typeof bodySchema>): string {
  const basis = JSON.stringify({
    c: body.character_id,
    o: [...body.outfit_ids].sort(),
    p: body.pose_id,
    m: body.model,
    s: body.size ?? null,
  });
  // SHA-256 而不是短整型哈希：同一用户累积大量任务时避免生日碰撞把不同选择误判为重复提交。
  const digest = createHash('sha256').update(basis).digest('hex');
  return `auto-${digest}-${body.outfit_ids.length}`;
}
