// lib/constants.ts — 跨模块共享的常量。

/** new-api QuotaPerUnit 默认值：500,000 quota = $1 */
export const QUOTA_PER_UNIT = 500_000;

/** 面向用户的积分换算：1 积分 = 500 quota，即 1000 积分 ≈ 1 美元。 */
export const QUOTA_PER_CREDIT = 500;

/**
 * 用户上传个人服装时可选的粗分类。识别提示词以此作为目标类别，
 * 也用于个人衣橱筛选；官方服装仍保留运营自由文本 category，不强制收敛。
 */
export const GARMENT_CATEGORIES = ['上装', '下装', '连衣裙', '外套', '鞋履', '配饰'] as const;
export type GarmentCategory = (typeof GARMENT_CATEGORIES)[number];

/**
 * 应用层报价单价（单位：网关 quota）。网关 relay 仍按模型实际用量对 App 钱包计费，
 * 这里只是提交前给用户看的固定报价与记账字段，默认 0 表示不在网关计费之外另收固定费用。
 * 运营要启用固定费用时改这两个常量即可，无需改动编排逻辑。
 */
export const GARMENT_PARSE_FEE_QUOTA = 0;
export const TRYON_FEE_QUOTA = 0;
