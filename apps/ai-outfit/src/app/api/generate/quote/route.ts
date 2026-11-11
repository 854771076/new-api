// POST /api/generate/quote — 提交前报价（不创建任务、不扣费、不调用模型）。
// 重新校验人物/服装/姿势归属与可用性，仅把本次未解析个人服装计入解析费。
import 'server-only';
import { type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { config } from '@/lib/config';
import { GatewayError } from '@/lib/gateway-errors';
import { getGatewayAccessToken } from '@/lib/gateway-session';
import { gatewaySuccess, gatewayFailure, requireGatewayOrigin } from '@/lib/gateway-bff';
import { buildQuote } from '@/lib/tryon/quote';
import { resolveTryonModel } from '@/lib/tryon/models';
import { resolveQuotePrices } from '@/lib/tryon/pricing';
import { QUOTA_PER_CREDIT } from '@/lib/constants';
import type { WearCandidate } from '@/lib/tryon/wear';
import { z } from 'zod';

const bodySchema = z.object({
  character_id: z.string().uuid(),
  outfit_ids: z.array(z.string().uuid()).min(1).max(6).refine(ids => new Set(ids).size === ids.length),
  pose_id: z.string().uuid(),
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
    return gatewayFailure(new GatewayError(400, '请选择有效素材，服装最多 6 套', 'INVALID_BODY'));
  }

  const clientId = config.gatewayClientId;
  try {
    const [{ data: character }, { data: outfits }, { data: pose }] = await Promise.all([
      supabase.from('characters').select('id')
        .eq('id', body.character_id).eq('user_id', user.id).eq('client_id', clientId).maybeSingle(),
      supabase.from('outfits')
        .select('id, source, category, name_zh, user_id, bucket, thumb_path, preview_path, parse_status, product_bucket, product_path, garment_type, semantic_hints, original_bucket, original_path, parse_attempts, is_active')
        .in('id', body.outfit_ids).eq('client_id', clientId),
      supabase.from('poses').select('id')
        .eq('id', body.pose_id).eq('client_id', clientId).eq('is_active', true).maybeSingle(),
    ]);

    if (!character) return gatewayFailure(new GatewayError(404, '人物不存在或无权限', 'CHARACTER_NOT_FOUND'));
    if (!pose) return gatewayFailure(new GatewayError(404, '姿势不存在', 'POSE_NOT_FOUND'));
    const activeOutfits = (outfits ?? []).filter((o) => o.is_active);
    if (activeOutfits.length !== body.outfit_ids.length) {
      return gatewayFailure(new GatewayError(400, '所选服装已失效，请重新选择', 'OUTFIT_NOT_AVAILABLE'));
    }

    const [imageModelRes, visionModelRes] = await Promise.all([
      resolveTryonModel(supabase, 'image'),
      resolveTryonModel(supabase, 'vision'),
    ]);
    const prices = await resolveQuotePrices(imageModelRes.model, visionModelRes.model);
    const quote = buildQuote(activeOutfits as WearCandidate[], prices);
    return gatewaySuccess({
      quote,
      credits: quote.total_quota / QUOTA_PER_CREDIT,
      item_count: quote.item_count,
      unparsed_count: quote.unparsed_garment_ids.length,
    });
  } catch (error) {
    return gatewayFailure(error);
  }
}
