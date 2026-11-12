// lib/tryon/pricing.ts — 用网关 /oauth/pricing 的模型倍率/单价，折算一次换装的展示额度。
// 仅供提交前报价与记账展示，实际扣费仍由网关 relay 按真实 token/张数对 App 钱包结算。
import 'server-only';
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
const HAS_TOKEN_COEF_RE = /\b(img_cr|img|cr|c|p)\s*\*/;

type TieredPricing =
  | { kind: 'fixed'; priceUsd: number }
  | { kind: 'tokens'; p: number; c: number; cr: number; img: number; img_cr: number };

interface TokenEstimates {
  /** 文本输入 token 估算（提示词）。 */
  text: number;
  /** 图像输入 token 估算。 */
  image: number;
  /** 输出 token 估算。 */
  output: number;
}

function findEntry(data: GatewayPricingEntry[], modelName: string): GatewayPricingEntry | undefined {
  const exact = data.find((e) => e.model_name === modelName);
  if (exact) return exact;
  // 网关模型常带 -version 后缀；退而求其次按前缀匹配。
  return data.find((e) => modelName.startsWith(`${e.model_name}-`) || e.model_name.startsWith(`${modelName}-`));
}

// 分组倍率：中继按用户所属分组统一计费（非按模型 enable_groups 逐条取倍率），
// 而 /oauth/pricing 只回传「当前用户可用分组」的倍率，未直接标注用户所属分组。
// 因此当启用且可用的分组倍率唯一时直接取用；不一致时回退默认倍率 1（展示口径，
// 登录后下单以网关精确结算为准）。
function effectiveGroupRatio(pricing: GatewayPricing, entry: GatewayPricingEntry): number {
  const usable = new Set(Object.keys(pricing.usable_group));
  const ratios = new Set<number>();
  for (const g of entry.enable_groups ?? []) {
    if (g === 'all' || !usable.has(g)) continue;
    const ratio = pricing.group_ratio[g];
    if (typeof ratio === 'number') ratios.add(ratio);
  }
  return ratios.size === 1 ? [...ratios][0] : 1;
}

// 解析 tiered_expr：单次价返回 fixed，线性 token 表达式返回各变量系数；
// 无法可靠解析（条件分支、空串等）返回 null，交给旧字段回退。
function parseTieredExpr(expr: string): TieredPricing | null {
  const normalized = expr.replace(/^v1:/, '').trim();
  if (!normalized) return null;

  const fixedMatch = /fixed\(\s*([0-9]+(?:\.[0-9]+)?)\s*\)/.exec(normalized);
  if (fixedMatch) {
    // 同一段里既含 fixed 又含 token 系数说明是多档位条件分支，无法取单一档位，回退旧字段。
    if (HAS_TOKEN_COEF_RE.test(normalized)) return null;
    const priceUsd = Number(fixedMatch[1]);
    if (Number.isFinite(priceUsd) && priceUsd >= 0) return { kind: 'fixed', priceUsd };
    return null;
  }

  if (/[?:]/.test(normalized)) return null;

  const coef = { p: 0, c: 0, cr: 0, img: 0, img_cr: 0 };
  let matched = false;
  for (const m of normalized.matchAll(TOKEN_COEF_RE)) {
    matched = true;
    coef[m[1] as keyof typeof coef] += Number(m[2]);
  }
  if (!matched) return null;
  if (coef.p === 0 && coef.c === 0 && coef.cr === 0 && coef.img === 0 && coef.img_cr === 0) return null;
  return { kind: 'tokens', ...coef };
}

// 单次价：fixed(amount) 内部按 amount×1e6 返回，quota = amount × quota_per_unit × ratio。
function fixedQuota(priceUsd: number, quotaPerUnit: number, ratio: number): number {
  return Math.round(priceUsd * quotaPerUnit * ratio);
}

// 线性 token 计费：对齐网关 AST 内省——表达式引用 img 时，图像输入从 p 中扣除单列计价；
// cr / img_cr 的缓存用量此处估算为 0，不计入。
function tieredTokenQuota(
  coef: { p: number; c: number; cr: number; img: number; img_cr: number },
  tokens: TokenEstimates,
  quotaPerUnit: number,
  ratio: number,
): number {
  const usesImg = coef.img !== 0;
  const p = tokens.text + (usesImg ? 0 : tokens.image);
  const img = usesImg ? tokens.image : 0;
  const rawCost = coef.p * p + coef.img * img + coef.c * tokens.output;
  return Math.round((rawCost / 1_000_000) * quotaPerUnit * ratio);
}

// 倍率计费：对齐网关 text_quota 的公式
// quota = (text + image×image_ratio + output×completion_ratio) × model_ratio × group_ratio。
// model_ratio / image_ratio 是无单位倍率，不乘 quota_per_unit、不除 1M。
// model_ratio 由网关 GetModelRatio 填充（未配置时回退 37.5），倍率模式下恒为有效值，无需此处兜底。
function ratioQuota(
  tokens: TokenEstimates,
  entry: GatewayPricingEntry,
  ratio: number,
): number {
  const imageRatio = entry.image_ratio && entry.image_ratio > 0 ? entry.image_ratio : 1;
  const completionRatio = entry.completion_ratio > 0 ? entry.completion_ratio : 1;
  const weighted = tokens.text + tokens.image * imageRatio + tokens.output * completionRatio;
  return Math.round(weighted * entry.model_ratio * ratio);
}

// 生图模型单张额度（n=1）。tiered_expr 优先按表达式折算，否则回退旧 model_price / image_ratio / model_ratio。
export function estimateImageQuota(pricing: GatewayPricing, modelName: string): number {
  const entry = findEntry(pricing.data, modelName);
  if (!entry) return 0;
  const ratio = effectiveGroupRatio(pricing, entry);
  const tiered = entry.billing_mode === 'tiered_expr' ? parseTieredExpr(entry.billing_expr ?? '') : null;
  if (tiered) {
    if (tiered.kind === 'fixed') return fixedQuota(tiered.priceUsd, pricing.quota_per_unit, ratio);
    return tieredTokenQuota(tiered, {
      text: EST_IMAGE_PROMPT_TOKENS,
      image: EST_IMAGE_TOKENS_PER_REF * EST_IMAGE_REF_COUNT,
      output: EST_IMAGE_OUTPUT_TOKENS,
    }, pricing.quota_per_unit, ratio);
  }
  if (entry.quota_type === 1) return Math.round(entry.model_price * pricing.quota_per_unit * ratio);
  return ratioQuota({
    text: EST_IMAGE_PROMPT_TOKENS,
    image: EST_IMAGE_TOKENS_PER_REF * EST_IMAGE_REF_COUNT,
    output: EST_IMAGE_OUTPUT_TOKENS,
  }, entry, ratio);
}

// 视觉模型单件解析额度。tiered_expr 优先按表达式折算，否则回退旧 model_price / model_ratio。
export function estimateVisionQuota(pricing: GatewayPricing, modelName: string): number {
  const entry = findEntry(pricing.data, modelName);
  if (!entry) return 0;
  const ratio = effectiveGroupRatio(pricing, entry);
  const tiered = entry.billing_mode === 'tiered_expr' ? parseTieredExpr(entry.billing_expr ?? '') : null;
  if (tiered) {
    if (tiered.kind === 'fixed') return fixedQuota(tiered.priceUsd, pricing.quota_per_unit, ratio);
    return tieredTokenQuota(tiered, {
      text: EST_VISION_PROMPT_TOKENS,
      image: EST_VISION_IMAGE_TOKENS,
      output: EST_VISION_OUTPUT_TOKENS,
    }, pricing.quota_per_unit, ratio);
  }
  if (entry.quota_type === 1) return Math.round(entry.model_price * pricing.quota_per_unit * ratio);
  return ratioQuota({
    text: EST_VISION_PROMPT_TOKENS,
    image: EST_VISION_IMAGE_TOKENS,
    output: EST_VISION_OUTPUT_TOKENS,
  }, entry, ratio);
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
