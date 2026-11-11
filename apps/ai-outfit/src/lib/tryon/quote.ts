// lib/tryon/quote.ts — 提交前报价：本次未解析个人服装的解析费 + 一次换装制作费。
// 单价由网关 /oauth/pricing 折算（换装费 = 生图模型单张额度，解析费 = 视觉模型单件估算额度 × 未解析件数）；
// 网关 relay 仍按模型实际用量对 App 钱包计费，这里只是提交前展示与记账字段。
import 'server-only';
import type { GenerationQuote } from '@/lib/types';
import type { WearCandidate } from './wear';

/** 一件个人服装是否需要在本次任务里现场解析（官方服装与已就绪商品图都不计解析费）。 */
export function needsParsing(garment: WearCandidate): boolean {
  return garment.source === 'user'
    && !(garment.parse_status === 'ready' && garment.product_bucket && garment.product_path);
}

/**
 * 仅把本次未解析的个人服装计入解析费用，再加一次换装制作费。
 * 同一输入在任何时刻得到相同报价，保证「提交时重新校验价格」与提交前展示一致。
 */
export function buildQuote(
  candidates: WearCandidate[],
  pricing: { parsePrice: number; tryonPrice: number },
): GenerationQuote {
  const unparsed = candidates.filter(needsParsing);
  const parseFee = pricing.parsePrice * unparsed.length;
  const total = parseFee + pricing.tryonPrice;
  return {
    parse_fee_quota: parseFee,
    tryon_fee_quota: pricing.tryonPrice,
    total_quota: total,
    unparsed_garment_ids: unparsed.map((g) => g.id),
    item_count: candidates.length,
  };
}
