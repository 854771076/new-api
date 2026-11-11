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
