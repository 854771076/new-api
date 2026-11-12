// lib/tryon/models.ts — 解析管线各阶段使用的网关模型（当前 App 优先，全局默认行兜底）。
import 'server-only';
import type { SupabaseClient } from '@supabase/supabase-js';
import { config } from '@/lib/config';
import { GatewayError } from '@/lib/gateway-errors';

export type TryonModelUsage = 'image' | 'vision';

interface ModelRow {
  model_id: string;
  client_id: string;
  default_size: string | null;
  default_quality: string | null;
  is_default: boolean | null;
}

/**
 * 按 usage 取一个可用模型：优先当前 client 的默认/首行，回退全局共享行（client_id=''）。
 * image 同时返回默认出图尺寸与质量；vision 不消费这些参数。单价不再本地维护，报价走网关 /oauth/pricing。
 */
export async function resolveTryonModel(
  supabase: SupabaseClient,
  usage: TryonModelUsage,
): Promise<{ model: string; defaultSize: string | null; defaultQuality: string | null }> {
  const { data, error } = await supabase
    .from('ai_models')
    .select('model_id, client_id, default_size, default_quality, is_default')
    .eq('usage', usage)
    .eq('is_active', true)
    .in('client_id', [config.gatewayClientId, ''])
    .order('sort_order', { ascending: true })
    .order('id', { ascending: true });

  if (error) {
    throw new GatewayError(503, '模型配置暂时不可用，请稍后重试', 'AI_TRYON_MODEL_UNAVAILABLE');
  }
  const rows = (data ?? []) as ModelRow[];
  const clientRows = rows.filter((r) => r.client_id === config.gatewayClientId);
  const pool = clientRows.length > 0 ? clientRows : rows;
  const row = pool.find((r) => r.is_default) ?? pool[0];
  if (!row) {
    const label = usage === 'vision' ? '视觉理解模型' : '生图模型';
    throw new GatewayError(400, `未配置可用的${label}，请联系管理员`, 'AI_TRYON_MODEL_UNAVAILABLE');
  }
  return { model: row.model_id, defaultSize: row.default_size ?? null, defaultQuality: row.default_quality ?? null };
}
