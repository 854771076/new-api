// POST /api/generate — 换装任务入口（异步）。
// 幂等去重 → 重新校验素材/模型 → 冻结报价 → 落任务（含 provider_snapshot）→ 立即返回任务 id，
// 由前端轮询 /api/generate/[id]/advance 单步推进。网关 relay 负责实际 App 钱包计费。
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
import { PIPELINE_VERSION } from '@/lib/tryon/prompts';
import { resolveTryonModel } from '@/lib/tryon/models';
import { resolveQuotePrices } from '@/lib/tryon/pricing';
import { OUTFIT_SELECT, type WearCandidate } from '@/lib/tryon/wear';
import { z } from 'zod';

// 创建任务本身耗时极短；真正的重活拆到 /advance 单步执行以避开 Hobby 60s 硬超时。
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
  const quote = buildQuote(candidates, await resolveQuotePrices(imageModelRes.model, visionModelRes.model));

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
      provider_snapshot: { model: body.model, size, vision_model: visionModelRes.model },
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

  // 4. 立即返回任务 id，交由前端轮询 /advance 单步推进，避免 Hobby 60s 超时。
  return gatewaySuccess({ id: recordId, status: 'pending', stage: 'quoted' });
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
