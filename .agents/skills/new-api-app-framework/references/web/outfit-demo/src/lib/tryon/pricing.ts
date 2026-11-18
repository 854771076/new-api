// lib/tryon/pricing.ts — 用网关 /oauth/pricing 的模型倍率/单价，折算一次换装的展示额度。
// 仅供提交前报价与记账展示，实际扣费仍由网关 relay 按真实 token/张数对 App 钱包结算。
import 'server-only';
import { createHash } from 'node:crypto';
import { GatewayError } from '@/lib/gateway-errors';
import { fetchGatewayPricing } from '@/lib/gateway';
import type { GatewayPricing, GatewayPricingEntry } from '@/lib/gateway';

// 生图模型单张（n=1）估算：冻结提示词约 100 token，最终换装固定 2 张参考图，
// 输出一张图约 1352 token（对齐网关 gpt-image 上游 usage 实测）。
const EST_IMAGE_PROMPT_TOKENS = 100;
const EST_IMAGE_TOKENS_PER_REF = 765;
const EST_IMAGE_REF_COUNT = 2;
const EST_IMAGE_OUTPUT_TOKENS = 1352;

// 视觉模型单件解析估算：1 张服装图输入 + 冻结提示词，JSON 输出约 1300 token。
const EST_VISION_PROMPT_TOKENS = 100;
const EST_VISION_IMAGE_TOKENS = 765;
const EST_VISION_OUTPUT_TOKENS = 1300;

// tiered_expr 线性表达式里的变量系数（美元 / 1M token）。
const TOKEN_COEF_RE = /\b(img_cr|img|cr|c|p)\s*\*\s*([0-9]+(?:\.[0-9]+)?)/g;
type TieredPricing =
  | { kind: 'fixed'; priceUsd: number }
  | { kind: 'tokens'; p: number; c: number; cr: number; img: number; img_cr: number; usesImg: boolean };

interface TokenEstimates {
  /** 文本输入 token 估算（提示词）。 */
  text: number;
  /** 图像输入 token 估算。 */
  image: number;
  /** 输出 token 估算。 */
  output: number;
}

function findEntry(data: GatewayPricingEntry[], modelName: string): GatewayPricingEntry {
  const entry = data.find(e => e.model_name === modelName);
  if (!entry) throw new GatewayError(503, '所选模型没有可用报价', 'PRICING_UNAVAILABLE');
  return entry;
}

// 自动分组可能重试至其他组，展示可用组的最高倍率作为保守估算。
function effectiveGroupRatio(pricing: GatewayPricing, entry: GatewayPricingEntry): number {
  const values = Object.entries(pricing.group_ratio)
    .filter(([group]) => !entry.enable_groups?.length || entry.enable_groups.includes('all') || entry.enable_groups.includes(group))
    .map(([, value]) => value);
  if (!values.length || values.some(value => !Number.isFinite(value) || value < 0)) {
    throw new GatewayError(503, '模型分组报价不可用', 'PRICING_UNAVAILABLE');
  }
  return Math.max(...values);
}

// shortcut: 仅估算单一固定价或线性 token 表达式，复杂定价需接入网关求值接口。
function parseTieredExpr(expr: string): TieredPricing | null {
  let normalized = expr.replace(/^v1:/, '').trim();
  const tier = /^tier\(\s*"[^"\\]*"\s*,\s*(.*)\)$/.exec(normalized);
  if (tier) normalized = tier[1].trim();
  const fixed = /^fixed\(\s*([0-9]+(?:\.[0-9]+)?)\s*\)$/.exec(normalized);
  if (fixed) {
    const priceUsd = Number(fixed[1]);
    return Number.isFinite(priceUsd) ? { kind: 'fixed', priceUsd } : null;
  }
  if (!/^(?:img_cr|img|cr|c|p)\s*\*\s*[0-9]+(?:\.[0-9]+)?(?:\s*\+\s*(?:img_cr|img|cr|c|p)\s*\*\s*[0-9]+(?:\.[0-9]+)?)*$/.test(normalized)) return null;
  const coef = { p: 0, c: 0, cr: 0, img: 0, img_cr: 0 };
  for (const match of normalized.matchAll(TOKEN_COEF_RE)) coef[match[1] as keyof typeof coef] += Number(match[2]);
  if (Object.values(coef).some(value => !Number.isFinite(value))) return null;
  return { kind: 'tokens', ...coef, usesImg: /\bimg\s*\*/.test(normalized) };
}

// 单次价：fixed(amount) 内部按 amount×1e6 返回，quota = amount × quota_per_unit × ratio。
function fixedQuota(priceUsd: number, quotaPerUnit: number, ratio: number): number {
  return Math.round(priceUsd * quotaPerUnit * ratio);
}

// 线性 token 计费：对齐网关 AST 内省——表达式引用 img 时，图像输入从 p 中扣除单列计价；
// cr / img_cr 的缓存用量此处估算为 0，不计入。
function tieredTokenQuota(
  coef: { p: number; c: number; cr: number; img: number; img_cr: number; usesImg: boolean },
  tokens: TokenEstimates,
  quotaPerUnit: number,
  ratio: number,
): number {
  const usesImg = coef.usesImg;
  const p = tokens.text + (usesImg ? 0 : tokens.image);
  const img = usesImg ? tokens.image : 0;
  const rawCost = coef.p * p + coef.img * img + coef.c * tokens.output;
  return Math.round((rawCost / 1_000_000) * quotaPerUnit * ratio);
}

// 生图模型单张额度（n=1）。tiered_expr 优先按表达式折算，否则回退旧 model_price / image_ratio / model_ratio。
export function estimateImageQuota(pricing: GatewayPricing, modelName: string): number {
  const entry = findEntry(pricing.data, modelName);
  const ratio = effectiveGroupRatio(pricing, entry);
  const tiered = entry.billing_mode === 'tiered_expr' ? parseTieredExpr(entry.billing_expr ?? '') : null;
  if (entry.billing_mode === 'tiered_expr' && !tiered) throw new GatewayError(503, '当前计费表达式暂不支持可靠报价', 'PRICING_UNSUPPORTED');
  if (tiered) {
    if (tiered.kind === 'fixed') return fixedQuota(tiered.priceUsd, pricing.quota_per_unit, ratio);
    return tieredTokenQuota(tiered, {
      text: EST_IMAGE_PROMPT_TOKENS,
      image: EST_IMAGE_TOKENS_PER_REF * EST_IMAGE_REF_COUNT,
      output: EST_IMAGE_OUTPUT_TOKENS,
    }, pricing.quota_per_unit, ratio);
  }
  if (entry.quota_type === 1) return fixedQuota(entry.model_price, pricing.quota_per_unit, ratio);
  return Math.round(entry.model_ratio * (EST_IMAGE_PROMPT_TOKENS + EST_IMAGE_TOKENS_PER_REF * EST_IMAGE_REF_COUNT * (entry.image_ratio ?? 1) + EST_IMAGE_OUTPUT_TOKENS * entry.completion_ratio) * ratio);
}

// 视觉模型单件解析额度。tiered_expr 优先按表达式折算，否则回退旧 model_price / model_ratio。
export function estimateVisionQuota(pricing: GatewayPricing, modelName: string): number {
  const entry = findEntry(pricing.data, modelName);
  const ratio = effectiveGroupRatio(pricing, entry);
  const tiered = entry.billing_mode === 'tiered_expr' ? parseTieredExpr(entry.billing_expr ?? '') : null;
  if (entry.billing_mode === 'tiered_expr' && !tiered) throw new GatewayError(503, '当前计费表达式暂不支持可靠报价', 'PRICING_UNSUPPORTED');
  if (tiered) {
    if (tiered.kind === 'fixed') return fixedQuota(tiered.priceUsd, pricing.quota_per_unit, ratio);
    return tieredTokenQuota(tiered, {
      text: EST_VISION_PROMPT_TOKENS,
      image: EST_VISION_IMAGE_TOKENS,
      output: EST_VISION_OUTPUT_TOKENS,
    }, pricing.quota_per_unit, ratio);
  }
  if (entry.quota_type === 1) return Math.round(entry.model_price * pricing.quota_per_unit * ratio);
  return Math.round(entry.model_ratio * (EST_VISION_PROMPT_TOKENS + EST_VISION_IMAGE_TOKENS * (entry.image_ratio ?? 1) + EST_VISION_OUTPUT_TOKENS * entry.completion_ratio) * ratio);
}

/** 拉取网关定价并折算为 quote 需要的 parsePrice / tryonPrice。 */
export async function resolveQuotePrices(
  imageModel: string,
  visionModel: string,
): Promise<{ parsePrice: number; tryonPrice: number; quotaPerCredit: number | null; pricingVersion: string; appId: number | null; identitySource: string | null }> {
  const pricing = await fetchGatewayPricing();
  return {
    quotaPerCredit: pricing.quota_per_credit ?? null,
    pricingVersion: quotePricingVersion(pricing, imageModel, visionModel),
    appId: pricing.app_id ?? null,
    identitySource: pricing.identity_source ?? null,
    parsePrice: estimateVisionQuota(pricing, visionModel),
    tryonPrice: estimateImageQuota(pricing, imageModel),
  };
}

export function quotePricingVersion(pricing: GatewayPricing, imageModel: string, visionModel: string): string {
  const entries = [imageModel, visionModel].map(model => {
    const entry = findEntry(pricing.data, model);
    return { ...entry, enable_groups: [...(entry.enable_groups ?? [])].sort() };
  });
  const canonical = JSON.stringify([pricing.app_id, pricing.identity_source, pricing.quota_per_credit,
    pricing.quota_per_unit, pricing.group_ratio, entries], (_key, value) =>
    value && typeof value === 'object' && !Array.isArray(value)
      ? Object.fromEntries(Object.entries(value).sort(([left], [right]) => left.localeCompare(right))) : value);
  return createHash('sha256').update(canonical).digest('hex');
}
