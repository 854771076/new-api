// lib/tryon/pricing.ts — 用网关 /oauth/pricing 的模型倍率/单价，折算一次换装的展示额度。
// 仅供提交前报价与记账展示，实际扣费仍由网关 relay 按真实 token/张数对 App 钱包结算。
import 'server-only';
import { fetchGatewayPricing } from '@/lib/gateway';
import type { GatewayPricing, GatewayPricingEntry } from '@/lib/gateway';

// 每件个人服装约 1 次视觉调用（图像输入 + JSON 输出），取整估算 token 数。
const EST_VISION_TOKENS_PER_GARMENT = 1300;

function findEntry(data: GatewayPricingEntry[], modelName: string): GatewayPricingEntry | undefined {
  const exact = data.find((e) => e.model_name === modelName);
  if (exact) return exact;
  // 网关模型常带 -version 后缀；退而求其次按前缀匹配。
  return data.find((e) => modelName.startsWith(`${e.model_name}-`) || e.model_name.startsWith(`${modelName}-`));
}

// 分组倍率：优先取模型 enable_groups 里首个可用分组倍率；多分组不一致时近似取唯一值，否则 1。
function effectiveGroupRatio(pricing: GatewayPricing, entry: GatewayPricingEntry): number {
  for (const g of entry.enable_groups ?? []) {
    if (g !== 'all' && typeof pricing.group_ratio[g] === 'number') return pricing.group_ratio[g];
  }
  const values = Object.values(pricing.group_ratio).filter((v) => typeof v === 'number');
  return values.length === 1 ? values[0] : 1;
}

/** 生图模型单张额度（n=1）。价格模式取 model_price，倍率模式优先用 image_ratio（单图倍率），缺失时回退 model_ratio。 */
export function estimateImageQuota(pricing: GatewayPricing, modelName: string): number {
  const entry = findEntry(pricing.data, modelName);
  if (!entry) return 0;
  const ratio = effectiveGroupRatio(pricing, entry);
  const unit = entry.quota_type === 1
    ? entry.model_price
    : typeof entry.image_ratio === 'number'
      ? entry.image_ratio
      : entry.model_ratio;
  return Math.round(unit * pricing.quota_per_unit * ratio);
}

/** 视觉模型单件解析额度（固定 token 估算）。 */
export function estimateVisionQuota(pricing: GatewayPricing, modelName: string): number {
  const entry = findEntry(pricing.data, modelName);
  if (!entry) return 0;
  const ratio = effectiveGroupRatio(pricing, entry);
  if (entry.quota_type === 1) return Math.round(entry.model_price * pricing.quota_per_unit * ratio);
  return Math.round(entry.model_ratio * pricing.quota_per_unit / 1_000_000 * EST_VISION_TOKENS_PER_GARMENT * ratio);
}

/** 拉取网关定价并折算为 quote 需要的 parsePrice / tryonPrice。 */
export async function resolveQuotePrices(
  imageModel: string,
  visionModel: string,
): Promise<{ parsePrice: number; tryonPrice: number }> {
  const pricing = await fetchGatewayPricing();
  return {
    parsePrice: estimateVisionQuota(pricing, visionModel),
    tryonPrice: estimateImageQuota(pricing, imageModel),
  };
}
