// lib/tryon/quote.ts — 提交前报价：本次未解析个人服装的解析费 + 一次换装制作费。
// 单价来自 constants（默认 0）；网关 relay 的实际模型用量计费不受这里影响。
import 'server-only';
import { GARMENT_PARSE_FEE_QUOTA, TRYON_FEE_QUOTA } from '@/lib/constants';
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
export function buildQuote(candidates: WearCandidate[]): GenerationQuote {
  const unparsed = candidates.filter(needsParsing);
  const parseFee = GARMENT_PARSE_FEE_QUOTA * unparsed.length;
  const total = parseFee + TRYON_FEE_QUOTA;
  return {
    parse_fee_quota: parseFee,
    tryon_fee_quota: TRYON_FEE_QUOTA,
    total_quota: total,
    unparsed_garment_ids: unparsed.map((g) => g.id),
    item_count: candidates.length,
  };
}
